package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/transfer"
	"sort"
	"time"
)

const EventLimit = 10000
const EventTTL = 24 * time.Hour

// appendEvent is called in the transaction that changes the authoritative row.
// The counter is never derived from retained rows, so pruning cannot reuse IDs.
func (s *Store) appendEvent(ctx context.Context, tx *sql.Tx, id, kind string, item *transfer.Item) error {
	r, err := s.transfer(ctx, tx, id)
	if err != nil {
		return err
	}
	var sequence uint64
	if err = tx.QueryRowContext(ctx, "UPDATE jobs SET event_sequence=event_sequence+1 WHERE job_id=? RETURNING event_sequence", id).Scan(&sequence); err != nil {
		return err
	}
	now := time.Now().UTC()
	e := transfer.Event{Sequence: sequence, Type: kind, OccurredAt: now.Format(time.RFC3339Nano), Record: &r, Item: item}
	data, err := s.sealValue(fmt.Sprintf("event/%s/%d", id, sequence), e)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO job_events(job_id,sequence,event_type,payload,created_at) VALUES(?,?,?,?,?)", id, sequence, kind, data, e.OccurredAt); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "DELETE FROM job_events WHERE job_id=? AND (sequence<=? OR created_at<?)", id, int64(sequence)-EventLimit, now.Add(-EventTTL).Format(time.RFC3339Nano))
	return err
}
func (s *Store) EventSnapshot(ctx context.Context, id string) (transfer.Record, uint64, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return transfer.Record{}, 0, err
	}
	defer tx.Rollback()
	r, err := s.transfer(ctx, tx, id)
	if err != nil {
		return r, 0, err
	}
	var head uint64
	err = tx.QueryRowContext(ctx, "SELECT event_sequence FROM jobs WHERE job_id=?", id).Scan(&head)
	return r, head, err
}
func (s *Store) TransferEvents(ctx context.Context, id string, after uint64, limit int) (transfer.EventBatch, error) {
	var out transfer.EventBatch
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(ctx, "SELECT event_sequence FROM jobs WHERE job_id=?", id).Scan(&out.Head)
	if err == sql.ErrNoRows {
		return out, fault.New(fault.NotFound, "RESOURCE_NOT_FOUND", "任务不存在")
	}
	if err != nil {
		return out, err
	}
	var oldest sql.NullInt64
	if err = tx.QueryRowContext(ctx, "SELECT min(sequence) FROM job_events WHERE job_id=? AND created_at>=?", id, time.Now().Add(-EventTTL).UTC().Format(time.RFC3339Nano)).Scan(&oldest); err != nil {
		return out, err
	}
	out.Oldest = uint64(oldest.Int64)
	if after > out.Head {
		return out, fault.New(fault.Conflict, "EVENT_CURSOR_FUTURE", "事件游标超出当前序号")
	}
	if out.Head > 0 && !oldest.Valid || after < out.Head && after+1 < out.Oldest {
		return out, fault.New(fault.Gone, "EVENT_CURSOR_EXPIRED", "事件游标已超过保留窗口")
	}
	if limit < 1 || limit > EventLimit {
		limit = 128
	}
	rows, err := tx.QueryContext(ctx, "SELECT sequence,payload FROM job_events WHERE job_id=? AND sequence>? AND created_at>=? ORDER BY sequence LIMIT ?", id, after, time.Now().Add(-EventTTL).UTC().Format(time.RFC3339Nano), limit)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	out.Events = []transfer.Event{}
	for rows.Next() {
		var seq uint64
		var data []byte
		var e transfer.Event
		if err = rows.Scan(&seq, &data); err != nil {
			return out, err
		}
		if err = s.openValue(fmt.Sprintf("event/%s/%d", id, seq), data, &e); err != nil {
			return out, err
		}
		out.Events = append(out.Events, e)
	}
	return out, rows.Err()
}
func (s *Store) SaveScanItem(ctx context.Context, id string, item transfer.Item, result transfer.ScanResult) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r, err := s.transfer(ctx, tx, id)
	if err != nil {
		return err
	}
	if r.State != "running" && r.State != "cancelling" && r.State != "pausing" {
		return fault.New(fault.Conflict, "STATE_CONFLICT", "扫描任务已停止")
	}
	payload, err := s.sealValue(fmt.Sprintf("item/%s/%d", id, item.Index), item)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO job_items(job_id,item_index,payload,state) VALUES(?,?,?,?)", id, item.Index, payload, item.State); err != nil {
		return err
	}
	r.ScanResult = &result
	if err = s.saveRecord(ctx, tx, r); err != nil {
		return err
	}
	if err = s.appendEvent(ctx, tx, id, "item.completed", &item); err != nil {
		return err
	}
	if err = s.appendEvent(ctx, tx, id, "job.progress", nil); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) saveRecord(ctx context.Context, tx *sql.Tx, r transfer.Record) error {
	data, err := s.sealValue("job/"+r.ID, r)
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, "UPDATE jobs SET record=?,updated_at=?,version=version+1 WHERE job_id=? AND version=?", data, time.Now().UTC().Format(time.RFC3339Nano), r.ID, r.Version)
	if err != nil {
		return err
	}
	return oneRow(res)
}
func (s *Store) SaveScanResult(ctx context.Context, id string, result transfer.ScanResult) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r, err := s.transfer(ctx, tx, id)
	if err != nil {
		return err
	}
	if r.State != "running" && r.State != "cancelling" && r.State != "pausing" {
		return fault.New(fault.Conflict, "STATE_CONFLICT", "扫描任务已停止")
	}
	r.ScanResult = &result
	if err = s.saveRecord(ctx, tx, r); err != nil {
		return err
	}
	if err = s.appendEvent(ctx, tx, id, "job.progress", nil); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) TransferItems(ctx context.Context, id string, limit int) ([]transfer.Item, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT item_index,payload FROM job_items WHERE job_id=? ORDER BY item_index LIMIT ?", id, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []transfer.Item{}
	for rows.Next() {
		var i uint64
		var data []byte
		var item transfer.Item
		if err = rows.Scan(&i, &data); err != nil {
			return nil, err
		}
		if err = s.openValue(fmt.Sprintf("item/%s/%d", id, i), data, &item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
func (s *Store) ListTransfers(ctx context.Context, f transfer.Filter, limit int, visible func(transfer.Record) bool) ([]transfer.Record, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// The SQL time predicate is inclusive because julianday has coarser precision
	// than RFC3339Nano. Apply the exact exclusive bounds in Go before counting.
	rows, err := tx.QueryContext(ctx, `SELECT job_id,state,record,updated_at,started_at,finished_at,version FROM jobs WHERE (?='' OR owner=?) AND (?='' OR account_id=?) AND (?='' OR kind=?) AND (?='' OR state=?) AND (?='' OR julianday(created_at)>=julianday(?)) AND (?='' OR julianday(created_at)<=julianday(?)) ORDER BY created_at DESC,job_id DESC`, f.Owner, f.Owner, f.AccountID, f.AccountID, f.Kind, f.Kind, f.State, f.State, f.After, f.After, f.Before, f.Before)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var after, before time.Time
	if f.After != "" {
		after, err = time.Parse(time.RFC3339Nano, f.After)
		if err != nil {
			return nil, err
		}
	}
	if f.Before != "" {
		before, err = time.Parse(time.RFC3339Nano, f.Before)
		if err != nil {
			return nil, err
		}
	}
	out := []transfer.Record{}
	size := 0
	for rows.Next() {
		var id, state, updated string
		var data []byte
		var started, finished sql.NullString
		var version uint64
		if err = rows.Scan(&id, &state, &data, &updated, &started, &finished, &version); err != nil {
			return nil, err
		}
		r, err := s.decodeTransfer(id, state, data, updated, started, finished, version)
		if err != nil {
			return nil, err
		}
		created, err := time.Parse(time.RFC3339Nano, r.CreatedAt)
		if err != nil {
			return nil, err
		}
		if !after.IsZero() && !created.After(after) || !before.IsZero() && !created.Before(before) || visible != nil && !visible(r) {
			continue
		}
		size += len(data)
		if size > 32<<20 {
			return nil, fault.New(fault.TooLarge, "QUERY_SNAPSHOT_LIMIT", "任务查询载荷超过32MiB，请收紧过滤条件")
		}
		out = append(out, r)
		if len(out) >= limit {
			break
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := time.Parse(time.RFC3339Nano, out[i].CreatedAt)
		b, _ := time.Parse(time.RFC3339Nano, out[j].CreatedAt)
		if a.Equal(b) {
			return out[i].ID > out[j].ID
		}
		return a.After(b)
	})
	return out, nil
}

func (s *Store) RetryTransferItems(ctx context.Context, id string, cancelled bool) ([]transfer.Item, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT item_index,payload FROM job_items WHERE job_id=? AND (state='failed' OR (? AND state='cancelled')) ORDER BY item_index LIMIT 10001", id, cancelled)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []transfer.Item{}
	for rows.Next() {
		var index uint64
		var data []byte
		var item transfer.Item
		if err = rows.Scan(&index, &data); err != nil {
			return nil, err
		}
		if err = s.openValue(fmt.Sprintf("item/%s/%d", id, index), data, &item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if len(out) > 10000 {
		return nil, fault.New(fault.TooLarge, "RETRY_ITEM_LIMIT", "单次重试最多10000个项目")
	}
	return out, rows.Err()
}
func (s *Store) CleanupEvents(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM job_events WHERE created_at<?", time.Now().Add(-EventTTL).UTC().Format(time.RFC3339Nano))
	return err
}

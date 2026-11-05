package persistence

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/account"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/operation"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/transfer"
)

func (s *Store) sealValue(binding string, value any) ([]byte, error) {
	plain, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, s.seal.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	return s.seal.Seal(nonce, nonce, plain, []byte(binding)), nil
}
func (s *Store) openValue(binding string, data []byte, value any) error {
	n := s.seal.NonceSize()
	if len(data) < n {
		return errors.New("invalid encrypted service record")
	}
	plain, err := s.seal.Open(nil, data[:n], data[n:], []byte(binding))
	if err != nil {
		return errors.New("cannot decrypt service record")
	}
	return json.Unmarshal(plain, value)
}

// The explicit envelope is the only serialization of write-only credentials.
type storedAccount struct {
	Entry         account.Entry
	Revision, UID uint64
	Cookies       map[string]string
	AccessToken   string
}

func (s *Store) CreateAccount(ctx context.Context, a account.Entry) error {
	c := a.Credentials
	data, err := s.sealValue("account/"+a.ID, storedAccount{a, c.Revision, c.UID, c.Cookies, c.AccessToken})
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "INSERT INTO accounts(account_id,record) VALUES(?,?)", a.ID, data)
	return err
}
func (s *Store) Account(ctx context.Context, id string) (account.Entry, error) {
	return s.account(ctx, s.db, id)
}

func (s *Store) CreateTransfer(ctx context.Context, r transfer.Record) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if r.Request.Public {
		a, err := s.account(ctx, tx, r.Request.AccountID)
		if err != nil {
			return false, err
		}
		if a.Credentials.Revision != r.CredentialRevision {
			var revoked bool
			err = tx.QueryRowContext(ctx, "SELECT revoked FROM account_credentials WHERE account_id=? AND revision=?", r.Request.AccountID, r.CredentialRevision).Scan(&revoked)
			if errors.Is(err, sql.ErrNoRows) || err == nil && revoked {
				return false, fault.New(fault.Unsupported, "CREDENTIAL_REVOKED", "任务凭据版本已撤销")
			}
			if err != nil {
				return false, err
			}
		}
	}
	r.State = "queued"
	r.Version = 1
	data, err := s.sealValue("job/"+r.ID, r)
	if err != nil {
		return false, err
	}
	res, err := tx.ExecContext(ctx, "INSERT INTO jobs(job_id,owner,account_id,kind,state,record,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(job_id) DO NOTHING", r.ID, r.Owner, r.Request.AccountID, r.Request.Kind, "queued", data, r.CreatedAt, r.CreatedAt)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil || n != 1 {
		return false, err
	}
	if len(r.Plan) > 0 {
		var plan transfer.Plan
		if err = json.Unmarshal(r.Plan, &plan); err != nil {
			return false, err
		}
		for _, unit := range plan.Units {
			if unit.StagedFileID == "" {
				continue
			}
			f, e := s.stage(ctx, tx, unit.StagedFileID)
			if e != nil {
				return false, e
			}
			expiry, _ := time.Parse(time.RFC3339Nano, f.ExpiresAt)
			if f.Owner != r.Owner || f.State != "ready" && f.State != "in_use" || f.References == 0 && !time.Now().Before(expiry) {
				return false, fault.New(fault.Gone, "RESOURCE_EXPIRED", "暂存源已失效")
			}
			if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO staging_references VALUES(?,?)", f.ID, r.ID); err != nil {
				return false, err
			}
		}
	}
	if err = s.appendEvent(ctx, tx, r.ID, "job.created", nil); err != nil {
		return false, err
	}
	if err = s.saveReplay(ctx, tx, r.Admission, r.ID, r); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func (s *Store) Transfer(ctx context.Context, id string) (transfer.Record, error) {
	return s.transfer(ctx, s.db, id)
}
func (s *Store) transfer(ctx context.Context, q queryer, id string) (transfer.Record, error) {
	var r transfer.Record
	var data []byte
	var state string
	var started, finished sql.NullString
	var updated string
	var version uint64
	err := q.QueryRowContext(ctx, "SELECT state,record,updated_at,started_at,finished_at,version FROM jobs WHERE job_id=?", id).Scan(&state, &data, &updated, &started, &finished, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return r, fault.New(fault.NotFound, "RESOURCE_NOT_FOUND", "任务不存在")
	}
	if err != nil {
		return r, err
	}
	return s.decodeTransfer(id, state, data, updated, started, finished, version)
}
func (s *Store) decodeTransfer(id, state string, data []byte, updated string, started, finished sql.NullString, version uint64) (transfer.Record, error) {
	var r transfer.Record
	if err := s.openValue("job/"+id, data, &r); err != nil {
		return r, err
	}
	if bytes.Equal(r.Checkpoint, []byte("null")) {
		r.Checkpoint = nil
	}
	if bytes.Equal(r.EffectiveOptions, []byte("null")) {
		r.EffectiveOptions = nil
	}
	r.State = state
	r.UpdatedAt, r.StartedAt, r.FinishedAt, r.Version = updated, started.String, finished.String, version
	return r, nil
}
func (s *Store) TransitionTransfer(ctx context.Context, id string, from []string, to string) (bool, error) {
	if len(from) == 0 {
		return false, errors.New("missing expected task state")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	args := []any{to, now}
	q := "UPDATE jobs SET state=?,updated_at=?,version=version+1"
	if to == "running" {
		q += ",started_at=COALESCE(started_at,?)"
		args = append(args, now)
	}
	if transfer.Terminal(to) {
		q += ",finished_at=?"
		args = append(args, now)
	}
	q += " WHERE job_id=? AND state IN (" + strings.TrimRight(strings.Repeat("?,", len(from)), ",") + ")"
	args = append(args, id)
	for _, state := range from {
		args = append(args, state)
	}
	res, err := tx.ExecContext(ctx, q, args...)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil || n != 1 {
		return false, err
	}
	if err = s.appendEvent(ctx, tx, id, "job.state_changed", nil); err != nil {
		return false, err
	}
	if transfer.Terminal(to) {
		if err = s.releaseStaging(ctx, tx, id); err != nil {
			return false, err
		}
		if err = s.appendEvent(ctx, tx, id, "job.finished", nil); err != nil {
			return false, err
		}
	}
	return true, tx.Commit()
}

func (s *Store) SaveTransferCheckpoint(ctx context.Context, id string, checkpoint json.RawMessage) error {
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
		return fault.New(fault.Conflict, "STATE_CONFLICT", "任务已停止")
	}
	r.Checkpoint = append(json.RawMessage(nil), checkpoint...)
	if err = s.saveRecord(ctx, tx, r); err != nil {
		return err
	}
	if err = s.appendEvent(ctx, tx, id, "job.progress", nil); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) InterruptTransfers(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, "SELECT job_id,record FROM jobs WHERE kind IN ('upload','download','file_scan','mkdir','file_delete','file_copy','file_move','file_rename','share_cancel','share_transfer','offline_add','offline_cancel','offline_delete','offline_clear','recycle_restore','recycle_delete','recycle_clear') AND state='cancelling'")
	if err != nil {
		return err
	}
	type cancellation struct {
		id    string
		state string
	}
	var pending []cancellation
	for rows.Next() {
		var id string
		var data []byte
		var r transfer.Record
		if err = rows.Scan(&id, &data); err != nil {
			rows.Close()
			return err
		}
		if err = s.openValue("job/"+id, data, &r); err != nil {
			rows.Close()
			return err
		}
		var cp struct{ Phase string }
		if len(r.Checkpoint) > 0 {
			if err = json.Unmarshal(r.Checkpoint, &cp); err != nil {
				rows.Close()
				return err
			}
		}
		state := "cancelled"
		// A final remote/local commit may already have happened. Preserve the
		// uncertainty and require reconciliation instead of claiming rollback.
		if cp.Phase == "committing" || cp.Phase == "done" {
			state = "interrupted"
		}
		pending = append(pending, cancellation{id, state})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, p := range pending {
		if _, err = s.TransitionTransfer(ctx, p.id, []string{"cancelling"}, p.state); err != nil {
			return err
		}
	}
	rows, err = s.db.QueryContext(ctx, "SELECT job_id FROM jobs WHERE kind IN ('upload','download','file_scan','mkdir','file_delete','file_copy','file_move','file_rename','share_cancel','share_transfer','offline_add','offline_cancel','offline_delete','offline_clear','recycle_restore','recycle_delete','recycle_clear') AND state IN ('running','pausing')")
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err = s.TransitionTransfer(ctx, id, []string{"running", "pausing"}, "interrupted"); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) FinishTransfer(ctx context.Context, id, state, code string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r, err := s.transfer(ctx, tx, id)
	if err != nil {
		return err
	}
	if r.State != "running" && r.State != "cancelling" && r.State != "pausing" && !(code == "CREDENTIAL_REVOKED" && (r.State == "queued" || r.State == "interrupted")) {
		return fault.New(fault.Conflict, "STATE_CONFLICT", "任务已停止")
	}
	r.FailureCode = code
	if r.ScanResult != nil && state == "succeeded" && (r.ScanResult.Counts.Failed > 0 || r.ScanResult.Counts.Cancelled > 0) {
		state = "failed"
		if r.ScanResult.Counts.Succeeded > 0 || r.ScanResult.Counts.Skipped > 0 {
			state = "partially_succeeded"
		}
	}
	data, err := s.sealValue("job/"+id, r)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var finished any
	if transfer.Terminal(state) {
		finished = now
	}
	res, err := tx.ExecContext(ctx, "UPDATE jobs SET state=?,record=?,updated_at=?,finished_at=?,version=version+1 WHERE job_id=? AND version=?", state, data, now, finished, id, r.Version)
	if err != nil {
		return err
	}
	if err = oneRow(res); err != nil {
		return err
	}
	if err = s.appendEvent(ctx, tx, id, "job.state_changed", nil); err != nil {
		return err
	}
	if transfer.Terminal(state) {
		if err = s.releaseStaging(ctx, tx, id); err != nil {
			return err
		}
		if r.Request.Kind != "file_scan" && len(r.Plan) == 0 && r.Request.WritePlan == "" {
			if err = s.appendEvent(ctx, tx, id, "item.completed", nil); err != nil {
				return err
			}
		}
		if err = s.appendEvent(ctx, tx, id, "job.finished", nil); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) QueuedTransfers(ctx context.Context, limit int) ([]transfer.Record, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT job_id FROM jobs WHERE kind IN ('upload','download','file_scan','mkdir','file_delete','file_copy','file_move','file_rename','share_cancel','share_transfer','offline_add','offline_cancel','offline_delete','offline_clear','recycle_restore','recycle_delete','recycle_clear') AND state='queued' ORDER BY created_at,job_id LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	result := make([]transfer.Record, 0, len(ids))
	for _, id := range ids {
		r, err := s.Transfer(ctx, id)
		if err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, nil
}

func (s *Store) ReplayControl(ctx context.Context, m operation.Mutation) (*transfer.Record, error) {
	var r transfer.Record
	found, err := s.replayRequest(ctx, s.db, m, &r)
	if !found || err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Store) ActiveTransferCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM jobs WHERE kind IN ('upload','download','file_scan','mkdir','file_delete','file_copy','file_move','file_rename','share_cancel','share_transfer','offline_add','offline_cancel','offline_delete','offline_clear','recycle_restore','recycle_delete','recycle_clear') AND state IN ('queued','running','cancelling')").Scan(&n)
	return n, err
}

func (s *Store) ControlTransfer(ctx context.Context, id string, from []string, to string, m operation.Mutation) (transfer.Record, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return transfer.Record{}, err
	}
	defer tx.Rollback()
	var replay transfer.Record
	if found, err := s.replayRequest(ctx, tx, m, &replay); found || err != nil {
		return replay, err
	}
	r, err := s.transfer(ctx, tx, id)
	if err != nil {
		return r, err
	}
	if to != "" {
		allowed := false
		for _, state := range from {
			if r.State == state {
				allowed = true
			}
		}
		if !allowed {
			return r, fault.New(fault.Conflict, "STATE_CONFLICT", "任务状态已改变")
		}
		if to == "cancelled" && r.Request.WritePlan != "" {
			if err = s.cancelWriteItems(ctx, tx, &r); err != nil {
				return r, err
			}
			data, err := s.sealValue("job/"+id, r)
			if err != nil {
				return r, err
			}
			if _, err = tx.ExecContext(ctx, "UPDATE jobs SET record=? WHERE job_id=?", data, id); err != nil {
				return r, err
			}
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		var finished any
		if to == "cancelled" {
			finished = now
		}
		result, err := tx.ExecContext(ctx, "UPDATE jobs SET state=?,updated_at=?,finished_at=?,version=version+1 WHERE job_id=? AND version=?", to, now, finished, id, r.Version)
		if err != nil {
			return r, err
		}
		if err = oneRow(result); err != nil {
			return r, err
		}
		if err = s.appendEvent(ctx, tx, id, "job.state_changed", nil); err != nil {
			return r, err
		}
		if transfer.Terminal(to) {
			if err = s.releaseStaging(ctx, tx, id); err != nil {
				return r, err
			}
			if err = s.appendEvent(ctx, tx, id, "job.finished", nil); err != nil {
				return r, err
			}
		}
		r, err = s.transfer(ctx, tx, id)
		if err != nil {
			return r, err
		}
	}
	if err = s.saveReplay(ctx, tx, m, id, r); err != nil {
		return r, err
	}
	return r, tx.Commit()
}

func (s *Store) ReconcileTransfer(ctx context.Context, id string, writeJob bool, m operation.Mutation) (transfer.Record, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return transfer.Record{}, err
	}
	defer tx.Rollback()
	var replay transfer.Record
	if found, e := s.replayRequest(ctx, tx, m, &replay); found || e != nil {
		return replay, e
	}
	r, err := s.transfer(ctx, tx, id)
	if err != nil {
		return r, err
	}
	if r.State != "interrupted" {
		return r, fault.New(fault.Conflict, "STATE_CONFLICT", "任务状态已变化")
	}
	r.Request.Reconcile = writeJob
	if err = s.saveRecord(ctx, tx, r); err != nil {
		return r, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE jobs SET state='queued',finished_at=NULL WHERE job_id=?", id); err != nil {
		return r, err
	}
	if err = s.appendEvent(ctx, tx, id, "job.state_changed", nil); err != nil {
		return r, err
	}
	r, err = s.transfer(ctx, tx, id)
	if err != nil {
		return r, err
	}
	if err = s.saveReplay(ctx, tx, m, id, r); err != nil {
		return r, err
	}
	return r, tx.Commit()
}

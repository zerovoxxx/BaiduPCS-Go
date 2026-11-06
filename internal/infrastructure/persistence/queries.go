package persistence

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/transfer"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/ports"
	"time"
)

const QueryLimit = 10000
const QueryTTL = 600 * time.Second

type queryCursor struct {
	ID     string
	Offset int
}

func (s *Store) queryPage(id string, items []json.RawMessage, offset, size int, expiry int64) (ports.QueryPage, error) {
	p := ports.QueryPage{ExpiresAt: time.Unix(expiry, 0).UTC().Format(time.RFC3339), Items: []json.RawMessage{}}
	if size < 1 || size > 1000 || offset < 0 || offset > len(items) {
		return p, fault.New(fault.Invalid, "INVALID_REQUEST", "查询游标或页大小无效")
	}
	end := min(offset+size, len(items))
	p.Items = items[offset:end]
	p.HasMore = end < len(items)
	if p.HasMore {
		encrypted, err := s.sealValue("query-cursor", queryCursor{id, end})
		if err != nil {
			return p, err
		}
		p.NextCursor = base64.RawURLEncoding.EncodeToString(encrypted)
	}
	return p, nil
}
func (s *Store) CreateQuery(ctx context.Context, binding string, items []json.RawMessage, size int) (ports.QueryPage, error) {
	if len(items) > QueryLimit {
		return ports.QueryPage{}, fault.New(fault.TooLarge, "QUERY_SNAPSHOT_LIMIT", "查询快照超过10000项，请使用扫描任务")
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return ports.QueryPage{}, err
	}
	name := hex.EncodeToString(id[:])
	expiry := time.Now().Add(QueryTTL).Unix()
	payload, err := s.sealValue("query/"+name, items)
	if err != nil {
		return ports.QueryPage{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ports.QueryPage{}, err
	}
	defer tx.Rollback()
	// Expired payloads are reclaimable, but callers still get 410 for valid old cursors.
	if _, err = tx.ExecContext(ctx, "DELETE FROM query_snapshots WHERE expires_at<=?", time.Now().Unix()); err != nil {
		return ports.QueryPage{}, err
	}
	var count, bytes int64
	if err = tx.QueryRowContext(ctx, "SELECT count(*),COALESCE(sum(length(payload)),0) FROM query_snapshots").Scan(&count, &bytes); err != nil {
		return ports.QueryPage{}, err
	}
	if count >= 128 || bytes+int64(len(payload)) > 128<<20 {
		return ports.QueryPage{}, fault.New(fault.RateLimited, "QUERY_SNAPSHOT_QUOTA", "查询快照配额已满")
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO query_snapshots VALUES(?,?,?,?)", name, binding, payload, expiry); err != nil {
		return ports.QueryPage{}, err
	}
	page, err := s.queryPage(name, items, 0, size, expiry)
	if err != nil {
		return page, err
	}
	return page, tx.Commit()
}
func (s *Store) ReadQuery(ctx context.Context, binding, cursor string, size int) (ports.QueryPage, error) {
	var c queryCursor
	data, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || len(cursor) > 2048 || s.openValue("query-cursor", data, &c) != nil {
		return ports.QueryPage{}, fault.New(fault.Invalid, "INVALID_QUERY_CURSOR", "查询游标格式无效")
	}
	var storedBinding string
	var payload []byte
	var expiry int64
	err = s.db.QueryRowContext(ctx, "SELECT binding,payload,expires_at FROM query_snapshots WHERE snapshot_id=?", c.ID).Scan(&storedBinding, &payload, &expiry)
	if errors.Is(err, sql.ErrNoRows) || err == nil && expiry <= time.Now().Unix() {
		return ports.QueryPage{}, fault.New(fault.Gone, "QUERY_SNAPSHOT_EXPIRED", "查询快照已过期")
	}
	if err != nil {
		return ports.QueryPage{}, err
	}
	if binding != storedBinding {
		return ports.QueryPage{}, fault.New(fault.Conflict, "QUERY_CURSOR_MISMATCH", "游标与账号、调用方或查询条件不一致")
	}
	var items []json.RawMessage
	if err = s.openValue("query/"+c.ID, payload, &items); err != nil {
		return ports.QueryPage{}, err
	}
	return s.queryPage(c.ID, items, c.Offset, size, expiry)
}

type itemCursor struct {
	Job, Owner  string
	After, High int64
	Size        int
	Expires     int64
}

// Scan items are immutable append-only facts. Freeze a high-water mark rather
// than copying a million-item job into an in-memory query snapshot.
func (s *Store) TransferItemPage(ctx context.Context, id, owner, cursor string, size int) (ports.QueryPage, error) {
	out := ports.QueryPage{Items: []json.RawMessage{}}
	c := itemCursor{Job: id, Owner: owner, After: -1, High: -1, Size: size, Expires: time.Now().Add(QueryTTL).Unix()}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if cursor != "" {
		encoded, e := base64.RawURLEncoding.DecodeString(cursor)
		if e != nil || len(cursor) > 2048 || s.openValue("item-cursor", encoded, &c) != nil {
			return out, fault.New(fault.Invalid, "INVALID_QUERY_CURSOR", "任务项目游标格式无效")
		}
		if c.Owner != owner || c.Job != id || c.Size != size {
			return out, fault.New(fault.Conflict, "QUERY_CURSOR_MISMATCH", "任务项目游标范围不一致")
		}
		if c.Expires <= time.Now().Unix() {
			return out, fault.New(fault.Gone, "QUERY_SNAPSHOT_EXPIRED", "任务项目游标已过期")
		}
	} else {
		if err = tx.QueryRowContext(ctx, "SELECT COALESCE(max(item_index),-1) FROM job_items WHERE job_id=?", id).Scan(&c.High); err != nil {
			return out, err
		}
	}
	rows, err := tx.QueryContext(ctx, "SELECT item_index,payload FROM job_items WHERE job_id=? AND item_index>? AND item_index<=? ORDER BY item_index LIMIT ?", id, c.After, c.High, size+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var index int64
		var payload []byte
		if err = rows.Scan(&index, &payload); err != nil {
			return out, err
		}
		if len(out.Items) == size {
			out.HasMore = true
			break
		}
		var item transfer.Item
		if err = s.openValue(fmt.Sprintf("item/%s/%d", id, index), payload, &item); err != nil {
			return out, err
		}
		b, err := json.Marshal(item)
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, b)
		c.After = index
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	out.ExpiresAt = time.Unix(c.Expires, 0).UTC().Format(time.RFC3339)
	if out.HasMore {
		data, err := s.sealValue("item-cursor", c)
		if err != nil {
			return out, err
		}
		out.NextCursor = base64.RawURLEncoding.EncodeToString(data)
	}
	return out, nil
}

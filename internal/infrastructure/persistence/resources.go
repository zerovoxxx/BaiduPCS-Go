package persistence

import (
	"context"
	"database/sql"
	"errors"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/operation"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/session"
	"time"
)

func (s *Store) Resource(ctx context.Context, kind, id string) (session.Resource, error) {
	return s.resource(ctx, s.db, kind, id)
}
func (s *Store) resource(ctx context.Context, q queryer, kind, id string) (session.Resource, error) {
	var r session.Resource
	var b []byte
	e := q.QueryRowContext(ctx, "SELECT payload FROM resources WHERE kind=? AND id=?", kind, id).Scan(&b)
	if errors.Is(e, sql.ErrNoRows) {
		return r, fault.New(fault.NotFound, "RESOURCE_NOT_FOUND", "资源不存在")
	}
	if e != nil {
		return r, e
	}
	e = s.openValue("resource/"+kind+"/"+id, b, &r)
	return r, e
}
func (s *Store) ReplayResource(ctx context.Context, m operation.Mutation) (*session.Resource, error) {
	var r session.Resource
	ok, e := s.replayRequest(ctx, s.db, m, &r)
	if !ok || e != nil {
		return nil, e
	}
	return &r, nil
}
func (s *Store) PutResource(ctx context.Context, r session.Resource, expected uint64, m operation.Mutation) (session.Resource, error) {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return r, e
	}
	defer tx.Rollback()
	var replay session.Resource
	if ok, e := s.replayRequest(ctx, tx, m, &replay); ok || e != nil {
		return replay, e
	}
	if expected == 0 {
		var n int
		e = tx.QueryRowContext(ctx, "SELECT count(*) FROM resources WHERE kind=? AND expires>?", r.Kind, time.Now().Unix()).Scan(&n)
		if e != nil {
			return r, e
		}
		if n >= 128 {
			return r, fault.New(fault.RateLimited, "RESOURCE_LIMIT", "活动资源数量达到上限")
		}
	}
	r.Revision = expected + 1
	b, e := s.sealValue("resource/"+r.Kind+"/"+r.ID, r)
	if e != nil {
		return r, e
	}
	var res sql.Result
	if expected == 0 {
		res, e = tx.ExecContext(ctx, "INSERT INTO resources(kind,id,owner,version,expires,payload)VALUES(?,?,?,?,?,?) ON CONFLICT(kind,id) DO NOTHING", r.Kind, r.ID, r.Owner, r.Revision, r.Expires, b)
	} else {
		res, e = tx.ExecContext(ctx, "UPDATE resources SET owner=?,version=?,expires=?,payload=? WHERE kind=? AND id=? AND version=?", r.Owner, r.Revision, r.Expires, b, r.Kind, r.ID, expected)
	}
	if e != nil {
		return r, e
	}
	n, e := res.RowsAffected()
	if e != nil {
		return r, e
	}
	if n != 1 {
		return r, fault.New(fault.Precondition, "REVISION_MISMATCH", "资源版本已改变")
	}
	if e = s.saveReplay(ctx, tx, m, r.ID, r); e != nil {
		return r, e
	}
	return r, tx.Commit()
}
func (s *Store) Resources(ctx context.Context, kind string, limit int) ([]session.Resource, error) {
	rows, e := s.db.QueryContext(ctx, "SELECT id,payload FROM resources WHERE kind=? ORDER BY id LIMIT ?", kind, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []session.Resource{}
	for rows.Next() {
		var id string
		var b []byte
		if e = rows.Scan(&id, &b); e != nil {
			return nil, e
		}
		var r session.Resource
		if e = s.openValue("resource/"+kind+"/"+id, b, &r); e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Retain gone-resource tombstones for a day, then prune in bounded batches.
// Staging bytes and job references have their own independent retention rules.
func (s *Store) CleanupResources(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM resources WHERE rowid IN (
 SELECT rowid FROM resources WHERE kind IN ('context','reception') AND expires < ?
 ORDER BY expires LIMIT 512)`, time.Now().Add(-24*time.Hour).Unix())
	return err
}

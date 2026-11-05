package persistence

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/operation"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/staging"
)

func (s *Store) ReserveStage(ctx context.Context, f staging.File) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var count, size int64
	if err = tx.QueryRowContext(ctx, "SELECT count(*),COALESCE(sum(size),0) FROM staging_files WHERE state!='deleted'").Scan(&count, &size); err != nil {
		return err
	}
	if count >= 128 || size+int64(256<<20) > 1<<30 {
		return fault.New(fault.RateLimited, "STAGING_QUOTA", "暂存配额已满")
	}
	data, err := s.sealValue("stage/"+f.ID, f)
	if err != nil {
		return err
	}
	expiry, _ := time.Parse(time.RFC3339Nano, f.ExpiresAt)
	_, err = tx.ExecContext(ctx, "INSERT INTO staging_files VALUES(?,?,?,?,?,?)", f.ID, f.Owner, data, f.State, int64(256<<20), expiry.Unix())
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) Stage(ctx context.Context, id string) (staging.File, error) {
	return s.stage(ctx, s.db, id)
}
func (s *Store) stage(ctx context.Context, q queryer, id string) (staging.File, error) {
	var f staging.File
	var payload []byte
	var expiry int64
	err := q.QueryRowContext(ctx, "SELECT record,state,size,expires_at FROM staging_files WHERE file_id=?", id).Scan(&payload, &f.State, &f.Size, &expiry)
	if errors.Is(err, sql.ErrNoRows) {
		return f, fault.New(fault.NotFound, "RESOURCE_NOT_FOUND", "暂存文件不存在")
	}
	if err != nil {
		return f, err
	}
	state, size := f.State, f.Size
	if err = s.openValue("stage/"+id, payload, &f); err != nil {
		return f, err
	}
	f.State, f.Size = state, size
	f.ExpiresAt = time.Unix(expiry, 0).UTC().Format(time.RFC3339Nano)
	err = q.QueryRowContext(ctx, "SELECT count(*) FROM staging_references r JOIN jobs j ON r.job_id=j.job_id WHERE r.file_id=? AND j.state NOT IN ('succeeded','partially_succeeded','failed','cancelled')", id).Scan(&f.References)
	if f.References > 0 {
		f.State = "in_use"
	}
	return f, err
}
func (s *Store) FinishStage(ctx context.Context, f staging.File, m operation.Mutation) (staging.File, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return f, err
	}
	defer tx.Rollback()
	var replay staging.File
	if found, e := s.replayRequest(ctx, tx, m, &replay); found || e != nil {
		return replay, e
	}
	var size int64
	if err = tx.QueryRowContext(ctx, "SELECT COALESCE(sum(size),0) FROM staging_files WHERE state!='deleted'").Scan(&size); err != nil {
		return f, err
	}
	if f.Size > 256<<20 || size-int64(256<<20)+f.Size > 1<<30 {
		return f, fault.New(fault.TooLarge, "STAGING_QUOTA", "暂存内容超过配额")
	}
	data, err := s.sealValue("stage/"+f.ID, f)
	if err != nil {
		return f, err
	}
	res, err := tx.ExecContext(ctx, "UPDATE staging_files SET record=?,state='ready',size=? WHERE file_id=? AND state='writing'", data, f.Size, f.ID)
	if err != nil {
		return f, err
	}
	if err = oneRow(res); err != nil {
		return f, err
	}
	if err = s.saveReplay(ctx, tx, m, f.ID, f); err != nil {
		return f, err
	}
	return f, tx.Commit()
}
func (s *Store) ReplayStage(ctx context.Context, m operation.Mutation) (*staging.File, error) {
	var f staging.File
	found, err := s.replayRequest(ctx, s.db, m, &f)
	if !found || err != nil {
		return nil, err
	}
	return &f, nil
}
func (s *Store) DeleteStage(ctx context.Context, id string, m operation.Mutation) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var replay bool
	if found, e := s.replayRequest(ctx, tx, m, &replay); found || e != nil {
		return e
	}
	f, err := s.stage(ctx, tx, id)
	if err != nil {
		return err
	}
	if f.References > 0 {
		return fault.New(fault.Conflict, "STAGED_FILE_IN_USE", "暂存文件正在被任务引用")
	}
	if _, err = tx.ExecContext(ctx, "UPDATE staging_files SET state='deleted' WHERE file_id=?", id); err != nil {
		return err
	}
	if err = s.saveReplay(ctx, tx, m, id, true); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) StageCandidates(ctx context.Context) ([]staging.File, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT file_id FROM staging_files WHERE state='deleted' OR expires_at<=?", time.Now().Unix())
	if err != nil {
		return nil, err
	}
	ids := []string{}
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
	out := []staging.File{}
	for _, id := range ids {
		f, err := s.Stage(ctx, id)
		if err != nil {
			return nil, err
		}
		if f.References == 0 {
			out = append(out, f)
		}
	}
	return out, nil
}

// Extend idle TTL only as the referencing job becomes terminal, atomically with
// that state. Active, paused and interrupted references continue to pin content.
func (s *Store) releaseStaging(ctx context.Context, tx *sql.Tx, job string) error {
	_, err := tx.ExecContext(ctx, "UPDATE staging_files SET expires_at=max(expires_at,?) WHERE file_id IN (SELECT file_id FROM staging_references WHERE job_id=?)", time.Now().Add(24*time.Hour).Unix(), job)
	return err
}

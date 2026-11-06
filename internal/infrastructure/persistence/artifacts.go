package persistence

import (
	"context"
	"database/sql"
	"errors"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/artifact"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/operation"
	"time"
)

func (s *Store) ReserveArtifact(ctx context.Context, a artifact.Metadata) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var count, size int64
	if err = tx.QueryRowContext(ctx, "SELECT count(*),COALESCE(sum(CASE WHEN state='writing' THEN 268435456 ELSE size END),0) FROM artifacts WHERE state!='deleted'").Scan(&count, &size); err != nil {
		return err
	}
	if count >= 128 || size+256<<20 > 1<<30 {
		return fault.New(fault.TooLarge, "ARTIFACT_QUOTA_EXCEEDED", "结果文件配额已满")
	}
	r, err := s.transfer(ctx, tx, a.JobID)
	if err != nil {
		return err
	}
	if r.State != "running" {
		return fault.New(fault.Conflict, "STATE_CONFLICT", "任务未执行")
	}
	data, err := s.sealValue("artifact/"+a.ID, a)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO artifacts VALUES(?,?,?,?,?,?)", a.ID, a.JobID, data, time.Now().Add(24*time.Hour).Unix(), "writing", 0); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) FinishArtifact(ctx context.Context, a artifact.Metadata) error {
	data, err := s.sealValue("artifact/"+a.ID, a)
	if err != nil {
		return err
	}
	expiry, err := time.Parse(time.RFC3339Nano, a.ExpiresAt)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, "UPDATE artifacts SET record=?,expires_at=?,state='ready',size=? WHERE artifact_id=? AND state='writing'", data, expiry.Unix(), a.Size, a.ID)
	if err != nil {
		return err
	}
	return oneRow(result)
}
func (s *Store) Artifact(ctx context.Context, id string) (artifact.Metadata, error) {
	var a artifact.Metadata
	var data []byte
	err := s.db.QueryRowContext(ctx, "SELECT record,state FROM artifacts WHERE artifact_id=?", id).Scan(&data, &a.State)
	if errors.Is(err, sql.ErrNoRows) {
		return a, fault.New(fault.NotFound, "RESOURCE_NOT_FOUND", "结果文件不存在")
	}
	if err != nil {
		return a, err
	}
	state := a.State
	if err = s.openValue("artifact/"+id, data, &a); err != nil {
		return a, err
	}
	a.State = state
	return a, nil
}
func (s *Store) JobArtifacts(ctx context.Context, job string) ([]artifact.Metadata, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT artifact_id FROM artifacts WHERE job_id=? AND state='ready' ORDER BY artifact_id", job)
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
	out := []artifact.Metadata{}
	for _, id := range ids {
		a, err := s.Artifact(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}
func (s *Store) ArtifactReplayed(ctx context.Context, m operation.Mutation) (bool, error) {
	var v bool
	return s.replayRequest(ctx, s.db, m, &v)
}
func (s *Store) RemoveArtifact(ctx context.Context, id string, m operation.Mutation) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var replay bool
	if found, e := s.replayRequest(ctx, tx, m, &replay); found || e != nil {
		return e
	}
	var state string
	var job string
	if err = tx.QueryRowContext(ctx, "SELECT state,job_id FROM artifacts WHERE artifact_id=?", id).Scan(&state, &job); err != nil {
		return err
	}
	var jobState string
	if err = tx.QueryRowContext(ctx, "SELECT state FROM jobs WHERE job_id=?", job).Scan(&jobState); err != nil {
		return err
	}
	if jobState != "succeeded" && jobState != "partially_succeeded" && jobState != "failed" && jobState != "cancelled" {
		return fault.New(fault.Conflict, "ARTIFACT_IN_USE", "活动任务仍引用此结果文件")
	}
	if _, err = tx.ExecContext(ctx, "UPDATE artifacts SET state='deleted',size=0 WHERE artifact_id=?", id); err != nil {
		return err
	}
	if err = s.saveReplay(ctx, tx, m, id, true); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) ArtifactCandidates(ctx context.Context) ([]artifact.Metadata, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT a.artifact_id FROM artifacts a JOIN jobs j ON a.job_id=j.job_id WHERE a.state='deleted' OR (a.expires_at<=? AND j.state IN ('succeeded','partially_succeeded','failed','cancelled'))", time.Now().Unix())
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
	out := []artifact.Metadata{}
	for _, id := range ids {
		a, err := s.Artifact(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

func (s *Store) AbortArtifact(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, "UPDATE artifacts SET state='deleted',size=0 WHERE artifact_id=? AND state='writing'", id)
	if err != nil {
		return err
	}
	return oneRow(result)
}

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
	var data []byte
	err := s.db.QueryRowContext(ctx, "SELECT record FROM accounts WHERE account_id=?", id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return account.Entry{}, fault.New(fault.NotFound, "RESOURCE_NOT_FOUND", "账号不存在")
	}
	if err != nil {
		return account.Entry{}, err
	}
	var a storedAccount
	if err = s.openValue("account/"+id, data, &a); err != nil {
		return account.Entry{}, err
	}
	a.Entry.Credentials = account.Credentials{AccountID: id, Revision: a.Revision, UID: a.UID, Cookies: a.Cookies, AccessToken: a.AccessToken}
	if a.Entry.Revision == 0 {
		a.Entry.Revision = 1
	}
	if a.Entry.UpdatedAt == "" {
		a.Entry.UpdatedAt = a.Entry.CreatedAt
	}
	if a.Entry.LoginState == "" {
		a.Entry.LoginState = "unverified"
	}
	return a.Entry, nil
}
func (s *Store) CreateTransfer(ctx context.Context, r transfer.Record) (bool, error) {
	data, err := s.sealValue("job/"+r.ID, r)
	if err != nil {
		return false, err
	}
	result, err := s.db.ExecContext(ctx, "INSERT INTO jobs(job_id,owner,account_id,kind,state,record,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(job_id) DO NOTHING", r.ID, r.Owner, r.Request.AccountID, r.Request.Kind, "queued", data, r.CreatedAt, r.CreatedAt)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
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
	if err = s.openValue("job/"+id, data, &r); err != nil {
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
	now := time.Now().UTC().Format(time.RFC3339Nano)
	args := []any{to, now}
	q := "UPDATE jobs SET state=?,updated_at=?,version=version+1"
	if to == "running" {
		q += ",started_at=COALESCE(started_at,?)"
		args = append(args, now)
	}
	if to == "cancelled" {
		q += ",finished_at=?"
		args = append(args, now)
	}
	q += " WHERE job_id=? AND state IN (" + strings.TrimRight(strings.Repeat("?,", len(from)), ",") + ")"
	args = append(args, id)
	for _, state := range from {
		args = append(args, state)
	}
	result, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}
func (s *Store) SaveTransferCheckpoint(ctx context.Context, id string, checkpoint json.RawMessage) error {
	// Only an executing engine writes its record. State changes use a separate
	// column, so saving a checkpoint cannot revive a cancelled or completed job.
	r, err := s.Transfer(ctx, id)
	if err != nil {
		return err
	}
	r.Checkpoint = append(json.RawMessage(nil), checkpoint...)
	data, err := s.sealValue("job/"+id, r)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, "UPDATE jobs SET record=?,updated_at=?,version=version+1 WHERE job_id=? AND state IN ('running','cancelling')", data, time.Now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return err
	}
	return oneRow(result)
}
func (s *Store) InterruptTransfers(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, "SELECT job_id,record FROM jobs WHERE kind IN ('upload','download') AND state='cancelling'")
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
	_, err = s.db.ExecContext(ctx, "UPDATE jobs SET state='interrupted',updated_at=?,version=version+1 WHERE kind IN ('upload','download') AND state='running'", time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Store) FinishTransfer(ctx context.Context, id, state, code string) error {
	r, err := s.Transfer(ctx, id)
	if err != nil {
		return err
	}
	r.FailureCode = code
	data, err := s.sealValue("job/"+id, r)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var finished any
	if state == "succeeded" || state == "failed" || state == "cancelled" {
		finished = now
	}
	result, err := s.db.ExecContext(ctx, "UPDATE jobs SET state=?,record=?,updated_at=?,finished_at=?,version=version+1 WHERE job_id=? AND state IN ('running','cancelling')", state, data, now, finished, id)
	if err != nil {
		return err
	}
	return oneRow(result)
}

func (s *Store) QueuedTransfers(ctx context.Context, limit int) ([]transfer.Record, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT job_id FROM jobs WHERE kind IN ('upload','download') AND state='queued' ORDER BY created_at,job_id LIMIT ?", limit)
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
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM jobs WHERE kind IN ('upload','download') AND state IN ('queued','running','cancelling')").Scan(&n)
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

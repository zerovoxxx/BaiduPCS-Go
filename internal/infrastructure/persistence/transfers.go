package persistence

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/account"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
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
	var r transfer.Record
	var data []byte
	var state string
	err := s.db.QueryRowContext(ctx, "SELECT state,record FROM jobs WHERE job_id=?", id).Scan(&state, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return r, fault.New(fault.NotFound, "RESOURCE_NOT_FOUND", "任务不存在")
	}
	if err != nil {
		return r, err
	}
	if err = s.openValue("job/"+id, data, &r); err != nil {
		return r, err
	}
	r.State = state
	return r, nil
}
func (s *Store) TransitionTransfer(ctx context.Context, id string, from []string, to string) (bool, error) {
	if len(from) == 0 {
		return false, errors.New("missing expected task state")
	}
	args := []any{to, id}
	for _, state := range from {
		args = append(args, state)
	}
	q := "UPDATE jobs SET state=? WHERE job_id=? AND state IN (" + strings.TrimRight(strings.Repeat("?,", len(from)), ",") + ")"
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
	_, err = s.db.ExecContext(ctx, "UPDATE jobs SET record=? WHERE job_id=? AND state IN ('running','cancelling')", data, id)
	return err
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
	_, err = s.db.ExecContext(ctx, "UPDATE jobs SET state='interrupted' WHERE kind IN ('upload','download') AND state IN ('queued','running')")
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
	_, err = s.db.ExecContext(ctx, "UPDATE jobs SET state=?,record=? WHERE job_id=? AND state IN ('running','cancelling')", state, data, id)
	return err
}

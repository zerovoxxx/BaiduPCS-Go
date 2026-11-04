package persistence

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/account"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/operation"
)

func (s *Store) replayRequest(ctx context.Context, q queryer, m operation.Mutation, value any) (bool, error) {
	if !m.HasKey {
		return false, nil
	}
	var hash, data []byte
	err := q.QueryRowContext(ctx, `SELECT request_hash,reply FROM request_replays WHERE principal=? AND operation=? AND key_hash=? AND (expires_at>? OR resource_id IN (SELECT job_id FROM jobs WHERE state NOT IN ('succeeded','failed','cancelled')))`, m.Principal, m.Operation, m.KeyHash[:], time.Now().Unix()).Scan(&hash, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if subtle.ConstantTimeCompare(hash, m.RequestHash[:]) != 1 {
		return false, fault.New(fault.Conflict, "IDEMPOTENCY_KEY_REUSED", "幂等Key已用于不同请求")
	}
	return true, s.openValue("replay/"+m.Principal+"/"+m.Operation+"/"+string(m.KeyHash[:]), data, value)
}

func (s *Store) saveReplay(ctx context.Context, tx *sql.Tx, m operation.Mutation, resource string, value any) error {
	if !m.HasKey {
		return nil
	}
	data, err := s.sealValue("replay/"+m.Principal+"/"+m.Operation+"/"+string(m.KeyHash[:]), value)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO request_replays(principal,operation,key_hash,request_hash,resource_id,reply,expires_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(principal,operation,key_hash) DO UPDATE SET request_hash=excluded.request_hash,resource_id=excluded.resource_id,reply=excluded.reply,expires_at=excluded.expires_at`, m.Principal, m.Operation, m.KeyHash[:], m.RequestHash[:], resource, data, time.Now().Add(24*time.Hour).Unix()+1)
	return err
}

func (s *Store) CreateAccountOnce(ctx context.Context, a account.Entry, m operation.Mutation) (account.Entry, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return account.Entry{}, err
	}
	defer tx.Rollback()
	var replay account.Entry
	if found, err := s.replayRequest(ctx, tx, m, &replay); found || err != nil {
		return replay, err
	}
	c := a.Credentials
	data, err := s.sealValue("account/"+a.ID, storedAccount{a, c.Revision, c.UID, c.Cookies, c.AccessToken})
	if err != nil {
		return account.Entry{}, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO accounts(account_id,record) VALUES(?,?)", a.ID, data); err != nil {
		return account.Entry{}, err
	}
	// Replay the public metadata only. The canonical credentials stay in accounts.
	if err = s.saveReplay(ctx, tx, m, a.ID, a); err != nil {
		return account.Entry{}, err
	}
	return a, tx.Commit()
}

func (s *Store) Accounts(ctx context.Context) ([]account.Entry, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT account_id FROM accounts ORDER BY account_id")
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
	result := make([]account.Entry, 0, len(ids))
	for _, id := range ids {
		a, err := s.Account(ctx, id)
		if err != nil {
			return nil, err
		}
		result = append(result, a)
	}
	return result, nil
}

func (s *Store) UpdateAccount(ctx context.Context, a account.Entry, expected uint64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var data []byte
	if err = tx.QueryRowContext(ctx, "SELECT record FROM accounts WHERE account_id=?", a.ID).Scan(&data); err != nil {
		return err
	}
	var old storedAccount
	if err = s.openValue("account/"+a.ID, data, &old); err != nil {
		return err
	}
	if old.Entry.Revision == 0 {
		old.Entry.Revision = 1
	}
	if old.Entry.Revision != expected {
		return fault.New(fault.Conflict, "REVISION_MISMATCH", "账号版本已改变")
	}
	c := a.Credentials
	data, err = s.sealValue("account/"+a.ID, storedAccount{a, c.Revision, c.UID, c.Cookies, c.AccessToken})
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, "UPDATE accounts SET record=? WHERE account_id=?", data, a.ID)
	if err != nil {
		return err
	}
	if err = oneRow(result); err != nil {
		return err
	}
	return tx.Commit()
}

func oneRow(result sql.Result) error {
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fault.New(fault.Conflict, "STATE_CONFLICT", "持久化状态已改变")
	}
	return nil
}

// CleanupReplays never expires the replay of an active job. Jobs and their
// checkpoints are retained; P2 does not silently discard recovery evidence.
func (s *Store) CleanupReplays(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM request_replays WHERE expires_at<=? AND resource_id NOT IN (SELECT job_id FROM jobs WHERE state NOT IN ('succeeded','failed','cancelled'))`, time.Now().Unix())
	return err
}

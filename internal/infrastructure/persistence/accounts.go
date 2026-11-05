package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/account"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/operation"
)

func (s *Store) account(ctx context.Context, q queryer, id string) (account.Entry, error) {
	var data []byte
	err := q.QueryRowContext(ctx, "SELECT record FROM accounts WHERE account_id=?", id).Scan(&data)
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
	a.Entry.CredentialVersion = a.Revision
	return a.Entry, nil
}
func credentialBinding(id string, revision uint64) string {
	return fmt.Sprintf("credential/%s/%d", id, revision)
}
func (s *Store) saveCredential(ctx context.Context, tx *sql.Tx, c account.Credentials) error {
	data, err := s.sealValue(credentialBinding(c.AccountID, c.Revision), storedAccount{Revision: c.Revision, UID: c.UID, Cookies: c.Cookies, AccessToken: c.AccessToken})
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO account_credentials(account_id,revision,payload) VALUES(?,?,?) ON CONFLICT(account_id,revision) DO NOTHING", c.AccountID, c.Revision, data)
	return err
}
func (s *Store) CredentialVersion(ctx context.Context, id string, revision uint64) (account.Credentials, error) {
	var data []byte
	var revoked bool
	err := s.db.QueryRowContext(ctx, "SELECT revoked,payload FROM account_credentials WHERE account_id=? AND revision=?", id, revision).Scan(&revoked, &data)
	if errors.Is(err, sql.ErrNoRows) {
		// Version 3 stored only the current credential. Read it lazily until its first
		// mutation archives it; no old database or checkpoint needs rewriting.
		a, e := s.Account(ctx, id)
		if e == nil && a.Credentials.Revision == revision {
			return a.Credentials, nil
		}
		if e != nil && fault.As(e).Kind != fault.NotFound {
			return account.Credentials{}, e
		}
		return account.Credentials{}, fault.New(fault.Unsupported, "CREDENTIAL_REVOKED", "任务绑定的凭据已撤销")
	}
	if err != nil {
		return account.Credentials{}, err
	}
	if revoked {
		return account.Credentials{}, fault.New(fault.Unsupported, "CREDENTIAL_REVOKED", "任务绑定的凭据已撤销")
	}
	var a storedAccount
	if err = s.openValue(credentialBinding(id, revision), data, &a); err != nil {
		return account.Credentials{}, err
	}
	return account.Credentials{AccountID: id, Revision: revision, UID: a.UID, Cookies: a.Cookies, AccessToken: a.AccessToken}, nil
}
func presence(a *account.Entry) {
	a.CredentialVersion = a.Credentials.Revision
	a.Present = map[string]bool{"cookies": len(a.Credentials.Cookies) > 0, "access_token": a.Credentials.AccessToken != ""}
	for k, v := range a.Credentials.Cookies {
		a.Present[k] = v != ""
	}
}
func (s *Store) MutateAccount(ctx context.Context, id string, expected uint64, label *string, patch account.Patch, m operation.Mutation) (account.Entry, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return account.Entry{}, false, err
	}
	defer tx.Rollback()
	var replay struct {
		Account account.Entry
		Revoked bool
	}
	if found, e := s.replayRequest(ctx, tx, m, &replay); found || e != nil {
		return replay.Account, replay.Revoked, e
	}
	a, err := s.account(ctx, tx, id)
	if err != nil {
		return a, false, err
	}
	if a.Revision != expected {
		return a, false, fault.New(fault.Precondition, "REVISION_MISMATCH", "账号版本已改变")
	}
	revoked := false
	if label != nil {
		a.Label = *label
	}
	if patch != nil {
		if err = s.saveCredential(ctx, tx, a.Credentials); err != nil {
			return a, false, err
		}
		a.Credentials, revoked, err = account.Update(a.Credentials, patch)
		if err != nil {
			return a, false, err
		}
		if revoked {
			if _, err = tx.ExecContext(ctx, "UPDATE account_credentials SET revoked=1,payload=x'' WHERE account_id=?", id); err != nil {
				return a, false, err
			}
		}
		if err = s.saveCredential(ctx, tx, a.Credentials); err != nil {
			return a, false, err
		}
		a.Identity = account.Identity{}
		a.LoginState = "unverified"
	}
	a.Revision++
	a.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	presence(&a)
	c := a.Credentials
	data, err := s.sealValue("account/"+id, storedAccount{a, c.Revision, c.UID, c.Cookies, c.AccessToken})
	if err != nil {
		return a, false, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE accounts SET record=? WHERE account_id=?", data, id); err != nil {
		return a, false, err
	}
	replay.Account, replay.Revoked = a, revoked
	if err = s.saveReplay(ctx, tx, m, id, replay); err != nil {
		return a, false, err
	}
	return a, revoked, tx.Commit()
}
func (s *Store) DeleteAccount(ctx context.Context, id string, m operation.Mutation) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var deleted bool
	if found, e := s.replayRequest(ctx, tx, m, &deleted); found || e != nil {
		return e
	}
	a, err := s.account(ctx, tx, id)
	if err != nil {
		return err
	}
	var active int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM jobs WHERE account_id=? AND state NOT IN ('succeeded','partially_succeeded','failed','cancelled')", id).Scan(&active); err != nil {
		return err
	}
	if active > 0 {
		return fault.New(fault.Conflict, "ACCOUNT_IN_USE", "账号仍有活动或待核验任务，请先处理任务")
	}

	if err = s.saveCredential(ctx, tx, a.Credentials); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE account_credentials SET revoked=1,payload=x'' WHERE account_id=?", id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM accounts WHERE account_id=?", id); err != nil {
		return err
	}
	if err = s.saveReplay(ctx, tx, m, id, true); err != nil {
		return err
	}
	return tx.Commit()
}

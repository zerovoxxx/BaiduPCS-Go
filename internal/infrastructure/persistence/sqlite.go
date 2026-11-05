package persistence

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
)

type Store struct {
	db   *sql.DB
	seal cipher.AEAD
}

func Open(ctx context.Context, directory string, key []byte, defaults configuration.Client) (*Store, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.New("invalid master key")
	}
	seal, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	file := filepath.Join(directory, "service.db")
	if info, err := os.Lstat(file); err == nil && !info.Mode().IsRegular() {
		return nil, errors.New("service database must be a regular file")
	}
	db, err := openDatabase(file)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	store := &Store{db: db, seal: seal}
	ok := false
	defer func() {
		if !ok {
			db.Close()
		}
	}()
	if err = db.PingContext(ctx); err != nil {
		return nil, err
	}
	if err = os.Chmod(file, 0600); err != nil {
		return nil, err
	}
	if err = store.migrate(ctx); err != nil {
		return nil, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var revision int64
	err = tx.QueryRowContext(ctx, "SELECT revision FROM configuration_current WHERE id=1").Scan(&revision)
	if errors.Is(err, sql.ErrNoRows) {
		payload, err := store.encode(1, defaults)
		if err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO configuration_revisions(revision,payload) VALUES(1,?)", payload); err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO configuration_current(id,revision) VALUES(1,1)"); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	if _, err = store.Read(ctx); err != nil {
		return nil, err
	}
	ok = true
	return store, nil
}
func (s *Store) Close() error                   { return s.db.Close() }
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }
func (s *Store) migrate(ctx context.Context) error {
	var version int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > 6 {
		return errors.New("database schema is newer than this server")
	}
	if version == 6 {
		return nil
	}
	if version == 5 {
		_, err := s.db.ExecContext(ctx, `BEGIN IMMEDIATE;
 CREATE TABLE share_inspections(inspection_id TEXT PRIMARY KEY,payload BLOB NOT NULL,expires_at INTEGER NOT NULL);
 CREATE TABLE business_mutations(principal TEXT NOT NULL,operation TEXT NOT NULL,key_hash BLOB NOT NULL,request_hash BLOB NOT NULL,payload BLOB,expires_at INTEGER NOT NULL,PRIMARY KEY(principal,operation,key_hash));
 PRAGMA user_version=6;COMMIT;`)
		return err
	}
	if version == 4 {
		_, err := s.db.ExecContext(ctx, `BEGIN IMMEDIATE;
 CREATE TABLE staging_files(file_id TEXT PRIMARY KEY,owner TEXT NOT NULL,record BLOB NOT NULL,state TEXT NOT NULL,size INTEGER NOT NULL DEFAULT 0,expires_at INTEGER NOT NULL);
 CREATE TABLE staging_references(file_id TEXT NOT NULL REFERENCES staging_files(file_id),job_id TEXT NOT NULL REFERENCES jobs(job_id),PRIMARY KEY(file_id,job_id));
 PRAGMA user_version=5; COMMIT;`)
		if err != nil {
			return err
		}
		return s.migrate(ctx)
	}
	if version == 3 {
		_, err := s.db.ExecContext(ctx, `BEGIN IMMEDIATE;
 CREATE TABLE account_credentials(account_id TEXT NOT NULL, revision INTEGER NOT NULL, revoked INTEGER NOT NULL DEFAULT 0, payload BLOB NOT NULL, PRIMARY KEY(account_id,revision));
 ALTER TABLE jobs ADD COLUMN event_sequence INTEGER NOT NULL DEFAULT 0;
 CREATE INDEX jobs_query ON jobs(owner,created_at,job_id);
 CREATE TABLE query_snapshots(snapshot_id TEXT PRIMARY KEY, binding TEXT NOT NULL, payload BLOB NOT NULL, expires_at INTEGER NOT NULL);
 CREATE INDEX query_snapshots_expiry ON query_snapshots(expires_at);
 CREATE TABLE job_items(job_id TEXT NOT NULL REFERENCES jobs(job_id), item_index INTEGER NOT NULL, payload BLOB NOT NULL, state TEXT NOT NULL DEFAULT '', PRIMARY KEY(job_id,item_index));
 CREATE TABLE artifacts(artifact_id TEXT PRIMARY KEY, job_id TEXT NOT NULL REFERENCES jobs(job_id), record BLOB NOT NULL, expires_at INTEGER NOT NULL, state TEXT NOT NULL, size INTEGER NOT NULL DEFAULT 0);
 CREATE INDEX artifacts_job ON artifacts(job_id);
 PRAGMA user_version=4;
 COMMIT;`)
		if err != nil {
			return err
		}
		return s.migrate(ctx)
	}
	if version == 2 {
		_, err := s.db.ExecContext(ctx, `BEGIN IMMEDIATE;
 ALTER TABLE jobs ADD COLUMN version INTEGER NOT NULL DEFAULT 1;
 ALTER TABLE jobs ADD COLUMN started_at TEXT;
 ALTER TABLE jobs ADD COLUMN finished_at TEXT;
 CREATE INDEX jobs_dispatch ON jobs(kind,state,created_at);
 CREATE TABLE request_replays(principal TEXT NOT NULL, operation TEXT NOT NULL, key_hash BLOB NOT NULL, request_hash BLOB NOT NULL, resource_id TEXT NOT NULL, reply BLOB NOT NULL, expires_at INTEGER NOT NULL, PRIMARY KEY(principal,operation,key_hash));
 CREATE INDEX request_replays_expiry ON request_replays(expires_at);
 PRAGMA user_version=3;
 COMMIT;`)
		if err != nil {
			return err
		}
		return s.migrate(ctx)
	}
	if version == 1 {
		_, err := s.db.ExecContext(ctx, `BEGIN IMMEDIATE;
 CREATE TABLE accounts(account_id TEXT PRIMARY KEY, record BLOB NOT NULL);
 PRAGMA user_version=2;
 COMMIT;`)
		if err != nil {
			return err
		}
		return s.migrate(ctx)
	}
	// Only the initial schema is present in P1. Later job services add migrations
	// instead of silently recreating or discarding a user's stored state.
	_, err := s.db.ExecContext(ctx, `PRAGMA journal_mode=WAL;
 BEGIN IMMEDIATE;
 CREATE TABLE configuration_revisions(revision INTEGER PRIMARY KEY, payload BLOB NOT NULL);
 CREATE TABLE configuration_current(id INTEGER PRIMARY KEY CHECK(id=1), revision INTEGER NOT NULL REFERENCES configuration_revisions(revision));
 CREATE TABLE idempotency_records(principal_id TEXT NOT NULL, operation TEXT NOT NULL, key_hash BLOB NOT NULL, request_hash BLOB NOT NULL, revision INTEGER NOT NULL REFERENCES configuration_revisions(revision), fields TEXT NOT NULL, expires_at INTEGER NOT NULL, PRIMARY KEY(principal_id,operation,key_hash));
 CREATE TABLE jobs(job_id TEXT PRIMARY KEY, owner TEXT NOT NULL, account_id TEXT, kind TEXT NOT NULL, state TEXT NOT NULL, record BLOB NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
 CREATE TABLE job_events(job_id TEXT NOT NULL REFERENCES jobs(job_id), sequence INTEGER NOT NULL, event_type TEXT NOT NULL, payload BLOB NOT NULL, created_at TEXT NOT NULL, PRIMARY KEY(job_id,sequence));
 PRAGMA user_version=1;
 COMMIT;`)
	if err != nil {
		return err
	}
	return s.migrate(ctx)
}
func (s *Store) encode(revision uint64, value configuration.Client) ([]byte, error) {
	plain, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, s.seal.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	return s.seal.Seal(nonce, nonce, plain, []byte("configuration/"+strconv.FormatUint(revision, 10))), nil
}
func (s *Store) decode(revision uint64, payload []byte) (configuration.Snapshot, error) {
	size := s.seal.NonceSize()
	if len(payload) < size {
		return configuration.Snapshot{}, errors.New("invalid encrypted configuration")
	}
	plain, err := s.seal.Open(nil, payload[:size], payload[size:], []byte("configuration/"+strconv.FormatUint(revision, 10)))
	if err != nil {
		return configuration.Snapshot{}, errors.New("cannot decrypt service configuration with supplied master key")
	}
	var value configuration.Client
	if err = json.Unmarshal(plain, &value); err != nil {
		return configuration.Snapshot{}, errors.New("invalid stored configuration")
	}
	if len(configuration.Validate(value)) > 0 {
		return configuration.Snapshot{}, errors.New("stored configuration does not satisfy current constraints")
	}
	return configuration.NewSnapshot(revision, value), nil
}
func (s *Store) Read(ctx context.Context) (configuration.Snapshot, error) {
	var revision uint64
	var payload []byte
	err := s.db.QueryRowContext(ctx, "SELECT r.revision,r.payload FROM configuration_current c JOIN configuration_revisions r ON r.revision=c.revision WHERE c.id=1").Scan(&revision, &payload)
	if err != nil {
		return configuration.Snapshot{}, err
	}
	return s.decode(revision, payload)
}

type queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *Store) replay(ctx context.Context, q queryer, m configuration.Mutation) (*configuration.Change, error) {
	if !m.HasKey {
		return nil, nil
	}
	var hash, payload []byte
	var revision uint64
	var fields string
	err := q.QueryRowContext(ctx, `SELECT i.request_hash,i.revision,i.fields,r.payload FROM idempotency_records i JOIN configuration_revisions r ON r.revision=i.revision WHERE i.principal_id=? AND i.operation=? AND i.key_hash=? AND i.expires_at>?`, m.PrincipalID, m.Operation, m.KeyHash[:], time.Now().Unix()).Scan(&hash, &revision, &fields, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if subtle.ConstantTimeCompare(hash, m.RequestHash[:]) != 1 {
		return nil, fault.New(fault.Conflict, "IDEMPOTENCY_KEY_REUSED", "幂等Key已用于不同请求")
	}
	snapshot, err := s.decode(revision, payload)
	if err != nil {
		return nil, err
	}
	names := []string{}
	if err = json.Unmarshal([]byte(fields), &names); err != nil {
		return nil, err
	}
	return &configuration.Change{Snapshot: snapshot, Fields: names}, nil
}
func (s *Store) Replay(ctx context.Context, m configuration.Mutation) (*configuration.Change, error) {
	return s.replay(ctx, s.db, m)
}
func (s *Store) Commit(ctx context.Context, expected uint64, value configuration.Client, m configuration.Mutation) (configuration.Change, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return configuration.Change{}, err
	}
	defer tx.Rollback()
	if replay, err := s.replay(ctx, tx, m); err != nil {
		return configuration.Change{}, err
	} else if replay != nil {
		return *replay, nil
	}
	var current uint64
	if err = tx.QueryRowContext(ctx, "SELECT revision FROM configuration_current WHERE id=1").Scan(&current); err != nil {
		return configuration.Change{}, err
	}
	if current != expected {
		return configuration.Change{}, fault.New(fault.Precondition, "REVISION_MISMATCH", "配置版本已改变")
	}
	if current >= math.MaxInt64 {
		return configuration.Change{}, errors.New("configuration revision exhausted")
	}
	next := current + 1
	payload, err := s.encode(next, value)
	if err != nil {
		return configuration.Change{}, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO configuration_revisions(revision,payload) VALUES(?,?)", next, payload); err != nil {
		return configuration.Change{}, err
	}
	result, err := tx.ExecContext(ctx, "UPDATE configuration_current SET revision=? WHERE id=1 AND revision=?", next, current)
	if err != nil {
		return configuration.Change{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return configuration.Change{}, errors.New("configuration compare-and-swap failed")
	}
	if m.HasKey {
		fields, err := json.Marshal(m.Fields)
		if err != nil {
			return configuration.Change{}, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO idempotency_records(principal_id,operation,key_hash,request_hash,revision,fields,expires_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(principal_id,operation,key_hash) DO UPDATE SET request_hash=excluded.request_hash,revision=excluded.revision,fields=excluded.fields,expires_at=excluded.expires_at`, m.PrincipalID, m.Operation, m.KeyHash[:], m.RequestHash[:], next, string(fields), time.Now().Add(24*time.Hour).Unix()+1); err != nil {
			return configuration.Change{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return configuration.Change{}, fmt.Errorf("persist configuration: %w", err)
	}
	return configuration.Change{Snapshot: configuration.NewSnapshot(next, value), Fields: append([]string{}, m.Fields...)}, nil
}

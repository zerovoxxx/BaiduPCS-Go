package persistence

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/account"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/operation"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/transfer"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/infrastructure/baidu"
)

func p2Store(t *testing.T) (*Store, string, []byte) {
	t.Helper()
	if !Supported() {
		t.Skip("storage unsupported")
	}
	dir := t.TempDir()
	key := []byte(strings.Repeat("k", 32))
	s, err := Open(context.Background(), dir, key, baidu.DefaultConfiguration())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, dir, key
}
func p2Record() transfer.Record {
	return transfer.Record{ID: "job_p2", Owner: "owner", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Request: transfer.Request{Kind: "upload", AccountID: "acct_p2", RootID: "root", RelativePath: "file", RemotePath: "/file"}, Config: baidu.DefaultConfiguration(), ConfigRevision: 1, CredentialRevision: 1}
}
func TestP2MigrationFromV1AndV2PreservesEncryptedState(t *testing.T) {
	for _, version := range []int{1, 2} {
		t.Run(string(rune('0'+version)), func(t *testing.T) {
			s, dir, key := p2Store(t)
			ctx := context.Background()
			cfg := baidu.DefaultConfiguration()
			cfg.NoCheck = false
			change, err := s.Commit(ctx, 1, cfg, configuration.Mutation{})
			if err != nil {
				t.Fatal(err)
			}
			r := p2Record()
			if _, err = s.CreateTransfer(ctx, r); err != nil {
				t.Fatal(err)
			}
			if version == 2 {
				a := account.Entry{ID: "acct_p2", Label: "keep", CreatedAt: r.CreatedAt, Credentials: account.Credentials{AccountID: "acct_p2", Revision: 7, Cookies: map[string]string{"BDUSS": "ENCRYPTED_ONLY"}}}
				if err = s.CreateAccount(ctx, a); err != nil {
					t.Fatal(err)
				}
			}
			// Remove only the additive P2 columns to reproduce the historic schema.
			sql := `DROP TABLE business_mutations;DROP TABLE share_inspections;DROP TABLE staging_references;DROP TABLE staging_files;DELETE FROM job_events;DROP TABLE account_credentials; DROP TABLE query_snapshots; DROP TABLE job_items; DROP TABLE artifacts; DROP INDEX jobs_query; ALTER TABLE jobs DROP COLUMN event_sequence; DROP TABLE request_replays;DROP INDEX jobs_dispatch;ALTER TABLE jobs DROP COLUMN version;ALTER TABLE jobs DROP COLUMN started_at;ALTER TABLE jobs DROP COLUMN finished_at;`
			if version == 1 {
				sql += `DROP TABLE accounts;PRAGMA user_version=1;`
			} else {
				sql += `PRAGMA user_version=2;`
			}
			if _, err = s.db.ExecContext(ctx, sql); err != nil {
				t.Fatal(err)
			}
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			upgraded, err := Open(ctx, dir, key, baidu.DefaultConfiguration())
			if err != nil {
				t.Fatal(err)
			}
			defer upgraded.Close()
			snapshot, err := upgraded.Read(ctx)
			if err != nil || snapshot.Revision() != change.Snapshot.Revision() || snapshot.Client().NoCheck {
				t.Fatal("migration lost configuration", err)
			}
			restored, err := upgraded.Transfer(ctx, r.ID)
			if err != nil || restored.State != "queued" || restored.ConfigRevision != 1 {
				t.Fatal("migration lost task", err)
			}
			if version == 2 {
				a, err := upgraded.Account(ctx, "acct_p2")
				if err != nil || a.Credentials.Revision != 7 || a.Credentials.Cookies["BDUSS"] != "ENCRYPTED_ONLY" || a.Revision != 1 {
					t.Fatal("migration lost account", err)
				}
			}
			var current int
			if err = upgraded.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&current); err != nil || current != 6 {
				t.Fatal("wrong schema version", current, err)
			}
		})
	}
}
func TestP2StateWritesCheckAffectedRowsAndKeepVersions(t *testing.T) {
	s, _, _ := p2Store(t)
	ctx := context.Background()
	r := p2Record()
	if _, err := s.CreateTransfer(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveTransferCheckpoint(ctx, r.ID, json.RawMessage(`{}`)); err == nil {
		t.Fatal("checkpoint before execution reported persisted")
	}
	if ok, err := s.TransitionTransfer(ctx, r.ID, []string{"queued"}, "running"); err != nil || !ok {
		t.Fatal(err)
	}
	if err := s.SaveTransferCheckpoint(ctx, r.ID, json.RawMessage(`{"Phase":"done"}`)); err != nil {
		t.Fatal(err)
	}
	before, err := s.Transfer(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.FinishTransfer(ctx, r.ID, "succeeded", ""); err != nil {
		t.Fatal(err)
	}
	after, err := s.Transfer(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.StartedAt == "" || after.FinishedAt == "" || after.Version <= before.Version || after.UpdatedAt < before.UpdatedAt {
		t.Fatal("missing lifecycle or CAS version")
	}
	if err = s.FinishTransfer(ctx, r.ID, "failed", "LATE"); err == nil {
		t.Fatal("late finish reported success")
	}
	if err = s.SaveTransferCheckpoint(ctx, r.ID, json.RawMessage(`{"Phase":"bad"}`)); err == nil {
		t.Fatal("late checkpoint reported success")
	}
	final, err := s.Transfer(ctx, r.ID)
	if err != nil || final.State != "succeeded" {
		t.Fatal("late write changed terminal state", err)
	}
}
func TestP2ControlReplayRetentionAndCiphertextFailure(t *testing.T) {
	s, _, key := p2Store(t)
	ctx := context.Background()
	r := p2Record()
	if _, err := s.CreateTransfer(ctx, r); err != nil {
		t.Fatal(err)
	}
	mutation := operation.New("owner", "JOB08/job_p2", "key", "reason")
	first, err := s.ControlTransfer(ctx, r.ID, []string{"queued"}, "queued", mutation)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, "UPDATE request_replays SET expires_at=0"); err != nil {
		t.Fatal(err)
	}
	if err = s.CleanupReplays(ctx); err != nil {
		t.Fatal(err)
	}
	replay, err := s.ReplayControl(ctx, mutation)
	if err != nil || replay == nil || replay.Version != first.Version {
		t.Fatal("active task replay expired", err)
	}
	changed := operation.New("owner", "JOB08/job_p2", "key", "different")
	if _, err = s.ReplayControl(ctx, changed); fault.As(err).Kind != fault.Conflict {
		t.Fatal("body mismatch accepted", err)
	}
	if _, err = s.db.ExecContext(ctx, "UPDATE jobs SET record=randomblob(80) WHERE job_id=?", r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Transfer(ctx, r.ID); err == nil {
		t.Fatal("tampered ciphertext accepted")
	}
	if _, err = Open(ctx, t.TempDir(), key[:31], baidu.DefaultConfiguration()); err == nil {
		t.Fatal("invalid key accepted")
	}
}

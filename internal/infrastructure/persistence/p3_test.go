package persistence

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/account"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/operation"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/transfer"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/infrastructure/baidu"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/infrastructure/localfs"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/ports"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestP3JobEventTransactionsAndRestartSequence(t *testing.T) {
	s, dir, key := p2Store(t)
	ctx := context.Background()
	r := p2Record()
	fail := func() {
		t.Helper()
		if _, err := s.db.Exec(`CREATE TRIGGER p3_event_failure BEFORE INSERT ON job_events BEGIN SELECT RAISE(ABORT,'event write failed'); END`); err != nil {
			t.Fatal(err)
		}
	}
	restore := func() {
		t.Helper()
		if _, err := s.db.Exec("DROP TRIGGER p3_event_failure"); err != nil {
			t.Fatal(err)
		}
	}
	fail()
	if _, err := s.CreateTransfer(ctx, r); err == nil {
		t.Fatal("job accepted without durable creation event")
	}
	if _, err := s.Transfer(ctx, r.ID); fault.As(err).Kind != fault.NotFound {
		t.Fatal("failed transaction left job")
	}
	restore()
	if _, err := s.CreateTransfer(ctx, r); err != nil {
		t.Fatal(err)
	}
	before, head, err := s.EventSnapshot(ctx, r.ID)
	if err != nil || head != 1 {
		t.Fatal(err, head)
	}
	fail()
	if _, err = s.TransitionTransfer(ctx, r.ID, []string{"queued"}, "running"); err == nil {
		t.Fatal("state accepted without event")
	}
	after, h, err := s.EventSnapshot(ctx, r.ID)
	if err != nil || h != head || after.Version != before.Version || after.State != "queued" {
		t.Fatal("state/event CAS transaction split")
	}
	restore()
	if _, err = s.TransitionTransfer(ctx, r.ID, []string{"queued"}, "running"); err != nil {
		t.Fatal(err)
	}
	fail()
	if err = s.SaveTransferCheckpoint(ctx, r.ID, json.RawMessage(`{"Phase":"uploading","UploadID":"PRIVATE_ID"}`)); err == nil {
		t.Fatal("checkpoint accepted without event")
	}
	after, err = s.Transfer(ctx, r.ID)
	if err != nil || len(after.Checkpoint) != 0 {
		t.Fatal("checkpoint transaction split")
	}
	restore()
	if err = s.SaveTransferCheckpoint(ctx, r.ID, json.RawMessage(`{"Phase":"uploading","UploadID":"PRIVATE_ID"}`)); err != nil {
		t.Fatal(err)
	}
	m := operation.New("owner", "JOB06/"+r.ID, "cancel", "reason")
	fail()
	if _, err = s.ControlTransfer(ctx, r.ID, []string{"running"}, "cancelling", m); err == nil {
		t.Fatal("control accepted without event")
	}
	replay, err := s.ReplayControl(ctx, m)
	if err != nil || replay != nil {
		t.Fatal("failed control retained replay")
	}
	restore()
	fail()
	if err = s.FinishTransfer(ctx, r.ID, "succeeded", ""); err == nil {
		t.Fatal("finish accepted without events")
	}
	after, err = s.Transfer(ctx, r.ID)
	if err != nil || after.State != "running" {
		t.Fatal("failed finish changed state")
	}
	restore()
	if err = s.FinishTransfer(ctx, r.ID, "succeeded", ""); err != nil {
		t.Fatal(err)
	}
	_, head, err = s.EventSnapshot(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, dir, key, baidu.DefaultConfiguration())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	events, err := s.TransferEvents(ctx, r.ID, 0, 100)
	if err != nil || events.Head != head || len(events.Events) != int(head) {
		t.Fatal("events lost across restart", err)
	}
	for i, e := range events.Events {
		if e.Sequence != uint64(i+1) {
			t.Fatal("event IDs not monotone")
		}
	}
	for _, e := range events.Events {
		if e.Type == "job.finished" && !e.Record.Terminal() {
			t.Fatal("finish preceded terminal commit")
		}
	}
	if _, err = s.db.Exec("UPDATE job_events SET created_at=? WHERE job_id=?", time.Now().Add(-25*time.Hour).UTC().Format(time.RFC3339Nano), r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.TransferEvents(ctx, r.ID, head, 128); fault.As(err).Code != "EVENT_CURSOR_EXPIRED" {
		t.Fatal("expired latest event accepted", err)
	}
	if _, err = s.TransferEvents(ctx, r.ID, head+1, 128); fault.As(err).Code != "EVENT_CURSOR_FUTURE" {
		t.Fatal(err)
	}
}
func TestP3SnapshotBindingExpirationAndEncryptedCursors(t *testing.T) {
	s, dir, key := p2Store(t)
	ctx := context.Background()
	items := []json.RawMessage{json.RawMessage(`{"value":1}`), json.RawMessage(`{"value":2}`)}
	page, err := s.CreateQuery(ctx, "owner/account/query", items, 1)
	if err != nil || !page.HasMore {
		t.Fatal(err)
	}
	if _, err = s.ReadQuery(ctx, "other/account/query", page.NextCursor, 1); fault.As(err).Code != "QUERY_CURSOR_MISMATCH" {
		t.Fatal(err)
	}
	if _, err = s.ReadQuery(ctx, "owner/account/query", page.NextCursor+"x", 1); fault.As(err).Kind != fault.Invalid {
		t.Fatal("tampered cursor accepted", err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, dir, key, baidu.DefaultConfiguration())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	next, err := s.ReadQuery(ctx, "owner/account/query", page.NextCursor, 1)
	if err != nil || next.HasMore || len(next.Items) != 1 {
		t.Fatal("snapshot restart failed", err)
	}
	if _, err = s.db.Exec("UPDATE query_snapshots SET expires_at=0"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReadQuery(ctx, "owner/account/query", page.NextCursor, 1); fault.As(err).Code != "QUERY_SNAPSHOT_EXPIRED" {
		t.Fatal(err)
	}
	tooMany := make([]json.RawMessage, 10001)
	if _, err = s.CreateQuery(ctx, "q", tooMany, 1); fault.As(err).Code != "QUERY_SNAPSHOT_LIMIT" {
		t.Fatal(err)
	}
}
func TestP3ItemPaginationHighWaterBeyondTenThousand(t *testing.T) {
	s, _, _ := p2Store(t)
	ctx := context.Background()
	r := p2Record()
	r.Request.Kind = "file_scan"
	if _, err := s.CreateTransfer(ctx, r); err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := 0; i < 10001; i++ {
		item := transfer.Item{ID: fmt.Sprint("item_", i), Index: uint64(i), Path: fmt.Sprint("/file", i), State: "succeeded"}
		data, e := s.sealValue(fmt.Sprintf("item/%s/%d", r.ID, i), item)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = tx.ExecContext(ctx, "INSERT INTO job_items(job_id,item_index,payload) VALUES(?,?,?)", r.ID, i, data); e != nil {
			t.Fatal(e)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	page, err := s.TransferItemPage(ctx, r.ID, "owner", "", 1000)
	if err != nil {
		t.Fatal(err)
	}
	cursor := page.NextCursor
	total := len(page.Items)
	// New rows must not leak into the original cursor's fixed item set.
	extra := transfer.Item{ID: "item_extra", Index: 10001}
	data, _ := s.sealValue(fmt.Sprintf("item/%s/%d", r.ID, 10001), extra)
	if _, err = s.db.Exec("INSERT INTO job_items(job_id,item_index,payload) VALUES(?,?,?)", r.ID, 10001, data); err != nil {
		t.Fatal(err)
	}
	if _, err = s.TransferItemPage(ctx, r.ID, "other", cursor, 1000); fault.As(err).Code != "QUERY_CURSOR_MISMATCH" {
		t.Fatal(err)
	}
	for cursor != "" {
		page, err = s.TransferItemPage(ctx, r.ID, "owner", cursor, 1000)
		if err != nil {
			t.Fatal(err)
		}
		total += len(page.Items)
		cursor = page.NextCursor
	}
	if total != 10001 {
		t.Fatal("item snapshot lost or added rows", total)
	}
}
func TestP3ArtifactTTLReferencesCleanupAndSummaryRetention(t *testing.T) {
	s, dir, _ := p2Store(t)
	ctx := context.Background()
	r := p2Record()
	r.Request.Kind = "file_scan"
	if _, err := s.CreateTransfer(ctx, r); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TransitionTransfer(ctx, r.ID, []string{"queued"}, "running"); err != nil {
		t.Fatal(err)
	}
	files, err := localfs.NewArtifacts(dir, s)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := files.Create(ctx, r.ID, "scan.jsonl", "application/x-ndjson")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = writer.Write([]byte("durable output\n")); err != nil {
		t.Fatal(err)
	}
	a, err := writer.Complete(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = files.Delete(ctx, a.ID, operation.Mutation{}); fault.As(err).Code != "ARTIFACT_IN_USE" {
		t.Fatal("active job reference ignored", err)
	}
	result := transfer.ScanResult{Complete: true, ArtifactIDs: []string{a.ID}}
	if err = s.SaveScanResult(ctx, r.ID, result); err != nil {
		t.Fatal(err)
	}
	_, lease, err := files.Open(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.FinishTransfer(ctx, r.ID, "succeeded", ""); err != nil {
		t.Fatal(err)
	}
	if err = files.Delete(ctx, a.ID, operation.Mutation{}); fault.As(err).Code != "ARTIFACT_IN_USE" {
		t.Fatal("active response reference ignored", err)
	}
	a.ExpiresAt = time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)
	payload, _ := s.sealValue("artifact/"+a.ID, a)
	if _, err = s.db.Exec("UPDATE artifacts SET record=?,expires_at=0 WHERE artifact_id=?", payload, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = files.Open(ctx, a.ID); fault.As(err).Code != "RESOURCE_EXPIRED" {
		t.Fatal(err)
	}
	if err = files.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(dir, "artifacts", a.ID)); err != nil {
		t.Fatal("cleanup deleted active response")
	}
	if err = lease.Close(); err != nil {
		t.Fatal(err)
	}
	if err = files.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(dir, "artifacts", a.ID)); !os.IsNotExist(err) {
		t.Fatal("expired file not reclaimed", err)
	}
	job, err := s.Transfer(ctx, r.ID)
	if err != nil || len(job.ScanResult.ArtifactIDs) != 1 || !job.ScanResult.Complete {
		t.Fatal("cleanup changed final summary", err)
	}
	meta, err := s.Artifact(ctx, a.ID)
	if err != nil || meta.State != "deleted" {
		t.Fatal("missing tombstone", err)
	}
	// The encrypted metadata never stores file bytes in SQLite.
	var stored []byte
	if err = s.db.QueryRow("SELECT record FROM artifacts WHERE artifact_id=?", a.ID).Scan(&stored); err != nil || bytes.Contains(stored, []byte("durable output")) {
		t.Fatal("unexpected artifact payload in DB", err)
	}
}

func TestP3MigrationFromV3PreservesJobsAndCheckpoints(t *testing.T) {
	s, dir, key := p2Store(t)
	ctx := context.Background()
	r := p2Record()
	r.Admission = operation.New(r.Owner, "UPL01/acct_p2", "legacy-key", r.Request)
	if _, err := s.CreateTransfer(ctx, r); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TransitionTransfer(ctx, r.ID, []string{"queued"}, "running"); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveTransferCheckpoint(ctx, r.ID, json.RawMessage(`{"Version":1,"Phase":"uploading","UploadID":"PRIVATE_LEGACY_UPLOAD_ID"}`)); err != nil {
		t.Fatal(err)
	}
	// Reproduce the actual v3 schema; P2 did not populate its reserved event table.
	_, err := s.db.Exec(`DROP TABLE staging_references;DROP TABLE staging_files;DELETE FROM job_events;DROP TABLE account_credentials;DROP TABLE query_snapshots;DROP TABLE job_items;DROP TABLE artifacts;DROP INDEX jobs_query;ALTER TABLE jobs DROP COLUMN event_sequence;PRAGMA user_version=3;`)
	if err != nil {
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
	before, err := upgraded.Transfer(ctx, r.ID)
	if err != nil || !bytes.Contains(before.Checkpoint, []byte("PRIVATE_LEGACY_UPLOAD_ID")) {
		t.Fatal("v3 checkpoint lost", err)
	}
	if err = upgraded.InterruptTransfers(ctx); err != nil {
		t.Fatal(err)
	}
	after, seq, err := upgraded.EventSnapshot(ctx, r.ID)
	if err != nil || after.State != "interrupted" || after.Version <= before.Version || seq != 1 {
		t.Fatal("v3 recovery state/sequence invalid", err)
	}
	replay, err := upgraded.ReplayControl(ctx, r.Admission)
	if err != nil || replay == nil || replay.ID != r.ID {
		t.Fatal("v3 replay lost", err)
	}
}

func TestP3LegacyImportReplaySurvivesRetentionAndDeletion(t *testing.T) {
	s, _, _ := p2Store(t)
	ctx := context.Background()
	a := account.Entry{ID: "acct_legacy", Revision: 1, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Credentials: account.Credentials{AccountID: "acct_legacy", Revision: 1, Cookies: map[string]string{"BDUSS": "PRIVATE_LEGACY"}}, Present: map[string]bool{"BDUSS": true, "cookies": true}}
	m := operation.New("legacy-import", "ACC01/legacy", "fingerprint", "fingerprint")
	if _, err := s.CreateAccountOnce(ctx, a, m); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE request_replays SET expires_at=0"); err != nil {
		t.Fatal(err)
	}
	if err := s.CleanupReplays(ctx); err != nil {
		t.Fatal(err)
	}
	changed := a
	changed.ID = "acct_new"
	changed.Credentials.AccountID = changed.ID
	replay, err := s.CreateAccountOnce(ctx, changed, m)
	if err != nil || replay.ID != a.ID {
		t.Fatal("legacy replay expired", err)
	}
	if err = s.DeleteAccount(ctx, a.ID, operation.Mutation{}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateAccountOnce(ctx, changed, m); err != nil {
		t.Fatal(err)
	}
	all, err := s.Accounts(ctx)
	if err != nil || len(all) != 0 {
		t.Fatal("legacy replay resurrected deleted account", err)
	}
	var payload []byte
	var revoked bool
	if err = s.db.QueryRow("SELECT revoked,payload FROM account_credentials WHERE account_id=?", a.ID).Scan(&revoked, &payload); err != nil || !revoked || len(payload) != 0 {
		t.Fatal("revocation retained recoverable secret payload", err)
	}
}

func TestP3EventCountRetentionNeverReusesSequence(t *testing.T) {
	s, _, _ := p2Store(t)
	ctx := context.Background()
	r := p2Record()
	if _, err := s.CreateTransfer(ctx, r); err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	statement, err := tx.PrepareContext(ctx, "INSERT INTO job_events(job_id,sequence,event_type,payload,created_at) VALUES(?,?,?,?,?)")
	if err != nil {
		t.Fatal(err)
	}
	defer statement.Close()
	for i := uint64(2); i <= EventLimit; i++ {
		e := transfer.Event{Sequence: i, Type: "job.progress", OccurredAt: now, Record: &r}
		data, err := s.sealValue(fmt.Sprintf("event/%s/%d", r.ID, i), e)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = statement.ExecContext(ctx, r.ID, i, e.Type, data, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = tx.ExecContext(ctx, "UPDATE jobs SET event_sequence=? WHERE job_id=?", EventLimit, r.ID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err = s.TransitionTransfer(ctx, r.ID, []string{"queued"}, "running"); err != nil {
		t.Fatal(err)
	}
	var count, oldest, head int
	if err = s.db.QueryRow("SELECT count(*),min(sequence),max(sequence) FROM job_events WHERE job_id=?", r.ID).Scan(&count, &oldest, &head); err != nil || count != EventLimit || oldest != 2 || head != EventLimit+1 {
		t.Fatal("event count bound not enforced", count, oldest, head, err)
	}
	if _, err = s.TransferEvents(ctx, r.ID, 0, 128); fault.As(err).Code != "EVENT_CURSOR_EXPIRED" {
		t.Fatal("pruned cursor accepted", err)
	}
	batch, err := s.TransferEvents(ctx, r.ID, EventLimit, 128)
	if err != nil || len(batch.Events) != 1 || batch.Events[0].Sequence != EventLimit+1 {
		t.Fatal("retention reset sequence", err)
	}
}
func TestP3ArtifactReservationsEnforceAndReleaseTotalQuota(t *testing.T) {
	s, dir, _ := p2Store(t)
	ctx := context.Background()
	r := p2Record()
	if _, err := s.CreateTransfer(ctx, r); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TransitionTransfer(ctx, r.ID, []string{"queued"}, "running"); err != nil {
		t.Fatal(err)
	}
	files, err := localfs.NewArtifacts(dir, s)
	if err != nil {
		t.Fatal(err)
	}
	writers := []ports.ArtifactWriter{}
	for i := 0; i < 4; i++ {
		w, err := files.Create(ctx, r.ID, "scan.jsonl", "application/x-ndjson")
		if err != nil {
			t.Fatal(err)
		}
		writers = append(writers, w)
	}
	if _, err = files.Create(ctx, r.ID, "overflow", "application/octet-stream"); fault.As(err).Code != "ARTIFACT_QUOTA_EXCEEDED" {
		t.Fatal("reserved bytes did not consume quota", err)
	}
	if err = writers[0].Abort(ctx); err != nil {
		t.Fatal(err)
	}
	replacement, err := files.Create(ctx, r.ID, "replacement", "application/octet-stream")
	if err != nil {
		t.Fatal("aborted reservation still consumes quota", err)
	}
	writers[0] = replacement
	for _, w := range writers {
		if err = w.Abort(ctx); err != nil {
			t.Fatal(err)
		}
	}
}

func TestP3JobQueriesApplyVisibilityBeforeLimitAndKeepTimePrecision(t *testing.T) {
	s, _, _ := p2Store(t)
	ctx := context.Background()
	for i, stamp := range []string{"2026-11-05T01:02:03.000000100Z", "2026-11-05T01:02:03.000000200Z", "2026-11-05T01:02:03.000000300Z"} {
		r := p2Record()
		r.ID = fmt.Sprint("job_query_", i)
		r.CreatedAt = stamp
		r.Owner = "owner"
		if _, err := s.CreateTransfer(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	visible := func(r transfer.Record) bool { return r.ID == "job_query_0" }
	rows, err := s.ListTransfers(ctx, transfer.Filter{Owner: "owner"}, 1, visible)
	if err != nil || len(rows) != 1 || rows[0].ID != "job_query_0" {
		t.Fatal("invisible rows consumed the query limit", err)
	}
	rows, err = s.ListTransfers(ctx, transfer.Filter{After: "2026-11-05T01:02:03.000000150Z", Before: "2026-11-05T01:02:03.000000250Z"}, 100, nil)
	if err != nil || len(rows) != 1 || rows[0].ID != "job_query_1" {
		t.Fatal("sub-millisecond query precision lost", err)
	}
	rows, err = s.ListTransfers(ctx, transfer.Filter{Owner: "other"}, 1, nil)
	if err != nil || len(rows) != 0 {
		t.Fatal("owner filter ignored", err)
	}
}

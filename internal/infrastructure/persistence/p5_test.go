package persistence

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/file"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/operation"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/staging"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/write"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/infrastructure/baidu"
)

func TestP5MigrationFromV5PreservesCheckpointReceiptAndStage(t *testing.T) {
	s, dir, key := p2Store(t)
	ctx := context.Background()
	r := p2Record()
	r.Admission = operation.New(r.Owner, "UPL01/acct_p2", "p5-migration", r.Request)
	if _, err := s.CreateTransfer(ctx, r); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TransitionTransfer(ctx, r.ID, []string{"queued"}, "running"); err != nil {
		t.Fatal(err)
	}
	cp := json.RawMessage(`{"Version":1,"Phase":"uploading","UploadID":"PRIVATE_V5","Offset":4096}`)
	if err := s.SaveTransferCheckpoint(ctx, r.ID, cp); err != nil {
		t.Fatal(err)
	}
	m := operation.New(r.Owner, "JOB07/"+r.ID, "v5-receipt", "pause")
	if _, err := s.ControlTransfer(ctx, r.ID, []string{"running"}, "paused", m); err != nil {
		t.Fatal(err)
	}
	stage := staging.File{ID: "stage_v5", Owner: r.Owner, Filename: "migration", Size: 3, State: "writing", ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)}
	if err := s.ReserveStage(ctx, stage); err != nil {
		t.Fatal(err)
	}
	stage.State = "ready"
	if _, err := s.FinishStage(ctx, stage, operation.Mutation{}); err != nil {
		t.Fatal(err)
	}
	// Retain the encrypted v5 stage and reference while removing only v6 tables.
	if _, err := s.db.Exec(`INSERT INTO staging_references VALUES('stage_v5','job_p2');DROP TABLE resources;DROP TABLE business_mutations;DROP TABLE share_inspections;PRAGMA user_version=5;`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	next, err := Open(ctx, dir, key, baidu.DefaultConfiguration())
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	var v, refs int
	if err = next.db.QueryRow("PRAGMA user_version").Scan(&v); err != nil || v != 7 {
		t.Fatal(v, err)
	}
	got, err := next.Transfer(ctx, r.ID)
	if err != nil || string(got.Checkpoint) != string(cp) || got.State != "paused" {
		t.Fatal("v5 task lost", err)
	}
	replay, err := next.ReplayControl(ctx, m)
	if err != nil || replay == nil || replay.State != "paused" {
		t.Fatal("v5 receipt lost", err)
	}
	if err = next.db.QueryRow("SELECT count(*) FROM staging_references WHERE file_id='stage_v5'").Scan(&refs); err != nil || refs != 1 {
		t.Fatal("v5 stage reference lost", err)
	}
	restored, e := next.Stage(ctx, stage.ID)
	if e != nil || restored.Size != 3 || restored.References != 1 || restored.Filename != stage.Filename {
		t.Fatal("v5 encrypted stage changed", e)
	}
}

func TestP5InspectionMergeExpiryAndUnknownMutationReplay(t *testing.T) {
	s, _, _ := p2Store(t)
	ctx := context.Background()
	v := write.Inspection{ID: "inspect_test", Access: write.Access{Owner: "owner", AccountID: "account", CredentialRevision: 2, ExpiresAt: time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano), Entries: []file.Entry{{ID: "1"}}}}
	if err := s.SaveInspection(ctx, v); err != nil {
		t.Fatal(err)
	}
	a, b := v, v
	a.Access.Entries = []file.Entry{{ID: "2"}}
	b.Access.Entries = []file.Entry{{ID: "3"}}
	if err := s.UpdateInspection(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateInspection(ctx, b); err != nil {
		t.Fatal(err)
	}
	got, err := s.Inspection(ctx, v.ID)
	if err != nil || len(got.Access.Entries) != 3 || got.Access.ExpiresAt != v.Access.ExpiresAt {
		t.Fatal("concurrent expansion lost entries or renewed TTL", err)
	}
	if _, err = s.db.Exec("UPDATE share_inspections SET expires_at=0"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Inspection(ctx, v.ID); fault.As(err).Kind != fault.Gone {
		t.Fatal("expired inspection accepted", err)
	}
	m := operation.New("owner", "SHR01/account", "once", []string{"/test"})
	if _, replay, err := s.BeginBusinessMutation(ctx, m); err != nil || replay {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("UPDATE business_mutations SET expires_at=0"); err != nil {
		t.Fatal(err)
	}
	if err = s.CleanupBusiness(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.BeginBusinessMutation(ctx, m); fault.As(err).Code != "REMOTE_RESULT_UNKNOWN" {
		t.Fatal("unknown intent was replayed", err)
	}
	if err = s.FinishBusinessMutation(ctx, m, json.RawMessage(`{"share_id":"17"}`)); err != nil {
		t.Fatal(err)
	}
	if raw, replay, err := s.BeginBusinessMutation(ctx, m); err != nil || !replay || string(raw) != `{"share_id":"17"}` {
		t.Fatal("known result not replayed", err)
	}
}

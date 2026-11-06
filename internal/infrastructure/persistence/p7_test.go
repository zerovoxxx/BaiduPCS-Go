package persistence

import (
	"context"
	"encoding/json"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/operation"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/session"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/staging"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/infrastructure/baidu"
	"testing"
	"time"
)

func TestP7MigrationFromV6PreservesUnknownWriteCheckpointReceiptAndStage(t *testing.T) {
	s, dir, key := p2Store(t)
	ctx := context.Background()
	r := p2Record()
	r.Request.Kind = "file_copy"
	r.Plan = json.RawMessage(`{"AccountUK":"42","Units":[{"From":"/a","To":"/b"}]}`)
	if _, e := s.CreateTransfer(ctx, r); e != nil {
		t.Fatal(e)
	}
	s.TransitionTransfer(ctx, r.ID, []string{"queued"}, "running")
	cp := json.RawMessage(`{"Phase":"committing","Index":0}`)
	if e := s.SaveTransferCheckpoint(ctx, r.ID, cp); e != nil {
		t.Fatal(e)
	}
	m := operation.New(r.Owner, "JOB06/"+r.ID, "v6-control", "cancel")
	if _, e := s.ControlTransfer(ctx, r.ID, []string{"running"}, "interrupted", m); e != nil {
		t.Fatal(e)
	}
	intent := operation.New(r.Owner, "SHR01/account", "v6-unknown", []string{"/a"})
	if _, _, e := s.BeginBusinessMutation(ctx, intent); e != nil {
		t.Fatal(e)
	}
	stage := staging.File{ID: "stage_v6", Owner: r.Owner, Filename: "kept", Size: 3, State: "writing", ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)}
	if e := s.ReserveStage(ctx, stage); e != nil {
		t.Fatal(e)
	}
	stage.State = "ready"
	if _, e := s.FinishStage(ctx, stage, operation.Mutation{}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.db.Exec(`INSERT INTO staging_references VALUES('stage_v6','job_p2');DROP TABLE resources;PRAGMA user_version=6;`); e != nil {
		t.Fatal(e)
	}
	s.Close()
	next, e := Open(ctx, dir, key, baidu.DefaultConfiguration())
	if e != nil {
		t.Fatal(e)
	}
	defer next.Close()
	var version int
	next.db.QueryRow("PRAGMA user_version").Scan(&version)
	if version != 7 {
		t.Fatal(version)
	}
	got, e := next.Transfer(ctx, r.ID)
	if e != nil || got.State != "interrupted" || string(got.Checkpoint) != string(cp) || string(got.Plan) != string(r.Plan) {
		t.Fatal("v6 state changed", e)
	}
	receipt, e := next.ReplayControl(ctx, m)
	if e != nil || receipt == nil || receipt.State != "interrupted" {
		t.Fatal("control receipt lost", e)
	}
	if _, _, e = next.BeginBusinessMutation(ctx, intent); fault.As(e).Code != "REMOTE_RESULT_UNKNOWN" {
		t.Fatal("unknown intent replayed", e)
	}
	kept, e := next.Stage(ctx, stage.ID)
	if e != nil || kept.Size != 3 || kept.References != 1 {
		t.Fatal("stage lost", e)
	}
	if _, e = next.PutResource(ctx, session.Resource{Kind: "context", ID: "ctx_after_upgrade", Owner: r.Owner, Expires: time.Now().Add(time.Hour).Unix(), Data: json.RawMessage(`{}`)}, 0, operation.Mutation{}); e != nil {
		t.Fatal("v7 resource migration missing", e)
	}
}

func TestP7ResourceTombstoneCleanupIsBoundedAndKeepsActiveRows(t *testing.T) {
	s, _, _ := p2Store(t)
	ctx := context.Background()
	for _, v := range []struct {
		id      string
		expires int64
	}{{"active", time.Now().Add(time.Hour).Unix()}, {"recent", time.Now().Add(-time.Hour).Unix()}, {"old", time.Now().Add(-25 * time.Hour).Unix()}} {
		if _, e := s.PutResource(ctx, session.Resource{Kind: "context", ID: v.id, Owner: "owner", Expires: v.expires, Data: json.RawMessage(`{}`)}, 0, operation.Mutation{}); e != nil {
			t.Fatal(e)
		}
	}
	if e := s.CleanupResources(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Resource(ctx, "context", "old"); fault.As(e).Kind != fault.NotFound {
		t.Fatal("old tombstone kept", e)
	}
	for _, id := range []string{"active", "recent"} {
		if _, e := s.Resource(ctx, "context", id); e != nil {
			t.Fatal("live/recent removed", e)
		}
	}
}

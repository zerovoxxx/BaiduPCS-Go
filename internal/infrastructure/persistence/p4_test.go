package persistence

import (
	"context"
	"encoding/json"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/operation"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/infrastructure/baidu"
	"testing"
)

func TestP4MigrationFromV4PreservesCheckpointAndControlReceipt(t *testing.T) {
	s, dir, key := p2Store(t)
	ctx := context.Background()
	r := p2Record()
	r.Admission = operation.New(r.Owner, "UPL01/acct_p2", "p4-migration", r.Request)
	if _, err := s.CreateTransfer(ctx, r); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TransitionTransfer(ctx, r.ID, []string{"queued"}, "running"); err != nil {
		t.Fatal(err)
	}
	cp := json.RawMessage(`{"Version":1,"Phase":"uploading","Offset":1024}`)
	if err := s.SaveTransferCheckpoint(ctx, r.ID, cp); err != nil {
		t.Fatal(err)
	}
	m := operation.New(r.Owner, "JOB06/"+r.ID, "pause-key", "reason")
	if _, err := s.ControlTransfer(ctx, r.ID, []string{"running"}, "interrupted", m); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DROP TABLE staging_references;DROP TABLE staging_files;PRAGMA user_version=4;`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	upgraded, err := Open(ctx, dir, key, baidu.DefaultConfiguration())
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	got, err := upgraded.Transfer(ctx, r.ID)
	if err != nil || string(got.Checkpoint) != string(cp) || got.State != "interrupted" {
		t.Fatal("v4 record lost", err)
	}
	replay, err := upgraded.ReplayControl(ctx, m)
	if err != nil || replay == nil || replay.State != "interrupted" {
		t.Fatal("v4 control replay lost", err)
	}
}

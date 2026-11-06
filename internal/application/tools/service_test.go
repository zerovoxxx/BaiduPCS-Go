package tools

import (
	"context"
	"encoding/json"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/account"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/tool"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/transfer"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/infrastructure/baidu"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/infrastructure/persistence"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/ports"
	"os"
	"testing"
	"time"
)

type cancellingFiles struct {
	cancel context.CancelFunc
	calls  int
}

func (f *cancellingFiles) PrepareTool(context.Context, tool.Plan) error { return nil }
func (f *cancellingFiles) Checksum(ctx context.Context, _ tool.LocalFile, _ []string) (tool.Checksum, error) {
	f.calls++
	if f.calls == 2 {
		f.cancel()
		return tool.Checksum{}, ctx.Err()
	}
	return tool.Checksum{Size: "0", Status: "verified"}, nil
}
func (f *cancellingFiles) Transform(context.Context, tool.LocalFile, configuration.Directory, string, []byte, bool, bool, bool, func(json.RawMessage) error) (int64, error) {
	return 0, nil
}
func (f *cancellingFiles) CreateToolOutput(context.Context, configuration.Directory, string, func(json.RawMessage) error) (ports.ToolOutput, error) {
	return nil, nil
}
func (f *cancellingFiles) CleanupTool(context.Context, json.RawMessage) error { return nil }
func TestP6CancellationPersistsUnfinishedSelection(t *testing.T) {
	root := t.TempDir()
	os.Chmod(root, 0700)
	repo, e := persistence.Open(context.Background(), root, make([]byte, 32), baidu.DefaultConfiguration())
	if e != nil {
		t.Fatal(e)
	}
	defer repo.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	local := &cancellingFiles{cancel: cancel}
	s := New(nil, nil, repo, repo, repo, local, nil)
	p := tool.Plan{Units: []tool.Unit{{Local: tool.LocalFile{RootID: "root", Path: "first"}}, {Local: tool.LocalFile{RootID: "root", Path: "second"}}, {Local: tool.LocalFile{RootID: "root", Path: "third"}}}, Options: json.RawMessage(`{"algorithms":["md5"]}`)}
	raw, _ := json.Marshal(p)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	r := transfer.Record{ID: "job_tool_cancel", Owner: "owner", CreatedAt: now, UpdatedAt: now, Request: transfer.Request{Kind: "local_checksum", ToolPlan: string(raw)}, Config: baidu.DefaultConfiguration(), ConfigRevision: 1, EffectiveOptions: p.Options}
	if _, e = repo.CreateTransfer(context.Background(), r); e != nil {
		t.Fatal(e)
	}
	repo.TransitionTransfer(context.Background(), r.ID, []string{"queued"}, "running")
	if e = s.Execute(ctx, r, account.Credentials{}, func(json.RawMessage) error { return nil }); e != context.Canceled {
		t.Fatal("cancel ignored", e)
	}
	saved, e := repo.Transfer(context.Background(), r.ID)
	if e != nil {
		t.Fatal(e)
	}
	if saved.ScanResult == nil || saved.ScanResult.Complete || saved.ScanResult.Counts.Succeeded != 1 || saved.ScanResult.Counts.Cancelled != 2 || saved.ScanResult.Counts.Failed != 0 {
		t.Fatal("cancelled scope lost or misreported", saved.ScanResult)
	}
	items, e := repo.TransferItems(context.Background(), r.ID, 100)
	if e != nil || len(items) != 3 || items[1].State != "cancelled" || items[2].State != "cancelled" {
		t.Fatal("cancelled items lost", e)
	}
}

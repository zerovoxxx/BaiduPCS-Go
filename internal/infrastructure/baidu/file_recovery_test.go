package baidu

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/transfer"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/infrastructure/localfs"
)

func TestP2DownloadAllocationAndCommitFaultWindows(t *testing.T) {
	for _, window := range []string{"allocated-before-open", "linked-before-checkpoint", "changed-final-target"} {
		t.Run(window, func(t *testing.T) {
			f := newTransferFixture(t)
			a := f.account(t, "FILE_RECOVERY_TEST")
			body := bytes.Repeat([]byte("recovery"), 1000)
			f.upstream.mu.Lock()
			f.upstream.files["FILE_RECOVERY_TEST\x00/source.bin"] = body
			f.upstream.mu.Unlock()
			roots, err := localfs.New([]localfs.Root{{ID: "test", Path: f.root, Read: true, Write: true}}, f.directory)
			if err != nil {
				t.Fatal(err)
			}
			engine := NewSingleFileEngine(f.clients, roots)
			snapshot, err := f.store.Read(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			record := transfer.Record{ID: "job_fault_window", Request: transfer.Request{Kind: "download", AccountID: a.ID, RootID: "test", RelativePath: "output.bin", RemotePath: "/source.bin"}, Config: snapshot.Client(), ConfigRevision: snapshot.Revision(), CredentialRevision: 1}
			var saved json.RawMessage
			save := func(raw json.RawMessage) error {
				var cp fileCheckpoint
				if json.Unmarshal(raw, &cp) != nil {
					t.Fatal("bad checkpoint")
				}
				if window == "allocated-before-open" && cp.Phase == "allocating" {
					saved = append(saved[:0], raw...)
					// Reproduce death after creating a file but before the next checkpoint.
					if err := os.WriteFile(filepath.Join(f.root, cp.Temp), []byte("unconfirmed tail"), 0600); err != nil {
						t.Fatal(err)
					}
					return errors.New("injected allocation crash")
				}
				if window != "allocated-before-open" && cp.Phase == "done" {
					return errors.New("injected lost completion write")
				}
				saved = append(saved[:0], raw...)
				return nil
			}
			if err = engine.Execute(context.Background(), record, a.Credentials, save); err == nil {
				t.Fatal("fault window did not fail")
			}
			record.Checkpoint = append(json.RawMessage(nil), saved...)
			target := filepath.Join(f.root, "output.bin")
			if window == "changed-final-target" {
				if err = os.WriteFile(target, []byte("different file"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			err = engine.Execute(context.Background(), record, a.Credentials, func(raw json.RawMessage) error { saved = append(saved[:0], raw...); return nil })
			if window == "changed-final-target" {
				if fault.As(err).Code != "LOCAL_RESULT_UNKNOWN" {
					t.Fatal("changed final file was accepted", err)
				}
				data, _ := os.ReadFile(target)
				if string(data) != "different file" {
					t.Fatal("reconciliation overwrote a different target")
				}
				return
			}
			if err != nil {
				t.Fatal("recovery did not reconcile the fault window", err)
			}
			actual, err := os.ReadFile(target)
			if err != nil || !bytes.Equal(actual, body) {
				t.Fatal("fault recovery changed file bytes", err)
			}
			var cp fileCheckpoint
			json.Unmarshal(saved, &cp)
			if cp.Phase != "done" {
				t.Fatal("completion checkpoint not saved")
			}
		})
	}
}
func TestP2DownloadRootsAliasSameTargetAndCancelCleanup(t *testing.T) {
	f := newTransferFixture(t)
	a := f.account(t, "ALIAS_TEST")
	f.upstream.mu.Lock()
	f.upstream.files["ALIAS_TEST\x00/source.bin"] = []byte("content")
	f.upstream.mu.Unlock()
	roots, err := localfs.New([]localfs.Root{{ID: "one", Path: f.root, Read: true, Write: true}, {ID: "alias", Path: f.root, Read: true, Write: true}}, f.directory)
	if err != nil {
		t.Fatal(err)
	}
	engine := NewSingleFileEngine(f.clients, roots)
	snapshot, _ := f.store.Read(context.Background())
	record := transfer.Record{ID: "job_cleanup", State: "cancelled", Request: transfer.Request{Kind: "download", AccountID: a.ID, RootID: "one", RelativePath: "output.bin", RemotePath: "/source.bin"}, Config: snapshot.Client(), ConfigRevision: snapshot.Revision(), CredentialRevision: 1}
	first, err := engine.Prepare(context.Background(), record, a.Credentials)
	if err != nil {
		t.Fatal(err)
	}
	alias := record
	alias.Request.RootID = "alias"
	second, err := engine.Prepare(context.Background(), alias, a.Credentials)
	if err != nil || first != second {
		t.Fatal("root aliases bypass the physical destination lock", err)
	}
	cp := fileCheckpoint{Version: 1, Binding: binding(record), Phase: "downloading", Temp: ".baidupcs-job_cleanup-0123456789abcdef0123456789abcdef.part"}
	record.Checkpoint, _ = json.Marshal(cp)
	if err = os.WriteFile(filepath.Join(f.root, cp.Temp), []byte("owned"), 0600); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(f.root, ".baidupcs-another-job.part")
	if err = os.WriteFile(other, []byte("unowned"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = engine.CleanupTransfer(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(f.root, cp.Temp)); !os.IsNotExist(err) {
		t.Fatal("known cancelled allocation was retained")
	}
	if data, err := os.ReadFile(other); err != nil || string(data) != "unowned" {
		t.Fatal("cleanup removed an unowned file")
	}
}

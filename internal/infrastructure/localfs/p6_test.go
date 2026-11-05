package localfs

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/qjfoidnh/Baidu-Login/bdcrypto"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/operation"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/staging"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/tool"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/infrastructure/persistence"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func p6Local(t *testing.T) (*Registry, string) {
	t.Helper()
	root := t.TempDir()
	r, e := New([]Root{{ID: "local", Path: root, Read: true, Write: true}})
	if e != nil {
		t.Fatal(e)
	}
	return r, root
}
func TestP6CryptoModesLegacyCompatibilityAndFailureCleanup(t *testing.T) {
	r, root := p6Local(t)
	ctx := context.Background()
	key := []byte("compatible-key")
	input := bytes.Repeat([]byte("source-data\x00\xff"), 6000)
	os.WriteFile(filepath.Join(root, "source"), input, 0600)
	save := func(json.RawMessage) error { return nil }
	for _, bits := range []string{"128", "192", "256"} {
		for _, mode := range []string{"ctr", "cfb", "ofb"} {
			for _, zipped := range []bool{true, false} {
				name := bits + mode + map[bool]string{true: "gz", false: "raw"}[zipped]
				for _, d := range []string{name, name + "-plain"} {
					if e := r.MkdirAll(ctx, "local", d); e != nil {
						t.Fatal(e)
					}
				}
				dst := configuration.Directory{RootID: "local", Directory: name}
				if _, e := r.Transform(ctx, stagingLocal("source"), dst, "aes-"+bits+"-"+mode, key, zipped, false, true, save); e != nil {
					t.Fatal(name, e)
				}
				if _, e := r.Transform(ctx, stagingLocal(name+"/source.encrypt"), configuration.Directory{RootID: "local", Directory: name + "-plain"}, "aes-"+bits+"-"+mode, key, zipped, false, false, save); e != nil {
					t.Fatal(name, e)
				}
				got, _ := os.ReadFile(filepath.Join(root, name+"-plain/source"))
				if !bytes.Equal(got, input) {
					t.Fatal("mode roundtrip", name)
				}
			}
		}
	}
	// An actual legacy bdcrypto stream is accepted without touching the source.
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write(input)
	zw.Close()
	legacy, e := bdcrypto.Aes128CTREncrypt(bdcrypto.Convert16bytes(key), bytes.NewReader(gz.Bytes()))
	if e != nil {
		t.Fatal(e)
	}
	encoded, _ := io.ReadAll(legacy)
	os.WriteFile(filepath.Join(root, "legacy.encrypt"), encoded, 0600)
	r.MkdirAll(ctx, "local", "legacy-out")
	if _, e := r.Transform(ctx, stagingLocal("legacy.encrypt"), configuration.Directory{RootID: "local", Directory: "legacy-out"}, "aes-128-ctr", key, true, false, false, save); e != nil {
		t.Fatal(e)
	}
	got, _ := os.ReadFile(filepath.Join(root, "legacy-out/legacy"))
	if !bytes.Equal(got, input) {
		t.Fatal("legacy compatibility")
	}
	r.MkdirAll(ctx, "local", "bad-out")
	for _, tc := range []struct {
		name string
		key  []byte
	}{{"wrong-key", []byte("bad")}, {"damaged", key}} {
		b := append([]byte{}, encoded...)
		if tc.name == "damaged" {
			b[len(b)-1] ^= 1
		}
		os.WriteFile(filepath.Join(root, "bad.encrypt"), b, 0600)
		if _, e := r.Transform(ctx, stagingLocal("bad.encrypt"), configuration.Directory{RootID: "local", Directory: "bad-out"}, "aes-128-ctr", tc.key, true, true, false, save); fault.As(e).Code != "INVALID_CIPHERTEXT" {
			t.Fatal("corrupt input accepted", e)
		}
		if _, e := os.Stat(filepath.Join(root, "bad.encrypt")); e != nil {
			t.Fatal("failure deleted source")
		}
		entries, _ := os.ReadDir(filepath.Join(root, "bad-out"))
		if len(entries) != 0 {
			t.Fatal("failed plaintext leaked")
		}
	}
	if _, e := r.Transform(ctx, stagingLocal("source"), configuration.Directory{RootID: "local", Directory: "legacy-out"}, "aes-128-ctr", key, true, false, true, save); e != nil {
		t.Fatal(e)
	}
	if _, e := r.Transform(ctx, stagingLocal("source"), configuration.Directory{RootID: "local", Directory: "legacy-out"}, "aes-128-ctr", key, true, false, true, save); fault.As(e).Code != "TARGET_EXISTS" {
		t.Fatal("overwrote existing output", e)
	}
}

func stagingLocal(p string) tool.LocalFile { return tool.LocalFile{RootID: "local", Path: p} }
func TestP6CryptoDiskFullCancellationAndCommitReceipt(t *testing.T) {
	r, root := p6Local(t)
	os.WriteFile(filepath.Join(root, "source"), []byte("keep-source"), 0600)
	r.MkdirAll(context.Background(), "local", "output")
	dst := configuration.Directory{RootID: "local", Directory: "output"}
	save := func(json.RawMessage) error { return nil }
	r.toolCopy = func(w io.Writer, src io.Reader) (int64, error) {
		n, _ := w.Write([]byte("partial"))
		return int64(n), syscall.ENOSPC
	}
	if _, e := r.Transform(context.Background(), stagingLocal("source"), dst, "aes-128-ctr", []byte("key"), false, true, true, save); fault.As(e).Code != "LOCAL_IO_ERROR" {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Join(root, "source")); e != nil {
		t.Fatal("disk full removed source")
	}
	entries, _ := os.ReadDir(filepath.Join(root, "output"))
	if len(entries) != 0 {
		t.Fatal("disk-full temp leak")
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.toolCopy = func(w io.Writer, src io.Reader) (int64, error) { cancel(); _, e := io.Copy(w, src); return 0, e }
	if _, e := r.Transform(ctx, stagingLocal("source"), dst, "aes-128-ctr", []byte("key"), false, true, true, save); !errors.Is(e, context.Canceled) {
		t.Fatal("cancel ignored", e)
	}
	r.toolCopy = nil
	lostReceipt := func(cp json.RawMessage) error {
		if strings.Contains(string(cp), "done") {
			return syscall.ENOSPC
		}
		return nil
	}
	if _, e := r.Transform(context.Background(), stagingLocal("source"), dst, "aes-128-ctr", []byte("key"), false, true, true, lostReceipt); fault.As(e).Code != "LOCAL_RESULT_UNKNOWN" {
		t.Fatal("lost final receipt not uncertain", e)
	}
	if _, e := os.Stat(filepath.Join(root, "source")); e != nil {
		t.Fatal("receipt loss removed source")
	}
}
func p6Receive(t *testing.T) (*Receptions, *Staging, *persistence.Store, string) {
	t.Helper()
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	repo, e := persistence.Open(context.Background(), dir, make([]byte, 32), configuration.Client{AppID: 266719, CacheSize: 65536, MaxParallel: 1, MaxUploadParallel: 1, MaxDownloadLoad: 1, MaxUploadLoad: 1, PCSAddr: "pcs.baidu.com", EnableHTTPS: true, UploadPolicy: "skip"})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { repo.Close() })
	stage, e := NewStaging(dir, repo)
	if e != nil {
		t.Fatal(e)
	}
	return NewReceptions(stage, repo), stage, repo, dir
}
func TestP6ReceptionCASQuotaDigestDisconnectExpiryAndDiskFull(t *testing.T) {
	s, stage, repo, dir := p6Receive(t)
	ctx := context.Background()
	v, e := s.CreateReception(ctx, "owner", "file", 5, nil, "create")
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for n := 0; n < 2; n++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := s.PatchReception(ctx, v.ID, 0, strings.NewReader("he")); results <- e }()
	}
	wg.Wait()
	close(results)
	success, conflicts := 0, 0
	for e := range results {
		if e == nil {
			success++
		} else if fault.As(e).Code == "UPLOAD_OFFSET_MISMATCH" {
			conflicts++
		} else {
			t.Fatal(e)
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatal("PATCH CAS", success, conflicts)
	}
	stage.copyFile = func(w io.Writer, r io.Reader) (int64, error) { w.Write([]byte("bad")); return 3, syscall.ENOSPC }
	if _, e := s.PatchReception(ctx, v.ID, 2, strings.NewReader("llo")); e != syscall.ENOSPC {
		t.Fatal(e)
	}
	f, _ := os.Stat(filepath.Join(dir, "staging", v.StageID))
	if f.Size() != 2 {
		t.Fatal("disk failure advanced content")
	}
	stage.copyFile = nil
	if _, e := s.PatchReception(ctx, v.ID, 2, &failReader{}); e != io.ErrUnexpectedEOF {
		t.Fatal("disconnect hidden", e)
	}
	v, e = s.Reception(ctx, v.ID)
	if e != nil || v.Offset != 2 {
		t.Fatal("disconnect advanced offset", e)
	}
	if _, e := s.PatchReception(ctx, v.ID, 2, strings.NewReader("llo")); e != nil {
		t.Fatal(e)
	}
	finished, e := s.CompleteReception(ctx, v.ID)
	if e != nil {
		t.Fatal(e)
	}
	again, e := s.CompleteReception(ctx, v.ID)
	if e != nil || finished.ID != again.ID {
		t.Fatal("non-idempotent complete", e)
	}
	digest := md5.Sum([]byte("other"))
	bad, e := s.CreateReception(ctx, "owner", "bad", 1, &staging.Expected{Algorithm: "md5", Value: hex.EncodeToString(digest[:])}, "")
	if e != nil {
		t.Fatal(e)
	}
	s.PatchReception(ctx, bad.ID, 0, strings.NewReader("x"))
	if _, e = s.CompleteReception(ctx, bad.ID); fault.As(e).Code != "CHECKSUM_MISMATCH" {
		t.Fatal("mismatch published", e)
	}
	r, e := repo.Resource(ctx, "reception", bad.ID)
	if e != nil {
		t.Fatal(e)
	}
	r.Expires = time.Now().Add(-time.Second).Unix()
	if _, e = repo.PutResource(ctx, r, r.Revision, operation.Mutation{}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Reception(ctx, bad.ID); fault.As(e).Code != "RESOURCE_EXPIRED" {
		t.Fatal("expiry ignored", e)
	}
	if e = s.CleanupReceptions(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(dir, "staging", bad.StageID)); !os.IsNotExist(e) {
		t.Fatal("expired reception not cleaned")
	}
}

type failReader struct{ done bool }

func (r *failReader) Read(b []byte) (int, error) {
	if !r.done {
		r.done = true
		copy(b, []byte("l"))
		return 1, nil
	}
	return 0, io.ErrUnexpectedEOF
}
func TestP6ReceptionQuotaReservesIncompleteData(t *testing.T) {
	s, _, _, _ := p6Receive(t)
	ctx := context.Background()
	ids := []string{}
	for i := 0; i < 4; i++ {
		v, e := s.CreateReception(ctx, "owner", "quota", 1, nil, "")
		if e != nil {
			t.Fatal(e)
		}
		ids = append(ids, v.ID)
	}
	if _, e := s.CreateReception(ctx, "owner", "fifth", 1, nil, ""); fault.As(e).Code != "STAGING_QUOTA" {
		t.Fatal("incomplete receptions escaped quota", e)
	}
	if e := s.DeleteReception(ctx, ids[0]); e != nil {
		t.Fatal(e)
	}
	if _, e := s.CreateReception(ctx, "owner", "after-delete", 0, nil, ""); e != nil {
		t.Fatal("cancel did not release quota", e)
	}
}

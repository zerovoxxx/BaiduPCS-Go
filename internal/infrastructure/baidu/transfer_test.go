package baidu

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	appaccounts "github.com/qjfoidnh/BaiduPCS-Go/internal/application/accounts"
	apptransfers "github.com/qjfoidnh/BaiduPCS-Go/internal/application/transfers"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/account"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/auth"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/transfer"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/infrastructure/localfs"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/infrastructure/persistence"
)

type observedEngine struct {
	engine *SingleFileEngine
	t      *testing.T
}

func (o observedEngine) Plan(ctx context.Context, r transfer.Record, c account.Credentials) (json.RawMessage, error) {
	return o.engine.Plan(ctx, r, c)
}
func (o observedEngine) Prepare(ctx context.Context, r transfer.Record, c account.Credentials) (string, error) {
	return o.engine.Prepare(ctx, r, c)
}
func (o observedEngine) CleanupTransfer(ctx context.Context, r transfer.Record) error {
	return o.engine.CleanupTransfer(ctx, r)
}

func (o observedEngine) Execute(ctx context.Context, r transfer.Record, c account.Credentials, save func(json.RawMessage) error) error {
	err := o.engine.Execute(ctx, r, c, save)
	if err != nil && ctx.Err() == nil {
		o.t.Logf("controlled upstream execution: %v", err)
	}
	return err
}

type remoteUpload struct {
	owner, target string
	parts         map[int][]byte
}
type transferUpstream struct {
	mu           sync.Mutex
	files        map[string][]byte
	directories  map[string]bool
	rapidNine    bool
	badChecksum  bool
	expireUpload bool
	uploads      map[string]*remoteUpload
	parts        map[string]map[int]int
	blockedOnce  map[string]bool
	blocked      chan string
	resumed      chan int64
	creates      int
	dropCommit   bool
	holdCommit   bool
	committed    chan struct{}
}

func newTransferUpstream() *transferUpstream {
	return &transferUpstream{directories: map[string]bool{}, files: map[string][]byte{}, uploads: map[string]*remoteUpload{}, parts: map[string]map[int]int{}, blockedOnce: map[string]bool{}, blocked: make(chan string, 16), resumed: make(chan int64, 16)}
}
func digest(b []byte) string { s := md5.Sum(b); return hex.EncodeToString(s[:]) }
func (u *transferUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("BDUSS")
	if err != nil {
		http.Error(w, "missing isolated cookie", 401)
		return
	}
	owner := cookie.Value
	key := func(p string) string { return owner + "\x00" + p }
	respond := func(v any) { w.Header().Set("Content-Type", "application/json"); json.NewEncoder(w).Encode(v) }
	if r.URL.Query().Get("method") == "info" && strings.Contains(r.URL.Path, "quota") {
		respond(map[string]any{"quota": 1048576, "used": 0})
		return
	}
	if r.URL.Path == "/api/user/getinfo" {
		if r.URL.Query().Get("need_selfinfo") != "1" {
			http.Error(w, "need_selfinfo required", 400)
			return
		}
		respond(map[string]any{"errno": 0, "records": []any{map[string]any{"uk": 123, "uname": "test-user"}}})
		return
	}
	operation := r.URL.Query().Get("method")
	if operation == "" {
		operation = filepath.Base(r.URL.Path)
	}
	switch operation {
	case "meta":
		r.ParseMultipartForm(1 << 20)
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
		}
		var param struct {
			List []struct {
				Path string `json:"path"`
			} `json:"list"`
		}
		if json.Unmarshal([]byte(r.FormValue("param")), &param) != nil || len(param.List) != 1 {
			http.Error(w, "unexpected meta", 400)
			return
		}
		name := param.List[0].Path
		u.mu.Lock()
		body, ok := u.files[key(name)]
		isDir := u.directories[key(name)]
		u.mu.Unlock()
		if isDir {
			respond(map[string]any{"list": []any{map[string]any{"fs_id": 1, "path": name, "server_filename": filepath.Base(name), "size": 0, "isdir": 1}}})
			return
		}
		if !ok {
			respond(map[string]any{"error_code": 31066, "error_msg": "not found"})
			return
		}
		respond(map[string]any{"list": []any{map[string]any{"fs_id": 1, "path": name, "server_filename": filepath.Base(name), "size": len(body), "md5": digest(body), "isdir": 0, "mtime": 1700000000}}})
	case "mkdir":
		u.mu.Lock()
		u.directories[key(r.URL.Query().Get("path"))] = true
		u.mu.Unlock()
		respond(map[string]any{})
		return
	case "list":
		dir := r.URL.Query().Get("dir")
		if dir == "" {
			dir = r.URL.Query().Get("path")
		}
		list := []any{}
		u.mu.Lock()
		for k, b := range u.files {
			p := strings.TrimPrefix(k, owner+"\x00")
			if strings.HasPrefix(k, owner+"\x00") && path.Dir(p) == dir {
				list = append(list, map[string]any{"fs_id": len(list) + 1, "path": p, "server_filename": path.Base(p), "size": len(b), "md5": digest(b), "isdir": 0})
			}
		}
		for k := range u.directories {
			p := strings.TrimPrefix(k, owner+"\x00")
			if strings.HasPrefix(k, owner+"\x00") && path.Dir(p) == dir {
				list = append(list, map[string]any{"fs_id": len(list) + 1, "path": p, "server_filename": path.Base(p), "isdir": 1})
			}
		}
		u.mu.Unlock()
		respond(map[string]any{"errno": 0, "list": list})
		return
	case "locatedownload":
		respond(map[string]any{"urls": []any{map[string]any{"url": "https://pcs.baidu.com/rest/2.0/pcs/file?method=download&path=" + url.QueryEscape(r.URL.Query().Get("path")), "encrypt": 0}}})
		return
	case "precreate":
		r.ParseForm()
		target := r.Form.Get("path")
		u.mu.Lock()
		id := fmt.Sprint("up-", len(u.uploads)+1)
		u.uploads[id] = &remoteUpload{owner, target, map[int][]byte{}}
		u.mu.Unlock()
		if u.rapidNine && r.Form.Get("content-md5") != "" {
			respond(map[string]any{"errno": -9, "uploadid": id})
			return
		}
		respond(map[string]any{"errno": 0, "return_type": 1, "uploadid": id})
	case "upload":
		target := r.URL.Query().Get("path")
		part, _ := strconv.Atoi(r.URL.Query().Get("partseq"))
		reader, err := r.MultipartReader()
		if err != nil {
			http.Error(w, "multipart required", 400)
			return
		}
		partBody, err := reader.NextPart()
		if err != nil {
			http.Error(w, "file required", 400)
			return
		}
		body, err := io.ReadAll(partBody)
		if err != nil {
			return
		}
		if strings.Contains(target, "cancel") || strings.Contains(target, "pause") || (strings.Contains(target, "restart") && part == 1) {
			u.mu.Lock()
			seen := u.blockedOnce[key(target)]
			u.blockedOnce[key(target)] = true
			u.mu.Unlock()
			if strings.Contains(target, "cancel") || !seen {
				u.blocked <- target
				<-r.Context().Done()
				return
			}
		}
		u.mu.Lock()
		up := u.uploads[r.URL.Query().Get("uploadid")]
		if up == nil || up.owner != owner || up.target != target {
			u.mu.Unlock()
			http.Error(w, "wrong session owner", 403)
			return
		}
		up.parts[part] = body
		if u.parts[key(target)] == nil {
			u.parts[key(target)] = map[int]int{}
		}
		u.parts[key(target)][part]++
		u.mu.Unlock()
		if u.badChecksum {
			respond(map[string]any{"md5": digest([]byte("wrong"))})
			return
		}
		if u.expireUpload {
			u.mu.Lock()
			u.expireUpload = false
			u.mu.Unlock()
			respond(map[string]any{"error_code": 31363})
			return
		}
		respond(map[string]any{"md5": digest(body)})
	case "create":
		r.ParseForm()
		u.mu.Lock()
		up := u.uploads[r.Form.Get("uploadid")]
		if up == nil || up.owner != owner {
			u.mu.Unlock()
			http.Error(w, "wrong owner", 403)
			return
		}
		var checksums []string
		json.Unmarshal([]byte(r.Form.Get("block_list")), &checksums)
		var body []byte
		for i, checksum := range checksums {
			b := up.parts[i]
			if digest(b) != checksum {
				u.mu.Unlock()
				http.Error(w, "wrong block", 400)
				return
			}
			body = append(body, b...)
		}
		u.files[key(up.target)] = body
		u.creates++
		drop := u.dropCommit
		hold := u.holdCommit
		u.dropCommit = false
		u.mu.Unlock()
		if hold {
			u.committed <- struct{}{}
			<-r.Context().Done()
			return
		}
		if drop {
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
			return
		}
		respond(map[string]any{"errno": 0})
	case "download":
		u.mu.Lock()
		body, ok := u.files[key(r.URL.Query().Get("path"))]
		u.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		var offset int64
		last := int64(len(body) - 1)
		if header := r.Header.Get("Range"); header != "" {
			fmt.Sscanf(header, "bytes=%d-%d", &offset, &last)
			select {
			case u.resumed <- offset:
			default:
			}
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", offset, last, len(body)))
			w.Header().Set("Content-Length", strconv.FormatInt(last-offset+1, 10))
			w.WriteHeader(206)
		} else {
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		}
		if strings.Contains(r.URL.Query().Get("path"), "slow-proxy") {
			w.Write(body[offset : offset+1])
			w.(http.Flusher).Flush()
			u.blocked <- "slow-proxy"
			<-r.Context().Done()
			u.blocked <- "slow-proxy-closed"
			return
		}
		if strings.Contains(r.URL.Query().Get("path"), "download-restart") && offset == 0 {
			w.Write(body[:256<<10])
			w.(http.Flusher).Flush()
			u.blocked <- "download-restart"
			<-r.Context().Done()
			return
		}
		w.Write(body[offset : last+1])
	default:
		http.Error(w, "unexpected operation", 400)
	}
}

var transferAdmin = auth.Principal{ID: "admin", Scopes: []string{"admin"}, Accounts: []string{"*"}, Roots: []string{"*"}}

type transferFixture struct {
	directory, root string
	key             []byte
	upstream        *transferUpstream
	server          *httptest.Server
	pool            *x509.CertPool
	store           *persistence.Store
	clients         *ClientFactory
	service         *apptransfers.Service
}

func newTransferFixture(t *testing.T) *transferFixture {
	t.Helper()
	if !persistence.Supported() {
		t.Skip("service storage unsupported")
	}
	f := &transferFixture{directory: t.TempDir(), root: t.TempDir(), key: bytes.Repeat([]byte{3}, 32), upstream: newTransferUpstream()}
	os.Chmod(f.directory, 0700)
	f.server, f.pool = trustedServer(t, f.upstream)
	f.open(t)
	t.Cleanup(func() { f.close(t) })
	return f
}
func (f *transferFixture) open(t *testing.T) {
	t.Helper()
	var err error
	f.store, err = persistence.Open(context.Background(), f.directory, f.key, DefaultConfiguration())
	if err != nil {
		t.Fatal(err)
	}
	f.clients = connectedFactory(t, f.server, f.pool)
	roots, err := localfs.New([]localfs.Root{{ID: "test", Path: f.root, Read: true, Write: true}}, f.directory)
	if err != nil {
		t.Fatal(err)
	}
	engine := NewSingleFileEngine(f.clients, roots)
	engine.SetRepository(f.store)
	stage, err := localfs.NewStaging(f.directory, f.store)
	if err != nil {
		t.Fatal(err)
	}
	engine.SetStaging(stage)
	f.service, err = apptransfers.New(context.Background(), f.store, f.store, f.store, observedEngine{engine, t})
	if err != nil {
		t.Fatal(err)
	}
}
func (f *transferFixture) close(t *testing.T) {
	t.Helper()
	if f.service != nil {
		if err := f.service.Close(); err != nil {
			t.Error(err)
		}
		f.clients.Close()
		if err := f.store.Close(); err != nil {
			t.Error(err)
		}
		f.service = nil
	}
}
func (f *transferFixture) account(t *testing.T, secret string) account.Entry {
	t.Helper()
	a, err := appaccounts.New(f.store).Import(context.Background(), transferAdmin, "test", account.Import{Kind: "cookies", Cookies: "BDUSS=" + secret + "; STOKEN=stoken"})
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func (f *transferFixture) submit(t *testing.T, a account.Entry, kind, local, remote, key string) transfer.Record {
	t.Helper()
	r, err := f.service.Submit(context.Background(), transferAdmin, transfer.Request{Kind: kind, AccountID: a.ID, RootID: "test", RelativePath: local, RemotePath: remote}, key)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func (f *transferFixture) wait(t *testing.T, id, state string) transfer.Record {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		r, err := f.store.Transfer(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if r.State == state {
			return r
		}
		if r.Terminal() {
			t.Fatalf("job reached %s, wanted %s; checkpoint %s", r.State, state, r.Checkpoint)
		}
		time.Sleep(10 * time.Millisecond)
	}
	last, _ := f.store.Transfer(context.Background(), id)
	t.Fatalf("timed out waiting for job state: state=%s code=%s checkpoint=%s", last.State, last.FailureCode, last.Checkpoint)
	return transfer.Record{}
}
func blocked(t *testing.T, ch <-chan string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(8 * time.Second):
		t.Fatal("upstream did not reach cancellation point")
	}
}
func TestSingleFileTransferRoundTripAndCancellationIsolation(t *testing.T) {
	f := newTransferFixture(t)
	a := f.account(t, "account-A-secret")
	b := f.account(t, "account-B-secret")
	body := bytes.Repeat([]byte("real multi-uploader bytes\x00"), 10000)
	if err := os.WriteFile(filepath.Join(f.root, "source"), body, 0600); err != nil {
		t.Fatal(err)
	}
	cancelled := f.submit(t, a, "upload", "source", "/cancel.bin", "cancel")
	blocked(t, f.upstream.blocked)
	other := f.submit(t, b, "upload", "source", "/same.bin", "other")
	f.wait(t, other.ID, "succeeded")
	reader := auth.Principal{ID: "reader", Scopes: []string{"transfer", "local.read"}, Accounts: []string{a.ID}, Roots: []string{"test"}}
	if denied, err := f.service.Get(context.Background(), reader, other.ID); err == nil || denied.ID != "" {
		t.Fatal("cross-account task access accepted")
	}
	if _, err := f.service.Cancel(context.Background(), transferAdmin, cancelled.ID); err != nil {
		t.Fatal(err)
	}
	f.wait(t, cancelled.ID, "cancelled")
	next := f.submit(t, a, "upload", "source", "/same.bin", "next")
	f.wait(t, next.ID, "succeeded")
	f.upstream.mu.Lock()
	isolated := bytes.Equal(f.upstream.files["account-A-secret\x00/same.bin"], body) && bytes.Equal(f.upstream.files["account-B-secret\x00/same.bin"], body)
	f.upstream.mu.Unlock()
	if !isolated {
		t.Fatal("same-path transfers did not use both isolated accounts")
	}
	replay := f.submit(t, a, "upload", "source", "/same.bin", "next")
	if replay.ID != next.ID {
		t.Fatal("logical submission duplicated")
	}
	if _, err := f.service.Submit(context.Background(), transferAdmin, transfer.Request{Kind: "upload", AccountID: a.ID, RootID: "test", RelativePath: "source", RemotePath: "/different"}, "next"); err == nil {
		t.Fatal("key reuse accepted different input")
	}
	download := f.submit(t, a, "download", "result", "/same.bin", "")
	f.wait(t, download.ID, "succeeded")
	result, err := os.ReadFile(filepath.Join(f.root, "result"))
	if err != nil || !bytes.Equal(result, body) {
		t.Fatalf("round trip changed content: %v", err)
	}
	if _, err = f.service.Resume(context.Background(), transferAdmin, cancelled.ID); err == nil {
		t.Fatal("cancelled task revived")
	}
	for _, name := range []string{"service.db", "service.db-wal"} {
		data, _ := os.ReadFile(filepath.Join(f.directory, name))
		if bytes.Contains(data, []byte("account-A-secret")) || bytes.Contains(data, []byte("stoken")) {
			t.Fatal("credential stored in plaintext")
		}
	}
}
func TestSingleFileTransferRestartAndConfirmedRange(t *testing.T) {
	f := newTransferFixture(t)
	a := f.account(t, "restart-account-secret")
	body := bytes.Repeat([]byte("a"), (4<<20)+200000)
	os.WriteFile(filepath.Join(f.root, "source"), body, 0600)
	// The first block is confirmed. Block the second request, then stop the
	// service, close the database and rebuild every instance using the same dir.
	job := f.submit(t, a, "upload", "source", "/restart.bin", "")
	blocked(t, f.upstream.blocked)
	// Inject a persisted running state with no executor, as after process loss.
	if err := f.service.Close(); err != nil {
		t.Fatal(err)
	}
	if ok, err := f.store.TransitionTransfer(context.Background(), job.ID, []string{"interrupted"}, "running"); err != nil || !ok {
		t.Fatalf("inject running state: %v", err)
	}
	f.close(t)
	f.open(t)
	r, err := f.store.Transfer(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if r.State != "interrupted" {
		t.Fatalf("restart state %s", r.State)
	}
	if _, err = f.service.Resume(context.Background(), transferAdmin, job.ID); err != nil {
		t.Fatal(err)
	}
	f.wait(t, job.ID, "succeeded")
	f.upstream.mu.Lock()
	count := f.upstream.parts[a.Credentials.Cookies["BDUSS"]+"\x00/restart.bin"][0]
	f.upstream.files[a.Credentials.Cookies["BDUSS"]+"\x00/download-restart.bin"] = body
	f.upstream.mu.Unlock()
	if count != 1 {
		t.Fatalf("confirmed block uploaded %d times", count)
	}
	d := f.submit(t, a, "download", "resumed", "/download-restart.bin", "")
	blocked(t, f.upstream.blocked)
	deadline := time.Now().Add(5 * time.Second)
	var before fileCheckpoint
	for time.Now().Before(deadline) {
		r, _ := f.store.Transfer(context.Background(), d.ID)
		json.Unmarshal(r.Checkpoint, &before)
		if before.Offset > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if before.Offset == 0 {
		t.Fatal("download did not persist any confirmed offset")
	}
	f.close(t)
	f.open(t)
	if _, err = f.service.Resume(context.Background(), transferAdmin, d.ID); err != nil {
		t.Fatal(err)
	}
	f.wait(t, d.ID, "succeeded")
	select {
	case offset := <-f.upstream.resumed:
		if offset <= 0 {
			t.Fatal("download restarted from zero")
		}
	case <-time.After(time.Second):
		t.Fatal("no resumed range")
	}
	result, err := os.ReadFile(filepath.Join(f.root, "resumed"))
	if err != nil || !bytes.Equal(result, body) {
		t.Fatal("restart changed downloaded bytes")
	}
}

func TestSingleFileTransferRejectsChangedSourceAndCheckpointBinding(t *testing.T) {
	f := newTransferFixture(t)
	a := f.account(t, "source-binding-secret")
	os.WriteFile(filepath.Join(f.root, "source"), bytes.Repeat([]byte("a"), (4<<20)+200000), 0600)
	r := f.submit(t, a, "upload", "source", "/restart-source.bin", "")
	blocked(t, f.upstream.blocked)
	f.close(t)
	f.open(t)
	r, err := f.store.Transfer(context.Background(), r.ID)
	if err != nil {
		t.Fatal(err)
	}
	roots, err := localfs.New([]localfs.Root{{ID: "test", Path: f.root, Read: true, Write: true}}, f.directory)
	if err != nil {
		t.Fatal(err)
	}
	engine := NewSingleFileEngine(f.clients, roots)
	changed := r
	changed.Request.RemotePath = "/another-target.bin"
	err = engine.Execute(context.Background(), changed, a.Credentials, func(json.RawMessage) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "CHECKPOINT_BINDING_MISMATCH") {
		t.Fatalf("wrong-target checkpoint accepted: %v", err)
	}
	changed = r
	changed.CredentialRevision++
	credentials := a.Credentials
	credentials.Revision++
	err = engine.Execute(context.Background(), changed, credentials, func(json.RawMessage) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "CHECKPOINT_BINDING_MISMATCH") {
		t.Fatalf("wrong credential revision accepted: %v", err)
	}
	os.WriteFile(filepath.Join(f.root, "source"), []byte("changed source"), 0600)
	if _, err = f.service.Resume(context.Background(), transferAdmin, r.ID); err != nil {
		t.Fatal(err)
	}
	failed := f.wait(t, r.ID, "failed")
	if failed.FailureCode != "CHECKPOINT_SOURCE_CHANGED" {
		t.Fatalf("unexpected source rejection %s", failed.FailureCode)
	}
}

func TestSingleFileTransferReconcilesLostCommitResponse(t *testing.T) {
	f := newTransferFixture(t)
	a := f.account(t, "lost-response-secret")
	body := []byte("confirmed remote file, lost response")
	os.WriteFile(filepath.Join(f.root, "source"), body, 0600)
	f.upstream.mu.Lock()
	f.upstream.dropCommit = true
	f.upstream.mu.Unlock()
	r := f.submit(t, a, "upload", "source", "/lost-response.bin", "")
	f.wait(t, r.ID, "interrupted")
	f.close(t)
	f.open(t)
	if _, err := f.service.Resume(context.Background(), transferAdmin, r.ID); err != nil {
		t.Fatal(err)
	}
	f.wait(t, r.ID, "succeeded")
	f.upstream.mu.Lock()
	defer f.upstream.mu.Unlock()
	if f.upstream.creates != 1 || !bytes.Equal(f.upstream.files[a.Credentials.Cookies["BDUSS"]+"\x00/lost-response.bin"], body) {
		t.Fatal("unknown remote result was blindly replayed")
	}
}

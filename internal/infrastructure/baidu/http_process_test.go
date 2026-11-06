//go:build unix

package baidu

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	apptransfers "github.com/qjfoidnh/BaiduPCS-Go/internal/application/transfers"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/transfer"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/infrastructure/localfs"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/infrastructure/persistence"
)

type processGateStore struct {
	*persistence.Store
	mode, marker string
}

func (s processGateStore) gate(ctx context.Context, id string) error {
	data, _ := json.Marshal(map[string]string{"job_id": id})
	if err := os.WriteFile(s.marker, data, 0600); err != nil {
		return err
	}
	<-ctx.Done()
	return ctx.Err()
}
func (s processGateStore) CreateTransfer(ctx context.Context, r transfer.Record) (bool, error) {
	created, err := s.Store.CreateTransfer(ctx, r)
	if err == nil && created && s.mode == "admitted" {
		err = s.gate(ctx, r.ID)
	}
	return created, err
}
func (s processGateStore) SaveTransferCheckpoint(ctx context.Context, id string, cp json.RawMessage) error {
	if err := s.Store.SaveTransferCheckpoint(ctx, id, cp); err != nil {
		return err
	}
	if s.mode == "after-part" {
		var checkpoint fileCheckpoint
		json.Unmarshal(cp, &checkpoint)
		if checkpoint.Upload != nil {
			for _, block := range checkpoint.Upload.BlockList {
				if block.CheckSum != "" {
					return s.gate(ctx, id)
				}
			}
		}
	}
	return nil
}
func (s processGateStore) FinishTransfer(ctx context.Context, id, state, code string) error {
	if s.mode == "before-terminal" && state == "succeeded" {
		if err := s.gate(ctx, id); err != nil {
			return err
		}
	}
	return s.Store.FinishTransfer(ctx, id, state, code)
}

// A real child process runs the production HTTP handlers, account/transfer
// services, SQLite and TLS single-file engine. Gates inject only crash timing.
func TestP2ProcessHelper(t *testing.T) {
	if os.Getenv("BAIDUPCS_P2_CHILD") != "1" {
		return
	}
	directory, err := persistence.PrepareDirectory(os.Getenv("BAIDUPCS_P2_DATA"))
	if err != nil {
		t.Fatal(err)
	}
	lock, err := persistence.Lock(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	key, err := persistence.MasterKey(directory)
	if err != nil {
		t.Fatal(err)
	}
	store, err := persistence.Open(context.Background(), directory, key, DefaultConfiguration())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	cert, err := os.ReadFile(os.Getenv("BAIDUPCS_P2_CA"))
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(cert) {
		t.Fatal("invalid fixture CA")
	}
	address := os.Getenv("BAIDUPCS_P2_UPSTREAM")
	clients := NewClientFactoryWithTransport(func(configuration.Client) *http.Transport {
		return &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}, DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, address)
		}}
	})
	defer clients.Close()
	root := os.Getenv("BAIDUPCS_P2_ROOT")
	roots, err := localfs.New([]localfs.Root{{ID: "test", Path: root, Read: true, Write: true}}, directory)
	if err != nil {
		t.Fatal(err)
	}
	repo := processGateStore{Store: store, mode: os.Getenv("BAIDUPCS_P2_MODE"), marker: os.Getenv("BAIDUPCS_P2_MARKER")}
	service, err := apptransfers.New(context.Background(), repo, store, store, NewSingleFileEngine(clients, roots))
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	fixture := &transferFixture{directory: directory, root: root, store: store, clients: clients, service: service}
	server := &http.Server{Handler: testHTTPAPI(t, fixture), ReadHeaderTimeout: time.Second}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ready := os.Getenv("BAIDUPCS_P2_READY")
	if err = os.WriteFile(ready, []byte("http://"+listener.Addr().String()), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	<-ctx.Done()
	shutdown, release := context.WithTimeout(context.Background(), 3*time.Second)
	defer release()
	if err = server.Shutdown(shutdown); err != nil {
		_ = server.Close()
	}
	if err = service.CloseContext(shutdown); err != nil {
		t.Error(err)
	}
	clients.Close()
	if err = <-done; err != nil && err != http.ErrServerClosed {
		t.Error(err)
	}
}

type processLog struct {
	mu   sync.Mutex
	data bytes.Buffer
}

func (b *processLog) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data.Write(p)
}
func (b *processLog) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.data.String() }

type httpChild struct {
	cmd    *exec.Cmd
	url    string
	log    *processLog
	waited bool
}

func childProcess(t *testing.T, f *transferFixture, mode, marker string) *httpChild {
	t.Helper()
	ready := filepath.Join(t.TempDir(), "ready")
	cert := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.directory, "master.key")); os.IsNotExist(err) {
		if err = persistence.WritePrivateFile(filepath.Join(f.directory, "master.key"), f.key); err != nil {
			t.Fatal(err)
		}
	}
	child := &httpChild{cmd: exec.Command(os.Args[0], "-test.run=^TestP2ProcessHelper$", "-test.count=1"), log: &processLog{}}
	child.cmd.Env = append(os.Environ(), "BAIDUPCS_P2_CHILD=1", "BAIDUPCS_P2_DATA="+f.directory, "BAIDUPCS_P2_ROOT="+f.root, "BAIDUPCS_P2_UPSTREAM="+f.server.Listener.Addr().String(), "BAIDUPCS_P2_CA="+cert, "BAIDUPCS_P2_MODE="+mode, "BAIDUPCS_P2_MARKER="+marker, "BAIDUPCS_P2_READY="+ready)
	child.cmd.Stdout = child.log
	child.cmd.Stderr = child.log
	if err := child.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !child.waited {
			_ = child.cmd.Process.Kill()
			_ = child.cmd.Wait()
			child.waited = true
		}
	})
	until := time.Now().Add(10 * time.Second)
	for time.Now().Before(until) {
		if b, err := os.ReadFile(ready); err == nil {
			child.url = string(b)
			return child
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("child did not become ready: ", child.log.String())
	return nil
}
func processRequest(t *testing.T, base, method, path, key, body string, want int) map[string]any {
	t.Helper()
	req, err := http.NewRequest(method, base+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+httpTestToken)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	client := http.Client{Timeout: 15 * time.Second}
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	b, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != want {
		t.Fatalf("process HTTP %s %s: %d != %d: %s", method, path, response.StatusCode, want, b)
	}
	var result struct {
		Data map[string]any `json:"data"`
	}
	if err = json.Unmarshal(b, &result); err != nil {
		t.Fatal(err)
	}
	return result.Data
}
func markerID(t *testing.T, name string) string {
	t.Helper()
	until := time.Now().Add(10 * time.Second)
	for time.Now().Before(until) {
		if b, err := os.ReadFile(name); err == nil {
			var m map[string]string
			if json.Unmarshal(b, &m) == nil {
				return m["job_id"]
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("crash boundary not reached")
	return ""
}
func waitProcess(t *testing.T, c *httpChild, id, state string) {
	t.Helper()
	until := time.Now().Add(12 * time.Second)
	for time.Now().Before(until) {
		data := processRequest(t, c.url, "GET", "/api/v1/jobs/"+id, "", "", 200)
		if data["state"] == state {
			return
		}
		if data["state"] == "failed" {
			t.Fatalf("restored job failed: %v", data)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("restored job did not reach %s", state)
}
func killChild(t *testing.T, c *httpChild) {
	t.Helper()
	if err := c.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	err := c.cmd.Wait()
	c.waited = true
	if err == nil {
		t.Fatal("SIGKILL unexpectedly exited normally")
	}
}
func stopChild(t *testing.T, c *httpChild) {
	t.Helper()
	start := time.Now()
	if err := c.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- c.cmd.Wait() }()
	select {
	case err := <-done:
		c.waited = true
		if err != nil {
			t.Fatal("SIGTERM child failed", err, c.log.String())
		}
	case <-time.After(6 * time.Second):
		t.Fatal("SIGTERM did not drain in time")
	}
	if time.Since(start) > 6*time.Second {
		t.Fatal("unbounded shutdown")
	}
}

func TestP2HTTPProcessSIGKILLRecoveryBoundaries(t *testing.T) {
	for _, mode := range []string{"admitted", "after-part", "after-commit", "before-terminal"} {
		t.Run(mode, func(t *testing.T) {
			f := newTransferFixture(t)
			f.close(t)
			marker := filepath.Join(t.TempDir(), "checkpoint.json")
			if mode == "after-commit" {
				f.upstream.mu.Lock()
				f.upstream.holdCommit = true
				f.upstream.committed = make(chan struct{}, 1)
				f.upstream.mu.Unlock()
			}
			source := bytes.Repeat([]byte{0x57}, 5<<20)
			if err := os.WriteFile(filepath.Join(f.root, "source.bin"), source, 0600); err != nil {
				t.Fatal(err)
			}
			child := childProcess(t, f, mode, marker)
			account := processRequest(t, child.url, "POST", "/api/v1/accounts", "account", `{"credentials":{"kind":"bduss","bduss":"PROCESS_TEST"}}`, 201)
			accountID := account["account_id"].(string)
			body := `{"sources":[{"type":"local","root_id":"test","relative_path":"source.bin"}],"target_dir":"/crash",` + uploadOptions + `}`
			var id string
			disconnected := make(chan struct{})
			if mode == "admitted" {
				go func() {
					defer close(disconnected)
					req, _ := http.NewRequest("POST", child.url+"/api/v1/accounts/"+accountID+"/uploads", strings.NewReader(body))
					req.Header.Set("Authorization", "Bearer "+httpTestToken)
					req.Header.Set("Content-Type", "application/json")
					req.Header.Set("Idempotency-Key", "submit")
					client := http.Client{Timeout: 15 * time.Second}
					resp, _ := client.Do(req)
					if resp != nil {
						resp.Body.Close()
					}
				}()
				id = markerID(t, marker)
			} else {
				job := processRequest(t, child.url, "POST", "/api/v1/accounts/"+accountID+"/uploads", "submit", body, 202)
				id = job["job_id"].(string)
				if mode == "after-commit" {
					select {
					case <-f.upstream.committed:
					case <-time.After(10 * time.Second):
						t.Fatal("upstream did not commit")
					}
				} else {
					if markerID(t, marker) != id {
						t.Fatal("wrong gated job")
					}
				}
			}
			killChild(t, child)
			if mode == "admitted" {
				select {
				case <-disconnected:
				case <-time.After(3 * time.Second):
					t.Fatal("submission connection leaked")
				}
			}
			f.upstream.mu.Lock()
			f.upstream.holdCommit = false
			f.upstream.mu.Unlock()
			restored := childProcess(t, f, "", filepath.Join(t.TempDir(), "unused"))
			if mode != "admitted" {
				waitProcess(t, restored, id, "interrupted")
				if mode == "after-commit" || mode == "before-terminal" {
					processRequest(t, restored.url, "POST", "/api/v1/jobs/"+id+"/cancel", "", `{}`, 409)
				}
				processRequest(t, restored.url, "POST", "/api/v1/jobs/"+id+"/resume", "resume", `{}`, 202)
			}
			waitProcess(t, restored, id, "succeeded")
			processRequest(t, restored.url, "POST", "/api/v1/accounts/"+accountID+"/uploads", "submit", body, 202)
			f.upstream.mu.Lock()
			got := append([]byte(nil), f.upstream.files["PROCESS_TEST\x00/crash/source.bin"]...)
			parts := f.upstream.parts["PROCESS_TEST\x00/crash/source.bin"][0]
			creates := f.upstream.creates
			f.upstream.mu.Unlock()
			if !bytes.Equal(got, source) || parts != 1 || creates != 1 {
				t.Fatalf("recovery replayed confirmed effects: bytes=%v part0=%d creates=%d", bytes.Equal(got, source), parts, creates)
			}
			stopChild(t, restored)
		})
	}
}
func TestP2HTTPProcessSIGTERMInterruptsActiveTransfer(t *testing.T) {
	f := newTransferFixture(t)
	f.close(t)
	if err := os.WriteFile(filepath.Join(f.root, "file.bin"), []byte("shutdown"), 0600); err != nil {
		t.Fatal(err)
	}
	child := childProcess(t, f, "", filepath.Join(t.TempDir(), "unused"))
	account := processRequest(t, child.url, "POST", "/api/v1/accounts", "", `{"credentials":{"kind":"bduss","bduss":"PROCESS_TEST"}}`, 201)
	body := `{"sources":[{"type":"local","root_id":"test","relative_path":"file.bin"}],"target_dir":"/cancel-process",` + uploadOptions + `}`
	job := processRequest(t, child.url, "POST", fmt.Sprintf("/api/v1/accounts/%s/uploads", account["account_id"]), "", body, 202)
	id := job["job_id"].(string)
	select {
	case <-f.upstream.blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("slow upstream not entered")
	}
	stopChild(t, child)
	restored := childProcess(t, f, "", filepath.Join(t.TempDir(), "unused"))
	waitProcess(t, restored, id, "interrupted")
	processRequest(t, restored.url, "POST", "/api/v1/jobs/"+id+"/cancel", "", `{}`, 202)
	waitProcess(t, restored, id, "cancelled")
	stopChild(t, restored)
}

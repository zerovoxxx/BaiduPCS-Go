package bootstrap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/infrastructure/identity"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/infrastructure/persistence"
)

const testToken = "P1_EXAMPLE_TEST_TOKEN_0123456789abcdef"
const readToken = "P1_READ_ONLY_TEST_TOKEN_0123456789abcdef"

type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}
func (b *lockedBuffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.buffer.String() }
func testOptions(t *testing.T) (Options, *lockedBuffer) {
	t.Helper()
	if !persistence.Supported() {
		t.Skip("server is not enabled on this target")
	}
	sum := sha256.Sum256([]byte(readToken))
	read := identity.Record{ID: "reader", TokenSHA256: hex.EncodeToString(sum[:]), Scopes: []string{"read"}, Accounts: []string{"account_a"}, Roots: []string{}}
	directory := t.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	cfg := startup{Listen: "127.0.0.1:0", DataDir: directory, Principals: []identity.Record{adminRecord(testToken), read}}
	encoded, _ := json.Marshal(cfg)
	configFile := filepath.Join(t.TempDir(), "server.json")
	if err := os.WriteFile(configFile, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	logs := &lockedBuffer{}
	return Options{ConfigFile: configFile, Version: "p1-test", Logger: slog.New(slog.NewJSONHandler(logs, nil))}, logs
}
func testRuntime(t *testing.T) (*Runtime, *httptest.Server, *lockedBuffer) {
	t.Helper()
	options, logs := testOptions(t)
	runtime, err := New(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(runtime.Handler)
	t.Cleanup(func() {
		server.Close()
		if err := runtime.Close(); err != nil {
			t.Error(err)
		}
	})
	return runtime, server, logs
}
func request(t *testing.T, handler http.Handler, method, path, token, etag, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if etag != "" {
		req.Header.Set("If-Match", etag)
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}
func data(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var response struct {
		Data map[string]any `json:"data"`
		Meta struct {
			ID string `json:"request_id"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("bad response %d %s", rec.Code, rec.Body.String())
	}
	if response.Meta.ID != rec.Header().Get("X-Request-ID") {
		t.Fatal("request ID mismatch")
	}
	return response.Data
}
func wantStatus(t *testing.T, rec *httptest.ResponseRecorder, status int) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status %d want %d: %s", rec.Code, status, rec.Body.String())
	}
}

func TestRuntimeHTTPConfigurationAndAuth(t *testing.T) {
	runtime, _, logs := testRuntime(t)
	h := runtime.Handler
	wantStatus(t, request(t, h, "GET", "/healthz", "", "", "", ""), 200)
	wantStatus(t, request(t, h, "GET", "/readyz", "", "", "", ""), 200)
	unauthorized := request(t, h, "GET", "/api/v1/system", "", "", "", "")
	wantStatus(t, unauthorized, 401)
	if unauthorized.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Fatal("missing challenge")
	}
	wantStatus(t, request(t, h, "GET", "/api/v1/configuration", readToken, "", "", ""), 403)
	wantStatus(t, request(t, h, "GET", "/api/v1/accounts/account_b/files", readToken, "", "", ""), 403)
	wantStatus(t, request(t, h, "GET", "/api/v1/accounts/account_a/files", readToken, "", "", ""), 501)
	wantStatus(t, request(t, h, "GET", "/unknown", testToken, "", "", ""), 404)
	snapshot, err := runtime.store.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	initial := request(t, h, "GET", "/api/v1/configuration", testToken, "", "", "")
	wantStatus(t, initial, 200)
	if initial.Header().Get("ETag") != `"1"` || data(t, initial)["client"].(map[string]any)["no_check"] != true {
		t.Fatal("wrong defaults")
	}
	body := `{"max_parallel":7,"no_check":false,"ignore_illegal":false,"max_download_rate":"0","proxy":"http://p1user:DO_NOT_LOG_PROXY_SECRET@127.0.0.1:3128"}`
	updated := request(t, h, "PATCH", "/api/v1/configuration", testToken, `"1"`, "config-once", body)
	wantStatus(t, updated, 200)
	updatedData := data(t, updated)["configuration"].(map[string]any)
	client := updatedData["client"].(map[string]any)
	if updatedData["revision"] != "2" || client["no_check"] != false || client["ignore_illegal"] != false || client["max_download_rate"] != "0" || client["max_parallel"] != float64(7) {
		t.Fatalf("lost explicit values: %#v", client)
	}
	if snapshot.Revision() != 1 || snapshot.Client().MaxParallel != 1 || !snapshot.Client().NoCheck {
		t.Fatal("existing snapshot mutated")
	}
	if strings.Contains(updated.Body.String(), "DO_NOT_LOG_PROXY_SECRET") || strings.Contains(updated.Body.String(), "p1user") {
		t.Fatal("proxy secret in response")
	}
	wantStatus(t, request(t, h, "PATCH", "/api/v1/configuration", testToken, "", "", `{"no_check":true}`), 428)
	wantStatus(t, request(t, h, "PATCH", "/api/v1/configuration", testToken, `"1"`, "", `{"no_check":true}`), 412)
	replay := request(t, h, "PATCH", "/api/v1/configuration", testToken, `"1"`, "config-once", body)
	wantStatus(t, replay, 200)
	if data(t, replay)["configuration"].(map[string]any)["revision"] != "2" {
		t.Fatal("idempotency did not replay original version")
	}
	wantStatus(t, request(t, h, "PATCH", "/api/v1/configuration", testToken, `"1"`, "config-once", `{"no_check":true}`), 409)
	candidate := request(t, h, "POST", "/api/v1/configuration/validate", testToken, "", "", `{"cache_size":"1"}`)
	wantStatus(t, candidate, 200)
	if data(t, candidate)["valid"] != false {
		t.Fatal("invalid candidate reported valid")
	}
	candidate = request(t, h, "POST", "/api/v1/configuration/validate", testToken, "", "", `{"no_check":true}`)
	wantStatus(t, candidate, 200)
	if data(t, candidate)["valid"] != true {
		t.Fatal("valid candidate rejected")
	}
	state, _ := runtime.store.Read(context.Background())
	if state.Revision() != 2 {
		t.Fatal("validation persisted a revision")
	}
	reset := request(t, h, "POST", "/api/v1/configuration/reset", testToken, `"2"`, "reset-once", `{"fields":["no_check","proxy"]}`)
	wantStatus(t, reset, 200)
	resetClient := data(t, reset)["configuration"].(map[string]any)["client"].(map[string]any)
	if resetClient["no_check"] != true || resetClient["proxy"].(map[string]any)["configured"] != false {
		t.Fatal("reset failed")
	}
	replay = request(t, h, "PATCH", "/api/v1/configuration", testToken, `"1"`, "config-once", body)
	wantStatus(t, replay, 200)
	if replay.Header().Get("ETag") != `"2"` {
		t.Fatal("replay followed current revision instead of original")
	}
	for _, path := range []string{"/api/v1/configuration/schema", "/api/v1/configuration/defaults", "/api/v1/system", "/api/v1/capabilities", "/api/v1/openapi.json", "/docs"} {
		wantStatus(t, request(t, h, "GET", path, testToken, "", "", ""), 200)
	}
	schema := data(t, request(t, h, "GET", "/api/v1/configuration/schema", testToken, "", "", ""))
	if len(schema["fields"].([]any)) != 22 {
		t.Fatal("missing configuration fields")
	}
	for _, forbidden := range []string{testToken, "DO_NOT_LOG_PROXY_SECRET", "p1user", "Authorization", "token="} {
		if strings.Contains(logs.String(), forbidden) {
			t.Fatalf("secret/raw request in logs: %s", forbidden)
		}
	}
}

func TestHTTPRejectsMalformedInputsAndHeadBodies(t *testing.T) {
	runtime, _, _ := testRuntime(t)
	cases := []struct {
		body   string
		status int
	}{
		{`{"no_check":null}`, 400}, {`{"unknown":1}`, 400}, {`{"no_check":false,"no_check":true}`, 400}, {`{"savedir":{"root_id":"root"}}`, 400}, {`{"cache_size":"01"}`, 400}, {`{"max_parallel":0}`, 400}, {`{"max_parallel":1.0}`, 400}, {`{}`, 400}, {`{  }`, 400}, {`[]`, 400}, {`{"no_check":false} {}`, 400}, {`{"cache_size":"1"}`, 422}, {`{"proxy":"not a proxy URL"}`, 422}, {`{"user_agent":"` + strings.Repeat("x", 1<<20) + `"}`, 413},
	}
	for i, tc := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			wantStatus(t, request(t, runtime.Handler, "PATCH", "/api/v1/configuration", testToken, `"1"`, "", tc.body), tc.status)
		})
	}
	req := httptest.NewRequest("PATCH", "/api/v1/configuration", strings.NewReader(`{"no_check":false}`))
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("If-Match", `"1"`)
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(rec, req)
	wantStatus(t, rec, 415)
	req = httptest.NewRequest("GET", "/api/v1/system", nil)
	req.Header.Add("Authorization", "Bearer "+testToken)
	req.Header.Add("Authorization", "Bearer "+testToken)
	rec = httptest.NewRecorder()
	runtime.Handler.ServeHTTP(rec, req)
	wantStatus(t, rec, 401)
	head := request(t, runtime.Handler, "HEAD", "/api/v1/accounts/account_a/content", testToken, "", "", "")
	wantStatus(t, head, 501)
	if head.Body.Len() != 0 {
		t.Fatal("HEAD error has body")
	}
	head = request(t, runtime.Handler, "HEAD", "/api/v1/system", testToken, "", "", "")
	wantStatus(t, head, 405)
	if head.Body.Len() != 0 || head.Header().Get("Allow") != "GET" {
		t.Fatal("invalid method/HEAD behavior")
	}
	state, _ := runtime.store.Read(context.Background())
	if state.Revision() != 1 {
		t.Fatal("invalid request partially persisted")
	}
}

func TestConcurrentCASAndIdempotency(t *testing.T) {
	runtime, _, _ := testRuntime(t)
	var group sync.WaitGroup
	statuses := make(chan int, 16)
	for i := 0; i < 16; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			rec := request(t, runtime.Handler, "PATCH", "/api/v1/configuration", testToken, `"1"`, "concurrent-key", `{"max_parallel":9}`)
			statuses <- rec.Code
		}()
	}
	group.Wait()
	close(statuses)
	for status := range statuses {
		if status != 200 {
			t.Fatalf("same-key race returned %d", status)
		}
	}
	state, _ := runtime.store.Read(context.Background())
	if state.Revision() != 2 {
		t.Fatalf("duplicate mutation: revision=%d", state.Revision())
	}
	statuses = make(chan int, 12)
	for i := 0; i < 12; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			rec := request(t, runtime.Handler, "PATCH", "/api/v1/configuration", testToken, `"2"`, "", fmt.Sprintf(`{"max_parallel":%d}`, i+1))
			statuses <- rec.Code
		}(i)
	}
	group.Wait()
	close(statuses)
	success, conflict := 0, 0
	for status := range statuses {
		switch status {
		case 200:
			success++
		case 412:
			conflict++
		default:
			t.Fatalf("CAS race returned %d", status)
		}
	}
	if success != 1 || conflict != 11 {
		t.Fatalf("CAS success=%d conflict=%d", success, conflict)
	}
}

func TestPersistenceRestartLockAndGracefulShutdown(t *testing.T) {
	options, _ := testOptions(t)
	runtime, err := New(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(context.Background(), options); err == nil {
		runtime.Close()
		t.Fatal("second service acquired same data directory")
	}
	secret := "DO_NOT_STORE_PLAINTEXT_PROXY"
	rec := request(t, runtime.Handler, "PATCH", "/api/v1/configuration", testToken, `"1"`, "restart-key", `{"proxy":"http://user:`+secret+`@127.0.0.1:3128","no_check":false}`)
	wantStatus(t, rec, 200)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtime.Serve(ctx, listener) }()
	client := &http.Client{Timeout: time.Second}
	response, err := client.Get("http://" + listener.Addr().String() + "/healthz")
	if err != nil {
		cancel()
		runtime.Close()
		t.Fatal(err)
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown did not drain")
	}
	contents, err := os.ReadFile(filepath.Join(runtime.DataDir, "service.db"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(contents, []byte(secret)) {
		t.Fatal("plaintext secret in SQLite database")
	}
	restarted, err := New(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	state, err := restarted.store.Read(context.Background())
	if err != nil || state.Revision() != 2 || state.Client().NoCheck || !strings.Contains(state.Client().Proxy, secret) {
		t.Fatalf("restart lost config: rev=%d err=%v", state.Revision(), err)
	}
	replay := request(t, restarted.Handler, "PATCH", "/api/v1/configuration", testToken, `"1"`, "restart-key", `{"proxy":"http://user:`+secret+`@127.0.0.1:3128","no_check":false}`)
	wantStatus(t, replay, 200)
	if replay.Header().Get("ETag") != `"2"` {
		t.Fatal("restart lost idempotency record")
	}
}

func TestDefaultIdentityKeepsOnlyHashInAuthority(t *testing.T) {
	if !persistence.Supported() {
		t.Skip("server unsupported")
	}
	directory := t.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	runtime, err := New(context.Background(), Options{DataDir: directory, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	handoff, err := persistence.ReadPrivateFile(runtime.CredentialFile, 513)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := os.ReadFile(filepath.Join(directory, "principals.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(authority, bytes.TrimSpace(handoff)) {
		t.Fatal("authority stored token plaintext")
	}
	wantStatus(t, request(t, runtime.Handler, "GET", "/api/v1/system", strings.TrimSpace(string(handoff)), "", "", ""), 200)
	if err = runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(directory, "client-token")); err != nil {
		t.Fatal(err)
	}
	runtime, err = New(context.Background(), Options{DataDir: directory, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if runtime.CredentialFile != "" {
		t.Fatal("recreated a handoff/rotated token during restart")
	}
	wantStatus(t, request(t, runtime.Handler, "GET", "/api/v1/system", strings.TrimSpace(string(handoff)), "", "", ""), 200)
}

// This produces actual response artifacts for the Python OpenAPI checker only
// when an explicit temporary output path is supplied by the developer/CI.
func TestLiveContractResponses(t *testing.T) {
	runtime, _, _ := testRuntime(t)
	cases := []struct {
		id, method, path, token, etag, body string
		status                              int
	}{
		{"SYS01", "GET", "/healthz", "", "", "", 200},
		{"SYS02", "GET", "/readyz", "", "", "", 200},
		{"SYS03", "GET", "/api/v1/system", testToken, "", "", 200},
		{"SYS04", "GET", "/api/v1/capabilities", testToken, "", "", 200},
		{"SYS08", "GET", "/api/v1/openapi.json", testToken, "", "", 200},
		{"SYS09", "GET", "/docs", testToken, "", "", 200},
		{"CFG01", "GET", "/api/v1/configuration", testToken, "", "", 200},
		{"CFG03", "GET", "/api/v1/configuration/schema", testToken, "", "", 200},
		{"CFG04", "POST", "/api/v1/configuration/validate", testToken, "", `{"cache_size":"1"}`, 200},
		{"CFG07", "GET", "/api/v1/configuration/defaults", testToken, "", "", 200},
		{"CFG02", "PATCH", "/api/v1/configuration", testToken, `"1"`, `{"no_check":false,"proxy":""}`, 200},
		{"CFG05", "POST", "/api/v1/configuration/reset", testToken, `"2"`, `{"fields":["no_check"]}`, 200},
		{"CFG01", "GET", "/api/v1/configuration", "", "", "", 401},
		{"CFG01", "GET", "/api/v1/configuration", readToken, "", "", 403},
		{"CFG02", "PATCH", "/api/v1/configuration", testToken, "", `{"no_check":false}`, 428},
		{"CFG02", "PATCH", "/api/v1/configuration", testToken, `"1"`, `{"no_check":false}`, 412},
		{"CFG02", "PATCH", "/api/v1/configuration", testToken, `"3"`, `{"no_check":null}`, 400},
		{"FIL01", "GET", "/api/v1/accounts/account_a/files", readToken, "", "", 501},
	}
	type row struct {
		OperationID string      `json:"operation_id"`
		Method      string      `json:"method"`
		Status      int         `json:"status"`
		ContentType string      `json:"content_type"`
		Headers     http.Header `json:"headers"`
		Body        any         `json:"body"`
	}
	rows := []row{}
	for _, tc := range cases {
		rec := request(t, runtime.Handler, tc.method, tc.path, tc.token, tc.etag, "", tc.body)
		wantStatus(t, rec, tc.status)
		media := strings.Split(rec.Header().Get("Content-Type"), ";")[0]
		var body any = rec.Body.String()
		if strings.Contains(media, "json") {
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
		}
		rows = append(rows, row{OperationID: tc.id, Method: tc.method, Status: rec.Code, ContentType: media, Headers: rec.Header().Clone(), Body: body})
	}
	if path := os.Getenv("BAIDUPCS_P1_CONTRACT_RESPONSES"); path != "" {
		encoded, err := json.MarshalIndent(rows, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, append(encoded, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

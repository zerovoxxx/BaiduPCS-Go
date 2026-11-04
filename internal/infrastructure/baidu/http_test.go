package baidu

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	contract "github.com/qjfoidnh/BaiduPCS-Go/api/openapi"
	appaccounts "github.com/qjfoidnh/BaiduPCS-Go/internal/application/accounts"
	appauth "github.com/qjfoidnh/BaiduPCS-Go/internal/application/auth"
	appconfig "github.com/qjfoidnh/BaiduPCS-Go/internal/application/configuration"
	appsystem "github.com/qjfoidnh/BaiduPCS-Go/internal/application/system"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/httpapi"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/infrastructure/identity"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/infrastructure/localfs"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/infrastructure/network"
)

const httpTestToken = "P2_EXAMPLE_TOKEN_0123456789abcdefghijkl"
const uploadOptions = `"options":{"file_concurrency":1,"parallel_per_file":1,"max_retries":0,"policy":"skip","skip_rapid_check":true,"check_filename":false,"no_split_file":false,"max_rate_bytes_per_second":"0"}`
const downloadOptions = `"options":{"mode":"pcs","path_mode":"relative","parallel":1,"file_concurrency":1,"max_retries":0,"overwrite":false,"verify_checksum":true,"preserve_mtime":false,"executable":false,"link_index":0,"test":false,"report_workers":false,"max_rate_bytes_per_second":"0"}`

type liveResponse struct {
	OperationID string      `json:"operation_id"`
	Method      string      `json:"method"`
	Status      int         `json:"status"`
	ContentType string      `json:"content_type"`
	Headers     http.Header `json:"headers"`
	Body        any         `json:"body"`
}

func testHTTPAPI(t *testing.T, f *transferFixture) http.Handler {
	t.Helper()
	roots, err := localfs.New([]localfs.Root{{ID: "test", Path: f.root, Read: true, Write: true}}, f.directory)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(httpTestToken))
	auth, err := identity.New([]identity.Record{{ID: "admin", TokenSHA256: hex.EncodeToString(sum[:]), Scopes: []string{"admin"}, Accounts: []string{"*"}, Roots: []string{"*"}}})
	if err != nil {
		t.Fatal(err)
	}
	ops, baseline, err := contract.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	handler, err := httpapi.New(httpapi.Dependencies{Auth: appauth.New(auth), Configuration: appconfig.New(f.store, DefaultConfiguration(), roots, network.Environment{}), Accounts: appaccounts.NewValidated(f.store, f.store, f.clients), Transfers: f.service, System: appsystem.New("test", baseline, time.Now(), ops), Operations: ops, Contract: contract.JSON()})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}
func wire(t *testing.T, handler http.Handler, id, method, url, key, body string, status int, rows *[]liveResponse) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, url, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+httpTestToken)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != status {
		t.Fatalf("%s %s: status %d want %d: %s", method, url, rec.Code, status, rec.Body.String())
	}
	if rows != nil {
		var b any
		if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
			t.Fatal(err)
		}
		*rows = append(*rows, liveResponse{id, method, rec.Code, strings.Split(rec.Header().Get("Content-Type"), ";")[0], rec.Header().Clone(), b})
	}
	return rec
}
func resourceID(t *testing.T, rec *httptest.ResponseRecorder, key string) string {
	t.Helper()
	var data struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	var id string
	if err := json.Unmarshal(data.Data[key], &id); err != nil {
		t.Fatal(err)
	}
	return id
}
func waitHTTP(t *testing.T, handler http.Handler, id, state string) *httptest.ResponseRecorder {
	t.Helper()
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		rec := wire(t, handler, "JOB02", "GET", "/api/v1/jobs/"+id, "", "", 200, nil)
		var body struct {
			Data struct {
				State string `json:"state"`
			}
		}
		json.Unmarshal(rec.Body.Bytes(), &body)
		if body.Data.State == state {
			return rec
		}
		if body.Data.State == "failed" {
			t.Fatalf("job failed: %s", rec.Body.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("job %s did not reach %s", id, state)
	return nil
}
func TestP2LiveHTTPTransfersAndRecovery(t *testing.T) {
	f := newTransferFixture(t)
	handler := testHTTPAPI(t, f)
	rows := []liveResponse{}
	created := wire(t, handler, "ACC01", "POST", "/api/v1/accounts", "account-once", `{"label":"test","credentials":{"kind":"cookies","cookies":"BDUSS=P2_ONLY_TEST; STOKEN=PAN_ONLY"}}`, 201, &rows)
	accountID := resourceID(t, created, "account_id")
	accountURL := "/api/v1/accounts/" + accountID
	replay := wire(t, handler, "ACC01", "POST", "/api/v1/accounts", "account-once", `{"label":"test","credentials":{"kind":"cookies","cookies":"BDUSS=P2_ONLY_TEST; STOKEN=PAN_ONLY"}}`, 201, nil)
	if resourceID(t, replay, "account_id") != accountID {
		t.Fatal("account replay created another identity")
	}
	wire(t, handler, "ACC01", "POST", "/api/v1/accounts", "account-once", `{"label":"different","credentials":{"kind":"cookies","cookies":"BDUSS=P2_ONLY_TEST; STOKEN=PAN_ONLY"}}`, 409, &rows)
	wire(t, handler, "ACC02", "GET", "/api/v1/accounts", "", "", 200, &rows)
	wire(t, handler, "ACC02", "GET", "/api/v1/accounts?unexpected=1", "", "", 400, &rows)
	wire(t, handler, "ACC03", "GET", accountURL, "", "", 200, &rows)
	wire(t, handler, "ACC03", "GET", "/api/v1/accounts/does-not-exist", "", "", 404, &rows)
	validation := wire(t, handler, "ACC07", "POST", accountURL+"/validate", "", "", 200, &rows)
	if !strings.Contains(validation.Body.String(), `"valid":true`) || strings.Contains(validation.Body.String(), `"baidu_uid":"123"`) {
		t.Fatal("validation forged UID or failed")
	}
	wire(t, handler, "ACC07", "POST", accountURL+"/validate", "", `{}`, 400, &rows)
	content := bytes.Repeat([]byte("P2 HTTP round-trip\n"), 100)
	if err := os.WriteFile(filepath.Join(f.root, "source.bin"), content, 0600); err != nil {
		t.Fatal(err)
	}
	uploadBody := `{"sources":[{"type":"local","root_id":"test","relative_path":"source.bin"}],"target_dir":"/http",` + uploadOptions + `}`
	upload := wire(t, handler, "UPL01", "POST", accountURL+"/uploads", "upload-once", uploadBody, 202, &rows)
	jobID := resourceID(t, upload, "job_id")
	waitHTTP(t, handler, jobID, "succeeded")
	wire(t, handler, "JOB02", "GET", "/api/v1/jobs/"+jobID, "", "", 200, &rows)
	result := wire(t, handler, "JOB04", "GET", "/api/v1/jobs/"+jobID+"/result", "", "", 200, &rows)
	if !strings.Contains(result.Body.String(), `"succeeded":"1"`) {
		t.Fatal(result.Body.String())
	}
	wire(t, handler, "JOB02", "GET", "/api/v1/jobs/not-found", "", "", 404, &rows)
	wire(t, handler, "JOB04", "GET", "/api/v1/jobs/not-found/result", "", "", 404, &rows)
	wire(t, handler, "UPL01", "POST", accountURL+"/uploads", "", strings.Replace(uploadBody, `"parallel_per_file":1`, `"parallel_per_file":2`, 1), 422, &rows)
	// A new skip submission succeeds without changing the existing remote bytes.
	skipped := wire(t, handler, "UPL01", "POST", accountURL+"/uploads", "skip-once", uploadBody, 202, nil)
	skippedID := resourceID(t, skipped, "job_id")
	waitHTTP(t, handler, skippedID, "succeeded")
	skipResult := wire(t, handler, "JOB04", "GET", "/api/v1/jobs/"+skippedID+"/result", "", "", 200, nil)
	if !strings.Contains(skipResult.Body.String(), `"skipped":"1"`) || !strings.Contains(skipResult.Body.String(), `"transferred_bytes":"0"`) {
		t.Fatal("skip is not represented truthfully", skipResult.Body.String())
	}
	if err := os.Mkdir(filepath.Join(f.root, "out"), 0700); err != nil {
		t.Fatal(err)
	}
	downloadBody := `{"selector":{"kind":"paths","values":["/http/source.bin"]},"destination":{"root_id":"test","directory":"out"},` + downloadOptions + `}`
	download := wire(t, handler, "DWN01", "POST", accountURL+"/downloads", "download-once", downloadBody, 202, &rows)
	downloadID := resourceID(t, download, "job_id")
	waitHTTP(t, handler, downloadID, "succeeded")
	got, err := os.ReadFile(filepath.Join(f.root, "out", "source.bin"))
	if err != nil || !bytes.Equal(got, content) {
		t.Fatal("HTTP transfer changed bytes", err)
	}
	wire(t, handler, "DWN01", "POST", accountURL+"/downloads", "", strings.Replace(downloadBody, `"overwrite":false`, `"overwrite":true`, 1), 422, &rows)
	cancelBody := strings.Replace(uploadBody, `"target_dir":"/http"`, `"target_dir":"/cancel-http"`, 1)
	cancelled := wire(t, handler, "UPL01", "POST", accountURL+"/uploads", "cancel", cancelBody, 202, nil)
	cancelID := resourceID(t, cancelled, "job_id")
	select {
	case <-f.upstream.blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("cancel upstream did not block")
	}
	wire(t, handler, "JOB04", "GET", "/api/v1/jobs/"+cancelID+"/result", "", "", 409, &rows)
	wire(t, handler, "JOB06", "POST", "/api/v1/jobs/"+cancelID+"/cancel", "cancel-control", `{}`, 202, &rows)
	waitHTTP(t, handler, cancelID, "cancelled")
	wire(t, handler, "JOB06", "POST", "/api/v1/jobs/"+cancelID+"/cancel", "cancel-control", `{"reason":"different"}`, 409, &rows)
	wire(t, handler, "JOB08", "POST", "/api/v1/jobs/"+cancelID+"/resume", "", `{}`, 409, &rows)
	// Restart with one confirmed upload part and then restore through HTTP.
	big := bytes.Repeat([]byte{0x4a}, 5<<20)
	if err = os.WriteFile(filepath.Join(f.root, "big.bin"), big, 0600); err != nil {
		t.Fatal(err)
	}
	restartBody := strings.Replace(strings.Replace(uploadBody, "source.bin", "big.bin", 1), `"/http"`, `"/restart-http"`, 1)
	restart := wire(t, handler, "UPL01", "POST", accountURL+"/uploads", "restart", restartBody, 202, nil)
	restartID := resourceID(t, restart, "job_id")
	select {
	case <-f.upstream.blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("restart upstream did not block")
	}
	f.close(t)
	f.open(t)
	handler = testHTTPAPI(t, f)
	waitHTTP(t, handler, restartID, "interrupted")
	wire(t, handler, "JOB08", "POST", "/api/v1/jobs/"+restartID+"/resume", "resume-control", `{}`, 202, &rows)
	waitHTTP(t, handler, restartID, "succeeded")
	wire(t, handler, "JOB08", "POST", "/api/v1/jobs/"+restartID+"/resume", "resume-control", `{}`, 202, nil)
	f.upstream.mu.Lock()
	partCount := f.upstream.parts["P2_ONLY_TEST\x00/restart-http/big.bin"][0]
	f.upstream.mu.Unlock()
	if partCount != 1 {
		t.Fatalf("confirmed part repeated %d times", partCount)
	}
	// A request context cannot own the lifetime of the accepted transfer.
	disconnectedBody := strings.Replace(uploadBody, `"/http"`, `"/disconnected"`, 1)
	req := httptest.NewRequest("POST", accountURL+"/uploads", strings.NewReader(disconnectedBody))
	ctx, cancel := context.WithCancel(context.Background())
	req = req.WithContext(ctx)
	req.Header.Set("Authorization", "Bearer "+httpTestToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	cancel()
	if rec.Code != 202 {
		t.Fatal(rec.Body.String())
	}
	waitHTTP(t, handler, resourceID(t, rec, "job_id"), "succeeded")
	for _, row := range rows {
		encoded, _ := json.Marshal(row.Body)
		for _, secret := range []string{"P2_ONLY_TEST", "PAN_ONLY", "Uploadid", "uploadid", f.root} {
			if strings.Contains(string(encoded), secret) {
				t.Fatalf("public response leaked %s", secret)
			}
		}
	}
	if name := os.Getenv("BAIDUPCS_P2_CONTRACT_RESPONSES"); name != "" {
		encoded, _ := json.MarshalIndent(rows, "", "  ")
		if err = os.WriteFile(name, append(encoded, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestP2HTTPRejectsUnsupportedAndMalformedRequests(t *testing.T) {
	f := newTransferFixture(t)
	handler := testHTTPAPI(t, f)
	created := wire(t, handler, "ACC01", "POST", "/api/v1/accounts", "", `{"credentials":{"kind":"bduss","bduss":"TEST_ONLY"}}`, 201, nil)
	id := resourceID(t, created, "account_id")
	for _, body := range []string{`{"credentials":{"kind":"cookies","cookies":"bad"}}`, `{"credentials":{"kind":"bduss","bduss":"TEST","access_token":""}}`, `{"credentials":null}`, `{"credentials":{"kind":"bduss","bduss":"TEST","bduss":"OTHER"}}`, `{"Credentials":{"kind":"bduss","bduss":"TEST"}}`} {
		wire(t, handler, "ACC01", "POST", "/api/v1/accounts", "", body, 400, nil)
	}
	cases := []struct {
		body   string
		status int
	}{
		{`{"sources":[{"type":"staged","staged_file_id":"staged_1"}],"target_dir":"/"}`, 422},
		{`{"sources":[{"type":"local","root_id":"test","relative_path":"../outside"}],"target_dir":"/"}`, 400},
		{`{"sources":[{"type":"local","root_id":"test","relative_path":"test.bin"}],"target_dir":"/","dry_run":true}`, 422},
		{`{"sources":[{"type":"local","root_id":"test","relative_path":"test.bin"}],"target_dir":"/",` + uploadOptions + `}`, 404},
	}
	for _, tc := range cases {
		wire(t, handler, "UPL01", "POST", fmt.Sprintf("/api/v1/accounts/%s/uploads", id), "", tc.body, tc.status, nil)
	}
}

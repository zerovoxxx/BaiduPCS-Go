package bootstrap

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/account"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/infrastructure/baidu"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/infrastructure/localfs"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/infrastructure/persistence"
)

// Opt-in only. This test cannot use a CLI's active account or an arbitrary disk
// directory: credentials and the explicitly allowed verification root are required.
func TestP2UpstreamDedicatedAccount(t *testing.T) {
	credentialFile, remoteBase := os.Getenv("BAIDUPCS_P2_LIVE_CREDENTIAL_FILE"), os.Getenv("BAIDUPCS_P2_LIVE_REMOTE_DIR")
	if credentialFile == "" || remoteBase == "" {
		t.Skip("dedicated credentials and an explicit test directory are required")
	}
	if remoteBase != "/BaiduPCS-Go-P2-Verification" || path.Clean(remoteBase) != remoteBase {
		t.Fatal("only the explicitly approved P2 verification root is accepted")
	}
	raw, err := persistence.ReadPrivateFile(credentialFile, 1<<20)
	if err != nil {
		t.Fatal("cannot read the dedicated credential file")
	}
	var input struct {
		Credentials struct {
			Kind        string `json:"kind"`
			BDUSS       string `json:"bduss"`
			PToken      string `json:"ptoken"`
			SToken      string `json:"stoken"`
			SBoxTKN     string `json:"sboxtkn"`
			BAIDUID     string `json:"baiduid"`
			Cookies     string `json:"cookies"`
			AccessToken string `json:"access_token"`
		} `json:"credentials"`
	}
	if json.Unmarshal(raw, &input) != nil {
		t.Fatal("invalid dedicated credential input")
	}
	c := input.Credentials
	credentials, err := account.Normalize(account.Import{Kind: c.Kind, BDUSS: c.BDUSS, PToken: c.PToken, SToken: c.SToken, SBoxTKN: c.SBoxTKN, BAIDUID: c.BAIDUID, Cookies: c.Cookies, AccessToken: c.AccessToken})
	if err != nil {
		t.Fatal("invalid dedicated credential format")
	}
	credentials.AccountID, credentials.Revision = "live_setup", 1
	cfg := baidu.DefaultConfiguration()
	cfg.Proxy = os.Getenv("HTTPS_PROXY")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	clients := baidu.NewClientFactory()
	defer clients.Close()
	setup, err := clients.New(ctx, configuration.NewSnapshot(1, cfg), credentials)
	if err != nil {
		t.Fatal("cannot bind the live setup client")
	}
	defer setup.Close()
	report := map[string]any{"started_at": time.Now().UTC().Format(time.RFC3339Nano), "platform": runtime.GOOS + "/" + runtime.GOARCH, "go_version": runtime.Version(), "appid": cfg.AppID, "scope": "dedicated single-file P2 verification only", "stages": map[string]any{}}
	stages := report["stages"].(map[string]any)
	defer func() {
		report["finished_at"] = time.Now().UTC().Format(time.RFC3339Nano)
		report["passed"] = !t.Failed()
		if name := os.Getenv("BAIDUPCS_P2_LIVE_REPORT"); name != "" {
			data, _ := json.MarshalIndent(report, "", "  ")
			if err := os.WriteFile(name, append(data, '\n'), 0600); err != nil {
				t.Error("cannot write sanitized verification report")
			}
		}
	}()
	var nonce [12]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	runDir := remoteBase + "/run-" + hex.EncodeToString(nonce[:])
	if _, err = setup.PCS.FilesDirectoriesMeta(remoteBase); err != nil {
		if err = setup.PCS.Mkdir(remoteBase); err != nil {
			stages["setup"] = safeUpstreamCode(err)
			t.Fatal("cannot create the approved verification root")
		}
	}
	if err = setup.PCS.Mkdir(runDir); err != nil {
		stages["setup"] = safeUpstreamCode(err)
		t.Fatal("cannot create this run's isolated directory")
	}
	stages["setup"] = "passed"
	defer func() {
		if err := setup.PCS.Remove(runDir); err != nil {
			stages["cleanup"] = safeUpstreamCode(err)
			t.Error("dedicated test directory cleanup failed")
		} else {
			stages["cleanup"] = "passed"
		}
	}()
	options, _ := testOptions(t)
	data, err := os.ReadFile(options.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	var startupConfig startup
	if json.Unmarshal(data, &startupConfig) != nil {
		t.Fatal("invalid test startup")
	}
	root := t.TempDir()
	startupConfig.Roots = []localfs.Root{{ID: "live", Path: root, Read: true, Write: true}}
	data, _ = json.Marshal(startupConfig)
	if err = os.WriteFile(options.ConfigFile, data, 0600); err != nil {
		t.Fatal(err)
	}
	service, err := New(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(service.Handler)
	defer func() {
		server.Close()
		if err := service.Close(); err != nil {
			t.Error("live runtime did not drain cleanly")
		}
	}()
	call := func(method, uri, key string, body []byte, want int) map[string]any {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, server.URL+uri, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+testToken)
		if len(body) > 0 {
			req.Header.Set("Content-Type", "application/json")
		}
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		if method == "PATCH" {
			req.Header.Set("If-Match", `"1"`)
		}
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal("live HTTP request failed")
		}
		defer response.Body.Close()
		var envelope struct {
			Data map[string]any `json:"data"`
			Code string         `json:"code"`
		}
		if err = json.NewDecoder(response.Body).Decode(&envelope); err != nil {
			t.Fatal("invalid live HTTP response")
		}
		if response.StatusCode != want {
			stages["http_failure"] = map[string]any{"status": response.StatusCode, "code": envelope.Code}
			t.Fatalf("live HTTP status %d, expected %d (business code %s)", response.StatusCode, want, envelope.Code)
		}
		return envelope.Data
	}
	proxyBody, _ := json.Marshal(map[string]any{"proxy": cfg.Proxy})
	call("PATCH", "/api/v1/configuration", "live-proxy", proxyBody, 200)
	imported := call("POST", "/api/v1/accounts", "live-account", raw, 201)
	id := imported["account_id"].(string)
	accountURL := "/api/v1/accounts/" + id
	validation := call("POST", accountURL+"/validate", "", nil, 200)
	if validation["valid"] != true {
		stages["account_validation"] = "failed"
		t.Fatal("dedicated session did not pass online validation")
	}
	stages["account_validation"] = "passed"
	uploadOptions := map[string]any{"file_concurrency": 1, "parallel_per_file": 1, "max_retries": 0, "policy": "skip", "skip_rapid_check": true, "check_filename": false, "no_split_file": false, "max_rate_bytes_per_second": "0"}
	upload := func(file, key string) string {
		body, _ := json.Marshal(map[string]any{"sources": []any{map[string]any{"type": "local", "root_id": "live", "relative_path": file}}, "target_dir": runDir, "options": uploadOptions})
		job := call("POST", accountURL+"/uploads", key, body, 202)
		return job["job_id"].(string)
	}
	wait := func(jobID string) map[string]any {
		t.Helper()
		until := time.Now().Add(3 * time.Minute)
		for time.Now().Before(until) {
			job := call("GET", "/api/v1/jobs/"+jobID, "", nil, 200)
			switch job["state"] {
			case "succeeded", "cancelled":
				return job
			case "failed", "interrupted":
				problem, _ := job["problem"].(map[string]any)
				stages["job_failure"] = problem["code"]
				t.Fatalf("live job did not succeed: state=%s code=%v", job["state"], problem["code"])
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatal("live job deadline exceeded")
		return nil
	}
	source := make([]byte, 1<<20)
	if _, err = rand.Read(source); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "input.bin"), source, 0600); err != nil {
		t.Fatal(err)
	}
	jobID := upload("input.bin", "upload-one")
	if wait(jobID)["state"] != "succeeded" {
		t.Fatal("upload not successful")
	}
	stages["upload"] = "passed"
	if err = os.Mkdir(filepath.Join(root, "out"), 0700); err != nil {
		t.Fatal(err)
	}
	downloadOptions := map[string]any{"mode": "pcs", "path_mode": "relative", "parallel": 1, "file_concurrency": 1, "max_retries": 0, "overwrite": false, "verify_checksum": true, "preserve_mtime": false, "executable": false, "link_index": 0, "test": false, "report_workers": false, "max_rate_bytes_per_second": "0"}
	body, _ := json.Marshal(map[string]any{"selector": map[string]any{"kind": "paths", "values": []string{runDir + "/input.bin"}}, "destination": map[string]any{"root_id": "live", "directory": "out"}, "options": downloadOptions})
	downloaded := call("POST", accountURL+"/downloads", "download-one", body, 202)
	if wait(downloaded["job_id"].(string))["state"] != "succeeded" {
		t.Fatal("download not successful")
	}
	got, err := os.ReadFile(filepath.Join(root, "out", "input.bin"))
	if err != nil || sha256.Sum256(got) != sha256.Sum256(source) {
		t.Fatal("real upload/download byte verification failed")
	}
	stages["download_bytes"] = "passed"
	// Cancel a real second upload while another logical task remains usable.
	large := make([]byte, 5<<20)
	if _, err = rand.Read(large); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "cancel.bin"), large, 0600); err != nil {
		t.Fatal(err)
	}
	cancelID := upload("cancel.bin", "cancel-one")
	call("POST", "/api/v1/jobs/"+cancelID+"/cancel", "cancel-control", []byte(`{}`), 202)
	cancelled := wait(cancelID)
	stages["cancel_terminal_state"] = cancelled["state"]
	if err = os.WriteFile(filepath.Join(root, "after-cancel.bin"), source[:4096], 0600); err != nil {
		t.Fatal(err)
	}
	wait(upload("after-cancel.bin", "after-cancel"))
	stages["after_cancel_upload"] = "passed"
	if err = os.WriteFile(filepath.Join(root, "restart.bin"), large, 0600); err != nil {
		t.Fatal(err)
	}
	restartID := upload("restart.bin", "restart-one")
	// Close after the durable acceptance; a task that already completed remains
	// completed, otherwise the same account/config/checkpoint must be recoverable.
	time.Sleep(150 * time.Millisecond)
	server.Close()
	if err = service.Close(); err != nil {
		t.Fatal("live shutdown failed")
	}
	service, err = New(ctx, options)
	if err != nil {
		t.Fatal("live restart failed")
	}
	server = httptest.NewServer(service.Handler)
	restored := call("GET", "/api/v1/jobs/"+restartID, "", nil, 200)
	stages["restart_observed_state"] = restored["state"]
	if restored["state"] == "interrupted" {
		call("POST", "/api/v1/jobs/"+restartID+"/resume", "resume-control", []byte(`{}`), 202)
		stages["resume"] = "passed"
	}
	if wait(restartID)["state"] != "succeeded" {
		t.Fatal("restored transfer not successful")
	}
	stages["restart_result"] = "passed"
	// Re-read the confirmed remote length without enumerating the user's drive.
	meta, err := setup.PCS.FilesDirectoriesMeta(runDir + "/restart.bin")
	if err != nil || meta.Size != int64(len(large)) {
		t.Fatal("restored remote result did not match")
	}
	stages["request_count_policy"] = "only dedicated paths; no normal drive listing"
	t.Log("dedicated P2 live verification passed; sanitized report written when configured")
}
func safeUpstreamCode(err error) string {
	if e, ok := err.(interface{ GetRemoteErrCode() int }); ok {
		return fmt.Sprintf("UPSTREAM_%d", e.GetRemoteErrCode())
	}
	return "NETWORK_OR_PROTOCOL_ERROR"
}

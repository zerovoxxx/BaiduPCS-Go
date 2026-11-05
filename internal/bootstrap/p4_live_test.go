package bootstrap

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http/httptest"
	"net/url"
	"os"
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

// Opt-in: every remote file request stays inside this user-approved directory.
// Report only stage outcomes, never credentials, identity values or file entries.
func TestP4UpstreamDedicatedAccount(t *testing.T) {
	credentialFile, remoteBase := os.Getenv("BAIDUPCS_P3_LIVE_CREDENTIAL_FILE"), os.Getenv("BAIDUPCS_P3_LIVE_REMOTE_DIR")
	credentialJSON := os.Getenv("BAIDUPCS_P3_CREDENTIALS_JSON")
	if credentialFile == "" && credentialJSON == "" || remoteBase == "" {
		t.Skip("explicit P3 credentials and approved verification directory required")
	}
	if remoteBase != "/BaiduPCS-Go-P2-Verification" {
		t.Fatal("only the explicitly approved verification directory is accepted")
	}
	raw := []byte(credentialJSON)
	if credentialFile != "" {
		var err error
		raw, err = persistence.ReadPrivateFile(credentialFile, 1<<20)
		if err != nil {
			t.Fatal("cannot read dedicated P3 credential file")
		}
	}
	if len(raw) > 1<<20 {
		t.Fatal("dedicated P3 credential input exceeds size limit")
	}
	var input struct {
		Credentials credentialLiveInput `json:"credentials"`
	}
	if json.Unmarshal(raw, &input) != nil {
		t.Fatal("invalid dedicated credential input")
	}
	v := input.Credentials
	c, err := account.Normalize(account.Import{Kind: v.Kind, BDUSS: v.BDUSS, PToken: v.PToken, SToken: v.SToken, SBoxTKN: v.SBoxTKN, BAIDUID: v.BAIDUID, Cookies: v.Cookies, AccessToken: v.AccessToken})
	if err != nil {
		t.Fatal("invalid dedicated credential format")
	}
	c.AccountID, c.Revision = "p4_live_setup", 1
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	clients := baidu.NewClientFactory()
	defer clients.Close()
	cfg := baidu.DefaultConfiguration()
	cfg.Proxy = os.Getenv("HTTPS_PROXY")
	setup, err := clients.New(ctx, configuration.NewSnapshot(1, cfg), c)
	if err != nil {
		t.Fatal("cannot bind dedicated setup client")
	}
	defer setup.Close()
	stages := map[string]string{}
	report := map[string]any{"started_at": time.Now().UTC().Format(time.RFC3339Nano), "platform": runtime.GOOS + "/" + runtime.GOARCH, "go_version": runtime.Version(), "scope": "P4 dedicated-directory upload/download/staging/content/tickets; controlled tests cover failure and concurrency windows", "credential_kind": v.Kind, "tls_certificate_verification": true, "stages": stages}
	defer func() {
		report["finished_at"] = time.Now().UTC().Format(time.RFC3339Nano)
		report["passed"] = !t.Failed()
		if file := os.Getenv("BAIDUPCS_P4_LIVE_REPORT"); file != "" {
			encoded, _ := json.MarshalIndent(report, "", "  ")
			if err := os.WriteFile(file, append(encoded, '\n'), 0600); err != nil {
				t.Error("cannot write sanitized P3 live report")
			}
		}
	}()
	var nonce [12]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	runDir := remoteBase + "/p4-" + hex.EncodeToString(nonce[:])
	if _, err = setup.PCS.FilesDirectoriesMeta(remoteBase); err != nil {
		if err = setup.PCS.Mkdir(remoteBase); err != nil {
			stages["setup"] = safeUpstreamCode(err)
			t.Fatal("cannot prepare approved verification root")
		}
	}
	if err = setup.PCS.Mkdir(runDir); err != nil {
		stages["setup"] = safeUpstreamCode(err)
		t.Fatal("cannot create this run's dedicated directory")
	}
	defer func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		cleanupClient, err := clients.New(cleanupContext, configuration.NewSnapshot(1, cfg), c)
		if err != nil {
			stages["cleanup"] = "CLIENT_BINDING_FAILED"
			t.Error("cannot bind dedicated cleanup client")
			return
		}
		defer cleanupClient.Close()
		if err := cleanupClient.PCS.Remove(runDir); err != nil {
			stages["cleanup"] = safeUpstreamCode(err)
			t.Error("dedicated directory cleanup failed")
		} else {
			stages["cleanup"] = "passed"
		}
	}()
	options, _ := testOptions(t)
	startupData, _ := os.ReadFile(options.ConfigFile)
	var startupConfig startup
	_ = json.Unmarshal(startupData, &startupConfig)
	root := t.TempDir()
	startupConfig.Roots = []localfs.Root{{ID: "p4live", Path: root, Read: true, Write: true}}
	startupData, _ = json.Marshal(startupConfig)
	if err = os.WriteFile(options.ConfigFile, startupData, 0600); err != nil {
		t.Fatal(err)
	}
	service, err := New(ctx, options)
	if err != nil {
		t.Fatal("cannot start live HTTP service")
	}
	defer service.Close()
	// Propagate the session proxy only as a private saved configuration value.
	saved, err := service.store.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	configurationValue := saved.Client()
	configurationValue.Proxy = cfg.Proxy
	if _, err = service.store.Commit(ctx, saved.Revision(), configurationValue, configuration.Mutation{}); err != nil {
		t.Fatal("cannot save private live proxy")
	}
	call := func(stage, method, uri string, body []byte, want int, headers map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, uri, bytes.NewReader(body)).WithContext(ctx)
		req.Header.Set("Authorization", "Bearer "+testToken)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		service.Handler.ServeHTTP(rec, req)
		if rec.Code != want {
			var problem struct{ Code string }
			_ = json.Unmarshal(rec.Body.Bytes(), &problem)
			stages[stage] = fmt.Sprintf("HTTP_%d_%s", rec.Code, problem.Code)
			t.Fatalf("P4 live stage %s failed with HTTP %d", stage, rec.Code)
		}
		stages[stage] = "passed"
		return rec
	}

	created := call("account_import", "POST", "/api/v1/accounts", raw, 201, nil)
	id := dataMap(t, created)["account_id"].(string)
	base := "/api/v1/accounts/" + id
	call("account_validate", "POST", base+"/validate", nil, 200, nil)
	mustJSON := func(v any) []byte {
		b, e := json.Marshal(v)
		if e != nil {
			t.Fatal("cannot encode fixture request")
		}
		return b
	}
	wait := func(stage, job string) {
		t.Helper()
		for ctx.Err() == nil {
			got := call(stage+"_query", "GET", "/api/v1/jobs/"+job, nil, 200, nil)
			d := dataMap(t, got)
			state := d["state"].(string)
			if state == "succeeded" {
				stages[stage] = "passed"
				return
			}
			if state == "failed" || state == "partially_succeeded" || state == "interrupted" {
				stages[stage] = state
				t.Fatalf("P4 stage %s ended %s", stage, state)
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatal("P4 deadline")
	}
	for _, dir := range []string{"tree/a/empty", "tree/b", "out"} {
		if err = os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal("cannot create fixture")
		}
	}
	payload := bytes.Repeat([]byte("P4 dedicated payload\n"), 2048)
	for name, content := range map[string][]byte{"tree/a/x": payload, "tree/b/x": []byte("distinct"), "tree/zero": {}} {
		if err = os.WriteFile(filepath.Join(root, name), content, 0600); err != nil {
			t.Fatal("cannot write fixture")
		}
	}
	uploadOptions := map[string]any{"file_concurrency": 2, "parallel_per_file": 2, "max_retries": 1, "policy": "skip", "skip_rapid_check": true, "check_filename": true, "max_rate_bytes_per_second": "0"}
	upload := func(stage, source, target string, options map[string]any) string {
		job := call(stage+"_submit", "POST", base+"/uploads", mustJSON(map[string]any{"sources": []any{map[string]string{"type": "local", "root_id": "p4live", "relative_path": source}}, "target_dir": target, "options": options}), 202, nil)
		j := dataMap(t, job)["job_id"].(string)
		wait(stage, j)
		return j
	}
	upload("directory_upload", "tree", runDir, uploadOptions)
	upload("skip_policy", "tree", runDir, uploadOptions)
	uploadOptions["policy"] = "rsync"
	upload("rsync_policy", "tree", runDir, uploadOptions)
	os.WriteFile(filepath.Join(root, "rapid.bin"), payload, 0600)
	uploadOptions["skip_rapid_check"] = false
	upload("rapid_handshake", "rapid.bin", runDir, uploadOptions)
	uploadOptions["skip_rapid_check"] = true
	uploadOptions["policy"] = "overwrite"
	os.WriteFile(filepath.Join(root, "rapid.bin"), []byte("updated"), 0600)
	upload("overwrite_policy", "rapid.bin", runDir, uploadOptions)
	downloadOptions := map[string]any{"mode": "pcs", "path_mode": "relative", "parallel": 2, "file_concurrency": 2, "max_retries": 0, "overwrite": false, "verify_checksum": true, "preserve_mtime": true, "executable": true, "report_workers": true, "max_rate_bytes_per_second": "0"}
	downloaded := call("directory_download_submit", "POST", base+"/downloads", mustJSON(map[string]any{"selector": map[string]any{"kind": "paths", "values": []string{runDir + "/tree"}}, "destination": map[string]string{"root_id": "p4live", "directory": "out"}, "options": downloadOptions}), 202, nil)
	wait("directory_download", dataMap(t, downloaded)["job_id"].(string))
	got, e := os.ReadFile(filepath.Join(root, "out/tree/a/x"))
	if e != nil || sha256.Sum256(got) != sha256.Sum256(payload) {
		t.Fatal("real download bytes mismatch")
	}
	stages["download_sha256"] = "passed"
	if info, e := os.Stat(filepath.Join(root, "out/tree/a/empty")); e != nil || !info.IsDir() {
		t.Fatal("real empty directory missing")
	}
	stages["empty_directory"] = "passed"
	call("proxy_range", "GET", base+"/content?path="+url.QueryEscape(runDir+"/tree/a/x"), nil, 206, map[string]string{"Range": "bytes=1-9"})
	call("proxy_head", "HEAD", base+"/content?path="+url.QueryEscape(runDir+"/tree/a/x"), nil, 200, nil)
	call("proxy_unsatisfied", "GET", base+"/content?path="+url.QueryEscape(runDir+"/tree/a/x"), nil, 416, map[string]string{"Range": "bytes=999999999-"})
	call("download_links", "POST", base+"/download-links", mustJSON(map[string]any{"selector": map[string]any{"kind": "paths", "values": []string{runDir + "/tree/a/x"}}, "source": "direct"}), 200, nil)
	ticket := call("ticket_create", "POST", base+"/download-tickets", mustJSON(map[string]any{"path": runDir + "/tree/a/x"}), 201, nil)
	ticketURL := dataMap(t, ticket)["url"].(string)
	req := httptest.NewRequest("GET", ticketURL, nil).WithContext(ctx)
	req.Header.Set("Range", "bytes=0-9")
	rec := httptest.NewRecorder()
	service.Handler.ServeHTTP(rec, req)
	if rec.Code != 206 {
		t.Fatal("real ticket redemption failed")
	}
	stages["ticket_redeem"] = "passed"
	var multipartBody bytes.Buffer
	mw := multipart.NewWriter(&multipartBody)
	part, _ := mw.CreateFormFile("file", "staged.bin")
	part.Write(payload)
	mw.Close()
	req = httptest.NewRequest("POST", "/api/v1/staging-files", &multipartBody).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec = httptest.NewRecorder()
	service.Handler.ServeHTTP(rec, req)
	if rec.Code != 201 {
		t.Fatal("real staging failed")
	}
	stages["staging_receive"] = "passed"
	stageID := dataMap(t, rec)["staged_file_id"].(string)
	staged := call("staged_upload_submit", "POST", base+"/uploads", mustJSON(map[string]any{"sources": []any{map[string]string{"type": "staged", "staged_file_id": stageID}}, "target_dir": runDir, "options": uploadOptions}), 202, nil)
	wait("staged_upload", dataMap(t, staged)["job_id"].(string))
	call("staging_read", "GET", "/api/v1/staging-files/"+stageID, nil, 200, nil)
	call("staging_delete", "DELETE", "/api/v1/staging-files/"+stageID, nil, 204, nil)
	// Three modes use the same isolated engine. Record upstream status separately:
	// account permissions and historical streaming availability can differ.
	for _, mode := range []string{"locate", "stream"} {
		downloadOptions["mode"] = mode
		downloadOptions["parallel"] = 1
		downloadOptions["verify_checksum"] = false
		downloadOptions["test"] = true
		job := call(mode+"_submit", "POST", base+"/downloads", mustJSON(map[string]any{"selector": map[string]any{"kind": "paths", "values": []string{runDir + "/rapid.bin"}}, "destination": map[string]string{"root_id": "p4live", "directory": mode}, "options": downloadOptions}), 202, nil)
		jid := dataMap(t, job)["job_id"].(string)
		for ctx.Err() == nil {
			r, err := service.store.Transfer(ctx, jid)
			if err != nil {
				t.Fatal("cannot read mode outcome")
			}
			if r.Terminal() || r.State == "interrupted" {
				stages["mode_"+mode] = r.State
				if r.FailureCode != "" {
					stages["mode_"+mode] = r.State + ":" + r.FailureCode
				}
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	report["allowed_remote_root"] = "/BaiduPCS-Go-P2-Verification/"
	report["not_claimed_as_upstream_verified"] = []string{"multiple accounts", "SIGKILL/pause/disk-full injection", "128GiB transfers", "large directories"}
}

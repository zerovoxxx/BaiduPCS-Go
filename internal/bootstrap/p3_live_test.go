package bootstrap

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
func TestP3UpstreamDedicatedAccount(t *testing.T) {
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
	c.AccountID, c.Revision = "p3_live_setup", 1
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
	report := map[string]any{"started_at": time.Now().UTC().Format(time.RFC3339Nano), "platform": runtime.GOOS + "/" + runtime.GOARCH, "go_version": runtime.Version(), "scope": "P3 dedicated directory reads, scans, events and artifacts; P2 single-file fixture upload", "credential_kind": v.Kind, "tls_certificate_verification": true, "stages": stages}
	defer func() {
		report["finished_at"] = time.Now().UTC().Format(time.RFC3339Nano)
		report["passed"] = !t.Failed()
		if file := os.Getenv("BAIDUPCS_P3_LIVE_REPORT"); file != "" {
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
	runDir := remoteBase + "/p3-" + hex.EncodeToString(nonce[:])
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
	startupConfig.Roots = []localfs.Root{{ID: "p3live", Path: root, Read: true, Write: true}}
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
			t.Fatalf("P3 live stage %s failed with HTTP %d", stage, rec.Code)
		}
		stages[stage] = "passed"
		return rec
	}
	created := call("account_import", "POST", "/api/v1/accounts", raw, 201, nil)
	id := dataMap(t, created)["account_id"].(string)
	base := "/api/v1/accounts/" + id
	call("account_list", "GET", "/api/v1/accounts", nil, 200, nil)
	accountRead := call("account_read", "GET", base, nil, 200, nil)
	label := call("account_patch", "PATCH", base, []byte(`{"label":"P3 live verified"}`), 200, map[string]string{"If-Match": accountRead.Header().Get("ETag")})
	credentialPatch, _ := json.Marshal(map[string]string{"bduss": c.Cookies["BDUSS"]})
	credentialUpdate := call("credential_update", "PATCH", base+"/credentials", credentialPatch, 200, map[string]string{"If-Match": label.Header().Get("ETag")})
	if dataMap(t, credentialUpdate)["credential_revision"] != "2" {
		t.Fatal("live credential revision did not advance")
	}
	validation := call("account_validation", "POST", base+"/validate", nil, 200, nil)
	if dataMap(t, validation)["valid"] != true {
		t.Fatal("dedicated credentials failed validation")
	}
	call("quota", "GET", base+"/quota", nil, 200, nil)
	call("capabilities", "GET", base+"/capabilities", nil, 200, nil)
	contents := bytes.Repeat([]byte("P3 fixture\n"), 512)
	if err = os.WriteFile(filepath.Join(root, "p3-fixture.txt"), contents, 0600); err != nil {
		t.Fatal(err)
	}
	uploadBody, _ := json.Marshal(map[string]any{"sources": []any{map[string]string{"type": "local", "root_id": "p3live", "relative_path": "p3-fixture.txt"}}, "target_dir": runDir, "options": map[string]any{"file_concurrency": 1, "parallel_per_file": 1, "max_retries": 0, "policy": "skip", "skip_rapid_check": true, "check_filename": false, "no_split_file": false, "max_rate_bytes_per_second": "0"}})
	uploaded := call("fixture_upload", "POST", base+"/uploads", uploadBody, 202, nil)
	uploadJob := dataMap(t, uploaded)["job_id"].(string)
	wait := func(job, expected string) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Minute)
		for time.Now().Before(deadline) {
			rec := call("job_query", "GET", "/api/v1/jobs/"+job, nil, 200, nil)
			state := dataMap(t, rec)["state"].(string)
			if state == expected {
				return
			}
			if state == "failed" || state == "partially_succeeded" || state == "interrupted" || state == "succeeded" || state == "cancelled" {
				t.Fatal("dedicated live job did not succeed")
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatal("live job deadline")
	}
	wait(uploadJob, "succeeded")
	call("list_page", "GET", base+"/files?path="+url.QueryEscape(runDir)+"&page_size=1", nil, 200, nil)
	call("metadata", "GET", base+"/files/metadata?path="+url.QueryEscape(runDir+"/p3-fixture.txt"), nil, 200, nil)
	paths, _ := json.Marshal(map[string]any{"paths": []string{runDir + "/p3-fixture.txt", runDir + "/missing"}})
	call("metadata_partial", "POST", base+"/files/metadata", paths, 200, nil)
	call("search", "GET", base+"/files/search?path="+url.QueryEscape(runDir)+"&keyword=p3-fixture", nil, 200, nil)
	patterns, _ := json.Marshal(map[string]any{"patterns": []string{runDir + "/p3-*.txt"}})
	call("path_matches", "POST", base+"/path-matches", patterns, 200, nil)
	scanBody, _ := json.Marshal(map[string]any{"paths": []string{runDir}, "options": map[string]any{"mode": "tree", "depth": 0, "max_nodes": "100", "max_duration_seconds": 120}})
	scan := call("scan", "POST", base+"/file-scans", scanBody, 202, nil)
	job := dataMap(t, scan)["job_id"].(string)
	wait(job, "succeeded")
	call("items", "GET", "/api/v1/jobs/"+job+"/items", nil, 200, nil)
	result := call("scan_result", "GET", "/api/v1/jobs/"+job+"/result", nil, 200, nil)
	details := dataMap(t, result)["result"].(map[string]any)
	if details["complete"] != true {
		t.Fatal("dedicated scan was incomplete")
	}
	artifactID := details["artifact_ids"].([]any)[0].(string)
	artifactURL := "/api/v1/artifacts/" + artifactID
	call("sse_snapshot", "GET", "/api/v1/jobs/"+job+"/events", nil, 200, nil)
	replay := call("sse_replay", "GET", "/api/v1/jobs/"+job+"/events", nil, 200, map[string]string{"Last-Event-ID": "evt_" + job + "_0"})
	if !strings.Contains(replay.Body.String(), "job.finished") {
		t.Fatal("dedicated scan finish event missing")
	}
	call("artifact_metadata", "GET", artifactURL, nil, 200, nil)
	call("artifact_range", "GET", artifactURL+"/content", nil, 206, map[string]string{"Range": "bytes=0-9"})
	call("artifact_head", "HEAD", artifactURL+"/content", nil, 200, nil)
	call("artifact_delete", "DELETE", artifactURL, nil, 204, nil)
	call("artifact_deleted", "GET", artifactURL, nil, 410, nil)
	partialBody, _ := json.Marshal(map[string]any{"paths": []string{runDir + "/p3-fixture.txt", runDir + "/missing"}})
	partial := call("partial_scan", "POST", base+"/file-scans", partialBody, 202, nil)
	partialID := dataMap(t, partial)["job_id"].(string)
	wait(partialID, "partially_succeeded")
	call("partial_items", "GET", "/api/v1/jobs/"+partialID+"/items", nil, 200, nil)
	retry := call("retry", "POST", "/api/v1/jobs/"+partialID+"/retry", []byte(`{"scope":"failed"}`), 202, nil)
	retryID := dataMap(t, retry)["job_id"].(string)
	if retryID == partialID {
		t.Fatal("live retry reused original job")
	}
	wait(retryID, "failed")
	call("job_list", "GET", "/api/v1/jobs?account_id="+id, nil, 200, nil)
	current := call("account_read_after_jobs", "GET", base, nil, 200, nil)
	cleared := call("credential_clear", "PATCH", base+"/credentials", []byte(`{"cookies":null}`), 200, map[string]string{"If-Match": current.Header().Get("ETag")})
	if dataMap(t, cleared)["credential_revision"] != "3" {
		t.Fatal("live credential clear did not advance revision")
	}
	call("account_delete", "DELETE", base, nil, 204, nil)
	stages["request_scope"] = "only approved directory; no normal drive listing"
}

type credentialLiveInput struct {
	Kind        string `json:"kind"`
	BDUSS       string `json:"bduss"`
	PToken      string `json:"ptoken"`
	SToken      string `json:"stoken"`
	SBoxTKN     string `json:"sboxtkn"`
	BAIDUID     string `json:"baiduid"`
	Cookies     string `json:"cookies"`
	AccessToken string `json:"access_token"`
}

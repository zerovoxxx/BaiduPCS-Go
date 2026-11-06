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
func TestP5UpstreamDedicatedAccount(t *testing.T) {
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
	c.AccountID, c.Revision = "p5_live_setup", 1
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
	report := map[string]any{"started_at": time.Now().UTC().Format(time.RFC3339Nano), "platform": runtime.GOOS + "/" + runtime.GOARCH, "go_version": runtime.Version(), "scope": "P5 dedicated-directory file writes and new-share lifecycle; offline, recycle and global lists/clears use controlled evidence", "credential_kind": v.Kind, "tls_certificate_verification": true, "stages": stages}
	defer func() {
		report["finished_at"] = time.Now().UTC().Format(time.RFC3339Nano)
		report["passed"] = !t.Failed()
		if file := os.Getenv("BAIDUPCS_P5_LIVE_REPORT"); file != "" {
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
	runDir := remoteBase + "/p5-" + hex.EncodeToString(nonce[:])
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
	startupConfig.Roots = []localfs.Root{{ID: "p5live", Path: root, Read: true, Write: true}}
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
			t.Fatalf("P5 live stage %s failed with HTTP %d", stage, rec.Code)
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
				t.Fatalf("P5 stage %s ended %s", stage, state)
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatal("P5 deadline")
	}

	submit := func(stage, route string, input any) string {
		rec := call(stage+"_submit", "POST", base+route, mustJSON(input), 202, nil)
		id := dataMap(t, rec)["job_id"].(string)
		wait(stage, id)
		return id
	}
	submit("FIL07_mkdir", "/directories", map[string]any{"paths": []string{runDir + "/writes", runDir + "/writes/empty"}})
	if err = os.WriteFile(filepath.Join(root, "source"), []byte("P5 dedicated fixture\n"), 0600); err != nil {
		t.Fatal("cannot create local fixture")
	}
	submit("fixture_upload", "/uploads", map[string]any{"sources": []any{map[string]string{"type": "local", "root_id": "p5live", "relative_path": "source"}}, "target_dir": runDir + "/writes", "options": map[string]any{"skip_rapid_check": true, "parallel_per_file": 1, "file_concurrency": 1, "max_retries": 0}})
	source := runDir + "/writes/source"
	copyPath := runDir + "/writes/copied"
	copyBody := map[string]any{"items": []any{map[string]string{"from": source, "to": copyPath}}, "conflict_policy": "fail"}
	submit("FIL09_copy", "/file-copies", copyBody)
	copyBody["conflict_policy"] = "skip"
	submit("FIL09_skip", "/file-copies", copyBody)
	// Distinct content ensures overwrite cannot pass by silently keeping the target.
	if err = os.WriteFile(filepath.Join(root, "replacement"), []byte("P5 distinct replacement fixture\n"), 0600); err != nil {
		t.Fatal("cannot create replacement")
	}
	submit("replacement_upload", "/uploads", map[string]any{"sources": []any{map[string]string{"type": "local", "root_id": "p5live", "relative_path": "replacement"}}, "target_dir": runDir + "/writes", "options": map[string]any{"skip_rapid_check": true, "parallel_per_file": 1, "file_concurrency": 1, "max_retries": 0}})
	copyBody["items"] = []any{map[string]string{"from": runDir + "/writes/replacement", "to": copyPath}}
	copyBody["conflict_policy"] = "overwrite"
	submit("FIL09_overwrite", "/file-copies", copyBody)
	visible := call("FIL09_overwrite_verify", "GET", base+"/files/metadata?path="+url.QueryEscape(copyPath), nil, 200, nil)
	if dataMap(t, visible)["size_bytes"] != fmt.Sprint(len("P5 distinct replacement fixture\n")) {
		stages["FIL09_overwrite_verify"] = "CONTENT_SIZE_MISMATCH"
		t.Fatal("overwritten content differs")
	}
	moved := runDir + "/writes/moved"
	submit("FIL10_move", "/file-moves", map[string]any{"items": []any{map[string]string{"from": copyPath, "to": moved}}})
	renamed := runDir + "/writes/renamed"
	submit("FIL11_rename", "/file-renames", map[string]string{"path": moved, "target_path": renamed})
	share := call("SHR01_create", "POST", base+"/shares", mustJSON(map[string]any{"paths": []string{source}, "password": "a1B2", "period_days": 3, "combined_link": true}), 201, map[string]string{"Idempotency-Key": "p5-live-new-share"})
	shareData := dataMap(t, share)
	shareID := shareData["share_id"].(string)
	cancelled := false
	defer func() {
		if !cancelled {
			cleanupCtx, done := context.WithTimeout(context.Background(), 30*time.Second)
			defer done()
			g, e := clients.OpenShares(cleanupCtx, configuration.NewSnapshot(1, cfg), c)
			if e != nil {
				stages["share_cleanup"] = "CLIENT_BINDING_FAILED"
				t.Error("cannot bind share cleanup")
				return
			}
			defer g.Close()
			if e = g.Cancel(shareID); e != nil {
				stages["share_cleanup"] = safeUpstreamCode(e)
				t.Error("new share cleanup failed")
			} else {
				stages["share_cleanup"] = "passed"
			}
		}
	}()
	call("SHR03_detail", "GET", base+"/shares/"+shareID, nil, 200, nil)
	// Own-share transfer may be rejected by Baidu. Record the real boundary without
	// weakening the controlled protocol tests or touching any existing resource.
	sharedText := shareData["link"].(string) + " 提取码：a1B2"
	inspectReq := httptest.NewRequest("POST", base+"/share-inspections", bytes.NewReader(mustJSON(map[string]any{"input": sharedText, "page_size": 10}))).WithContext(ctx)
	inspectReq.Header.Set("Authorization", "Bearer "+testToken)
	inspectReq.Header.Set("Content-Type", "application/json")
	inspectRec := httptest.NewRecorder()
	service.Handler.ServeHTTP(inspectRec, inspectReq)
	if inspectRec.Code == 200 {
		stages["TRF01_inspect"] = "passed"
		inspection := dataMap(t, inspectRec)["inspection_id"].(string)
		transferRec := call("TRF02_submit", "POST", base+"/share-transfers", mustJSON(map[string]any{"share": map[string]string{"inspection_id": inspection}, "target_dir": runDir + "/writes/empty"}), 202, nil)
		j := dataMap(t, transferRec)["job_id"].(string)
		for ctx.Err() == nil {
			got := call("TRF02_query", "GET", "/api/v1/jobs/"+j, nil, 200, nil)
			state := dataMap(t, got)["state"].(string)
			if state == "succeeded" {
				stages["TRF02_transfer"] = "passed"
				break
			}
			if state == "failed" || state == "partially_succeeded" || state == "interrupted" {
				stages["TRF02_transfer"] = "UPSTREAM_" + state
				result := call("TRF02_item_query", "GET", "/api/v1/jobs/"+j+"/items?page_size=10", nil, 200, nil)
				var summary struct {
					Data []struct{ Problem *struct{ Code string } }
				}
				json.Unmarshal(result.Body.Bytes(), &summary)
				if len(summary.Data) > 0 && summary.Data[0].Problem != nil {
					report["transfer_rejection_code"] = summary.Data[0].Problem.Code
				}
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
	} else {
		var problem struct{ Code, Title string }
		json.Unmarshal(inspectRec.Body.Bytes(), &problem)
		switch problem.Title {
		case "百度文件元数据无效":
			report["inspection_diagnostic"] = "FILE_ENTRY_INVALID"
		case "百度文件字段超出有效范围":
			report["inspection_diagnostic"] = "FILE_FIELD_RANGE"
		case "百度响应格式异常":
			report["inspection_diagnostic"] = "JSON_PROTOCOL"
		}
		stages["TRF01_inspect"] = fmt.Sprintf("HTTP_%d_%s", inspectRec.Code, problem.Code)
		stages["TRF02_transfer"] = "not_run_inspection_unavailable"
	}
	submit("SHR04_cancel", "/share-cancellations", map[string]any{"share_ids": []string{shareID}})
	cancelled = true
	stages["share_cleanup"] = "passed"
	submit("FIL08_delete", "/file-deletions", map[string]any{"selector": map[string]any{"kind": "paths", "values": []string{renamed, runDir + "/writes/does-not-exist"}}, "ignore_missing": true})
	report["excluded_real_operations"] = []string{"SHR02 account-wide list", "OFF01-OFF07: no account-wide task interaction or arbitrary remote fetch", "REC01-REC04: no account-wide recycle interaction", "JOB10 destructive response loss: controlled only"}
}

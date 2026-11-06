package bootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/infrastructure/identity"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/infrastructure/localfs"
)

func p5Runtime(t *testing.T) (*Runtime, Options, *writeUpstream, string) {
	t.Helper()
	options, _ := testOptions(t)
	data, _ := os.ReadFile(options.ConfigFile)
	var config startup
	json.Unmarshal(data, &config)
	root := t.TempDir()
	config.Roots = []localfs.Root{{ID: "p5root", Path: root, Read: true, Write: true}}
	for _, entry := range []struct {
		id, token string
		accounts  []string
	}{{"p5writer", p5WriterToken, []string{"*"}}, {"p5restricted", p5RestrictedToken, []string{"acct_denied"}}} {
		sum := sha256.Sum256([]byte(entry.token))
		config.Principals = append(config.Principals, identity.Record{ID: entry.id, TokenSHA256: hex.EncodeToString(sum[:]), Scopes: []string{"write", "transfer"}, Accounts: entry.accounts, Roots: []string{}})
	}
	data, _ = json.Marshal(config)
	if err := os.WriteFile(options.ConfigFile, data, 0600); err != nil {
		t.Fatal(err)
	}
	u := newWriteUpstream()
	options.ClientTransport = p3Transport(t, u)
	runtime, err := New(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runtime.Close() })
	return runtime, options, u, root
}
func p5JSON(v any) string { raw, _ := json.Marshal(v); return string(raw) }
func p5Submit(t *testing.T, h http.Handler, rows *[]p3Response, id, base, route, body string) string {
	t.Helper()
	r := p3Wire(t, h, id, "POST", base+route, body, nil, 202, rows)
	return dataMap(t, r)["job_id"].(string)
}
func p5Export(t *testing.T, name string, rows []p3Response) {
	if file := os.Getenv(name); file != "" {
		data, _ := json.MarshalIndent(rows, "", "  ")
		if err := os.WriteFile(file, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
func TestP5FileWritesPartialConflictsAndRetry(t *testing.T) {
	runtime, _, u, _ := p5Runtime(t)
	h := runtime.Handler
	id := p3Account(t, h, "WRITE_OWNER", nil)
	base := "/api/v1/accounts/" + id
	u.seed("WRITE_OWNER", "/round/source", "source-data", false)
	u.seed("WRITE_OWNER", "/round/existing", "old", false)
	var rows []p3Response
	job := p5Submit(t, h, &rows, "FIL07", base, "/directories", `{"paths":["/round/new","/round/new/empty"]}`)
	p3Wait(t, h, job, "succeeded")
	job = p5Submit(t, h, &rows, "FIL09", base, "/file-copies", `{"items":[{"from":"/round/source","to":"/round/copy"},{"from":"/round/missing","to":"/round/missing-copy"}],"conflict_policy":"fail"}`)
	p3Wait(t, h, job, "partially_succeeded")
	result := p3Wire(t, h, "JOB04", "GET", "/api/v1/jobs/"+job+"/result", "", nil, 200, &rows)
	if !strings.Contains(result.Body.String(), `"failed":"1"`) || !strings.Contains(result.Body.String(), `"remote_effect":"confirmed"`) {
		t.Fatal("partial result missing")
	}
	items := p3Wire(t, h, "JOB03", "GET", "/api/v1/jobs/"+job+"/items?page_size=10", "", nil, 200, &rows)
	if !strings.Contains(items.Body.String(), "REMOTE_FILE_NOT_FOUND") {
		t.Fatal("per-item failure missing")
	}
	retry := p3Wire(t, h, "JOB09", "POST", "/api/v1/jobs/"+job+"/retry", `{"scope":"failed"}`, nil, 202, &rows)
	retryID := dataMap(t, retry)["job_id"].(string)
	if retryID == job {
		t.Fatal("retry reused original job")
	}
	p3Wait(t, h, retryID, "failed")
	for _, policy := range []string{"skip", "overwrite"} {
		j := p5Submit(t, h, nil, "FIL09", base, "/file-copies", fmt.Sprintf(`{"items":[{"from":"/round/source","to":"/round/existing"}],"conflict_policy":%q}`, policy))
		p3Wait(t, h, j, "succeeded")
	}
	u.mu.Lock()
	if string(u.files["WRITE_OWNER\x00/round/existing"].Body) != "source-data" {
		t.Fatal("overwrite ignored")
	}
	u.mu.Unlock()
	job = p5Submit(t, h, &rows, "FIL10", base, "/file-moves", `{"items":[{"from":"/round/copy","to":"/round/moved"}]}`)
	p3Wait(t, h, job, "succeeded")
	job = p5Submit(t, h, &rows, "FIL11", base, "/file-renames", `{"path":"/round/moved","target_path":"/round/renamed"}`)
	p3Wait(t, h, job, "succeeded")
	job = p5Submit(t, h, &rows, "FIL08", base, "/file-deletions", `{"selector":{"kind":"paths","values":["/round/renamed","/round/absent"]},"ignore_missing":true}`)
	p3Wait(t, h, job, "succeeded")
	p3Wire(t, h, "FIL11", "POST", base+"/file-renames", `{"path":"/round/source","target_path":"/elsewhere/name"}`, nil, 400, nil)
	selectedID := u.seed("WRITE_OWNER", "/round/by-id", "selected", false)
	selectedJob := p5Submit(t, h, nil, "FIL08", base, "/file-deletions", p5JSON(map[string]any{"selector": map[string]any{"kind": "fs_ids", "values": []string{strconv.FormatInt(selectedID, 10), "998877"}}}))
	p3Wait(t, h, selectedJob, "partially_succeeded")
	selectedItems := p3Wire(t, h, "JOB03", "GET", "/api/v1/jobs/"+selectedJob+"/items?page_size=10", "", nil, 200, &rows)
	if !strings.Contains(selectedItems.Body.String(), `"input":"998877"`) || !strings.Contains(selectedItems.Body.String(), `"fs_id":"`+strconv.FormatInt(selectedID, 10)+`"`) {
		t.Fatal("file-ID selection lost per-input result")
	}
	p5Export(t, "BAIDUPCS_P5_FILE_RESPONSES", rows)
}
func TestP5SharesInspectionPaginationCollectAndDownloadChild(t *testing.T) {
	runtime, _, u, root := p5Runtime(t)
	h := runtime.Handler
	id := p3Account(t, h, "WRITE_OWNER", nil)
	base := "/api/v1/accounts/" + id
	u.seed("WRITE_OWNER", "/round/source", "source", false)
	var rows []p3Response
	created := p3Wire(t, h, "SHR01", "POST", base+"/shares", `{"paths":["/round/source"],"password":"a1B2","period_days":37,"combined_link":true}`, map[string]string{"Idempotency-Key": "share-once"}, 201, &rows)
	shareID := dataMap(t, created)["share_id"].(string)
	if !strings.Contains(created.Body.String(), "pwd=a1B2") {
		t.Fatal("combined link ignored")
	}
	if u.period != 37 {
		t.Fatal("period clamped")
	}
	p3Wire(t, h, "SHR01", "POST", base+"/shares", `{"paths":["/round/source"],"password":"bad"}`, nil, 400, nil)
	p3Wire(t, h, "SHR01", "POST", base+"/shares", `{"paths":["/round/source"],"period_days":99999}`, nil, 422, nil)
	p3Wire(t, h, "TRF02", "POST", base+"/share-transfers", `{"share":{"input":"https://pan.baidu.com/s/1fixture"},"target_dir":"/round","options":{"randomize_name":true}}`, nil, 422, nil)
	p3Wire(t, h, "SHR02", "GET", base+"/shares?page=1&include_passwords=true", "", nil, 200, &rows)
	p3Wire(t, h, "SHR03", "GET", base+"/shares/"+shareID, "", nil, 200, &rows)
	inspected := p3Wire(t, h, "TRF01", "POST", base+"/share-inspections", `{"input":"通过网盘分享的文件 链接: https://pan.baidu.com/s/1fixture 提取码：a1B2","page_size":2}`, nil, 200, &rows)
	inspectionID := dataMap(t, inspected)["inspection_id"].(string)
	var decoded struct {
		Meta struct {
			Pagination struct {
				Next string `json:"next_cursor"`
				More bool   `json:"has_more"`
			}
		}
	}
	json.Unmarshal(inspected.Body.Bytes(), &decoded)
	if !decoded.Meta.Pagination.More {
		t.Fatal("inspection only read first batch")
	}
	p3Wire(t, h, "TRF01", "POST", base+"/share-inspections", p5JSON(map[string]any{"inspection_id": inspectionID, "page_size": 2, "cursor": decoded.Meta.Pagination.Next}), nil, 200, &rows)
	nested := p3Wire(t, h, "TRF01", "POST", base+"/share-inspections", p5JSON(map[string]any{"inspection_id": inspectionID, "directory_fs_id": "777", "page_size": 1}), nil, 200, &rows)
	if !strings.Contains(nested.Body.String(), "20000") {
		t.Fatal("nested directory unavailable")
	}
	p3Wire(t, h, "TRF01", "POST", base+"/share-inspections", `{"input":"https://pan.baidu.com/s/1fixture","password":"xxxx"}`, nil, 422, nil)
	p3Wire(t, h, "TRF02", "POST", base+"/share-transfers", p5JSON(map[string]any{"share": map[string]string{"inspection_id": inspectionID}, "target_dir": "/round", "fs_ids": []string{"999"}}), nil, 403, nil)
	body := map[string]any{"share": map[string]string{"inspection_id": inspectionID}, "target_dir": "/round", "fs_ids": []string{"10204", "20000"}, "options": map[string]any{"collect": true, "download_after": true, "download_destination": map[string]string{"root_id": "p5root", "directory": "child"}, "download_options": map[string]any{"mode": "pcs", "parallel": 1, "file_concurrency": 1, "max_retries": 0, "verify_checksum": true}}}
	job := p5Submit(t, h, &rows, "TRF02", base, "/share-transfers", p5JSON(body))
	done := p3Wait(t, h, job, "succeeded")
	children := dataMap(t, done)["child_job_ids"].([]any)
	if len(children) != 1 {
		t.Fatal("no download child")
	}
	p3Wait(t, h, children[0].(string), "succeeded")
	u.mu.Lock()
	ids := append([]int64{}, u.transferIDs...)
	u.mu.Unlock()
	if len(ids) != 2 || ids[0] != 10204 || ids[1] != 20000 {
		t.Fatal("selected fs_ids truncated or replaced")
	}
	files := 0
	filepath.WalkDir(filepath.Join(root, "child"), func(p string, e os.DirEntry, err error) error {
		if err == nil && !e.IsDir() {
			files++
		}
		return nil
	})
	if files != 2 {
		t.Fatal("download child omitted transferred paths")
	}
	job = p5Submit(t, h, &rows, "SHR04", base, "/share-cancellations", p5JSON(map[string]any{"share_ids": []string{shareID}}))
	p3Wait(t, h, job, "succeeded")
	p5Export(t, "BAIDUPCS_P5_SHARE_RESPONSES", rows)
}
func TestP5OfflineWindowIDsAndRecycleActualRestoreIDs(t *testing.T) {
	runtime, _, u, _ := p5Runtime(t)
	h := runtime.Handler
	id := p3Account(t, h, "WRITE_OWNER", nil)
	base := "/api/v1/accounts/" + id
	var rows []p3Response
	job := p5Submit(t, h, &rows, "OFF01", base, "/offline-tasks", `{"source_urls":["magnet:?xt=urn:btih:fixture"],"save_path":"/round/off"}`)
	p3Wait(t, h, job, "succeeded")
	result := p3Wire(t, h, "JOB04", "GET", "/api/v1/jobs/"+job+"/result", "", nil, 200, &rows)
	items := dataMap(t, result)["result"].(map[string]any)["items"].([]any)
	offlineID := items[0].(map[string]any)["offline_task_id"].(string)
	if offlineID == job {
		t.Fatal("upstream ID reused local job ID")
	}
	p3Wire(t, h, "OFF02", "GET", base+"/offline-tasks?page_size=1", "", nil, 200, &rows)
	p3Wire(t, h, "OFF03", "POST", base+"/offline-tasks/query", p5JSON(map[string]any{"offline_task_ids": []string{offlineID, "999"}}), nil, 200, &rows)
	p3Wire(t, h, "OFF07", "GET", base+"/offline-tasks/"+offlineID, "", nil, 200, &rows)
	ids := []string{}
	for i := 1; i <= 101; i++ {
		ids = append(ids, strconv.Itoa(i))
	}
	u.mu.Lock()
	before := u.calls["query_task"]
	u.mu.Unlock()
	p3Wire(t, h, "OFF03", "POST", base+"/offline-tasks/query", p5JSON(map[string]any{"offline_task_ids": ids}), nil, 413, &rows)
	u.mu.Lock()
	if u.calls["query_task"] != before {
		t.Fatal("101 IDs reached upstream")
	}
	u.mu.Unlock()
	hundred := p3Wire(t, h, "OFF03", "POST", base+"/offline-tasks/query", p5JSON(map[string]any{"offline_task_ids": ids[:100]}), nil, 200, nil)
	if got := dataMap(t, hundred)["items"].([]any); len(got) != 100 {
		t.Fatal("missing per-input results for 100 IDs", len(got))
	}
	job = p5Submit(t, h, &rows, "OFF04", base, "/offline-tasks/cancellations", p5JSON(map[string]any{"offline_task_ids": []string{offlineID}}))
	p3Wait(t, h, job, "succeeded")
	job = p5Submit(t, h, &rows, "OFF05", base, "/offline-tasks/deletions", p5JSON(map[string]any{"offline_task_ids": []string{offlineID}}))
	p3Wait(t, h, job, "succeeded")
	job = p5Submit(t, h, &rows, "OFF06", base, "/offline-tasks/clear", "")
	p3Wait(t, h, job, "succeeded")
	originalID := u.seed("WRITE_OWNER", "/round/to-restore", "restore", false)
	job = p5Submit(t, h, nil, "FIL08", base, "/file-deletions", `{"selector":{"kind":"paths","values":["/round/to-restore"]}}`)
	p3Wait(t, h, job, "succeeded")
	listed := p3Wire(t, h, "REC01", "GET", base+"/recycle?page=1", "", nil, 200, &rows)
	if !strings.Contains(listed.Body.String(), `"upstream_left_time":"-17"`) || !strings.Contains(listed.Body.String(), `"expires_at":null`) {
		t.Fatal("leftTime unit invented")
	}
	job = p5Submit(t, h, &rows, "REC02", base, "/recycle/restorations", p5JSON(map[string]any{"fs_ids": []string{strconv.FormatInt(originalID, 10)}}))
	p3Wait(t, h, job, "succeeded")
	restored := p3Wire(t, h, "JOB04", "GET", "/api/v1/jobs/"+job+"/result", "", nil, 200, &rows)
	restoredID := dataMap(t, restored)["result"].(map[string]any)["items"].([]any)[0].(map[string]any)["fs_id"].(string)
	if restoredID == strconv.FormatInt(originalID, 10) {
		t.Fatal("restore fabricated input ID instead of actual output")
	}
	job = p5Submit(t, h, nil, "FIL08", base, "/file-deletions", `{"selector":{"kind":"paths","values":["/round/to-restore"]}}`)
	p3Wait(t, h, job, "succeeded")
	job = p5Submit(t, h, &rows, "REC03", base, "/recycle/deletions", p5JSON(map[string]any{"fs_ids": []string{restoredID}}))
	p3Wait(t, h, job, "succeeded")
	job = p5Submit(t, h, &rows, "REC04", base, "/recycle/clear", "")
	p3Wait(t, h, job, "succeeded")
	p5Export(t, "BAIDUPCS_P5_BUSINESS_RESPONSES", rows)
}
func TestP5UnknownWriteReconciliationRestartAndNewCredentials(t *testing.T) {
	runtime, options, u, _ := p5Runtime(t)
	h := runtime.Handler
	id := p3Account(t, h, "WRITE_OWNER", nil)
	base := "/api/v1/accounts/" + id
	u.seed("WRITE_OWNER", "/round/source", "proof", false)
	u.mu.Lock()
	u.drop = "copy"
	u.mu.Unlock()
	var rows []p3Response
	job := p5Submit(t, h, nil, "FIL09", base, "/file-copies", `{"items":[{"from":"/round/source","to":"/round/unknown"}]}`)
	p3Wait(t, h, job, "interrupted")
	p3Wire(t, h, "JOB06", "POST", "/api/v1/jobs/"+job+"/cancel", `{}`, nil, 409, &rows)
	p3Wire(t, h, "JOB09", "POST", "/api/v1/jobs/"+job+"/retry", `{"scope":"failed"}`, nil, 409, nil)
	before, _ := runtime.store.Transfer(context.Background(), job)
	p3Wire(t, h, "JOB10", "POST", "/api/v1/jobs/"+job+"/reconcile", `{"dry_run":true}`, map[string]string{"Idempotency-Key": "audit-dry"}, 202, &rows)
	after, _ := runtime.store.Transfer(context.Background(), job)
	if string(before.Checkpoint) != string(after.Checkpoint) || after.State != "interrupted" {
		t.Fatal("dry audit changed task")
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	h = reopened.Handler
	a, err := reopened.store.Account(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	p3Wire(t, h, "ACC06", "PATCH", base+"/credentials", `{"cookies":null}`, map[string]string{"If-Match": fmt.Sprintf(`"%d"`, a.Revision)}, 200, nil)
	a, _ = reopened.store.Account(context.Background(), id)
	p3Wire(t, h, "ACC06", "PATCH", base+"/credentials", `{"bduss":"DIFFERENT_OWNER"}`, map[string]string{"If-Match": fmt.Sprintf(`"%d"`, a.Revision)}, 200, nil)
	p3Wire(t, h, "JOB10", "POST", "/api/v1/jobs/"+job+"/reconcile", `{"dry_run":true}`, nil, 409, nil)
	a, _ = reopened.store.Account(context.Background(), id)
	p3Wire(t, h, "ACC06", "PATCH", base+"/credentials", `{"bduss":"WRITE_OWNER","stoken":"replacement"}`, map[string]string{"If-Match": fmt.Sprintf(`"%d"`, a.Revision)}, 200, nil)
	p3Wire(t, h, "JOB10", "POST", "/api/v1/jobs/"+job+"/reconcile", `{}`, map[string]string{"Idempotency-Key": "audit-apply"}, 202, &rows)
	p3Wait(t, h, job, "succeeded")
	u.mu.Lock()
	if u.calls["copy"] != 1 {
		t.Fatal("unknown write was replayed")
	}
	u.mu.Unlock()
	// Offline submissions with a lost ID are correlated only against a complete
	// before-window plus a unique new URL/path/time match. No source fetch occurs.
	u.mu.Lock()
	u.drop = "add_task"
	u.mu.Unlock()
	offline := p5Submit(t, h, nil, "OFF01", base, "/offline-tasks", `{"source_urls":["https://source.invalid/dedicated-fixture"],"save_path":"/round/off"}`)
	p3Wait(t, h, offline, "interrupted")
	p3Wire(t, h, "JOB10", "POST", "/api/v1/jobs/"+offline+"/reconcile", `{}`, nil, 202, nil)
	p3Wait(t, h, offline, "succeeded")
	u.mu.Lock()
	if u.calls["add_task"] != 1 {
		t.Fatal("unknown offline add was replayed")
	}
	u.mu.Unlock()
	p5Export(t, "BAIDUPCS_P5_RECONCILE_RESPONSES", rows)
}

package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const p5WriterToken = "P5_WRITE_ONLY_OWNER_TOKEN_0123456789abcdef"
const p5RestrictedToken = "P5_ACCOUNT_DENIED_TOKEN_0123456789abcdef"

func TestP5IdempotencySurvivesDynamicInspectionAndDeletedPattern(t *testing.T) {
	runtime, _, u, _ := p5Runtime(t)
	h := runtime.Handler
	id := p3Account(t, h, "WRITE_OWNER", nil)
	base := "/api/v1/accounts/" + id
	u.seed("WRITE_OWNER", "/round/source", "source", false)
	u.seed("WRITE_OWNER", "/round", "", true)
	shareBody := `{"paths":["/round/source"],"password":"a1B2","period_days":37}`
	headers := map[string]string{"Idempotency-Key": "share-replay"}
	a := p3Wire(t, h, "SHR01", "POST", base+"/shares", shareBody, headers, 201, nil)
	b := p3Wire(t, h, "SHR01", "POST", base+"/shares", shareBody, headers, 201, nil)
	if dataMap(t, a)["share_id"] != dataMap(t, b)["share_id"] {
		t.Fatal("share replay changed result")
	}
	p3Wire(t, h, "SHR01", "POST", base+"/shares", strings.Replace(shareBody, "37", "38", 1), headers, 409, nil)
	u.mu.Lock()
	if u.calls["share/pset"] != 1 {
		t.Fatal("share replay issued a second write")
	}
	u.drop = "share/pset"
	u.mu.Unlock()
	headers["Idempotency-Key"] = "unknown-share"
	p3Wire(t, h, "SHR01", "POST", base+"/shares", shareBody, headers, 409, nil)
	p3Wire(t, h, "SHR01", "POST", base+"/shares", shareBody, headers, 409, nil)
	u.mu.Lock()
	if u.calls["share/pset"] != 2 {
		t.Fatal("unknown share submission was replayed")
	}
	u.mu.Unlock()
	transferBody := `{"share":{"input":"https://pan.baidu.com/s/1fixture","password":"a1B2"},"target_dir":"/round","fs_ids":["10204"],"options":{"collect":false,"randomize_name":false}}`
	headers = map[string]string{"Idempotency-Key": "transfer-replay"}
	a = p3Wire(t, h, "TRF02", "POST", base+"/share-transfers", transferBody, headers, 202, nil)
	j := dataMap(t, a)["job_id"].(string)
	p3Wait(t, h, j, "succeeded")
	u.mu.Lock()
	before := u.calls["share/list"]
	u.mu.Unlock()
	b = p3Wire(t, h, "TRF02", "POST", base+"/share-transfers", transferBody, headers, 202, nil)
	if dataMap(t, b)["job_id"] != j {
		t.Fatal("transfer replay changed task")
	}
	u.mu.Lock()
	if u.calls["share/list"] != before || len(u.transferIDs) != 1 {
		t.Fatal("transfer replay re-inspected or wrote")
	}
	u.mu.Unlock()
	deleteBody := `{"selector":{"kind":"patterns","values":["/round/so*"]},"ignore_missing":false}`
	headers = map[string]string{"Idempotency-Key": "deleted-pattern"}
	a = p3Wire(t, h, "FIL08", "POST", base+"/file-deletions", deleteBody, headers, 202, nil)
	j = dataMap(t, a)["job_id"].(string)
	p3Wait(t, h, j, "succeeded")
	b = p3Wire(t, h, "FIL08", "POST", base+"/file-deletions", deleteBody, headers, 202, nil)
	if dataMap(t, b)["job_id"] != j {
		t.Fatal("deletion replay re-resolved the now-empty pattern")
	}
}

func TestP5Offline1000WindowAndNeverFetchSource(t *testing.T) {
	runtime, _, u, _ := p5Runtime(t)
	h := runtime.Handler
	id := p3Account(t, h, "WRITE_OWNER", nil)
	base := "/api/v1/accounts/" + id
	u.mu.Lock()
	for i := 1; i <= 1001; i++ {
		id := strconv.Itoa(i)
		u.offline["WRITE_OWNER\x00"+id] = map[string]string{"task_id": id, "status": "1", "file_size": "0", "finished_size": "0", "source_url": "magnet:?fixture", "save_path": "/round"}
	}
	u.mu.Unlock()
	first := p3Wire(t, h, "OFF02", "GET", base+"/offline-tasks?page_size=500", "", nil, 200, nil)
	var page struct {
		Data []any
		Meta struct {
			Pagination struct {
				Next string `json:"next_cursor"`
				More bool   `json:"has_more"`
			}
			Window struct {
				Complete bool `json:"complete_history"`
				Limit    int
			} `json:"upstream_window"`
		}
	}
	if err := json.Unmarshal(first.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Data) != 500 || !page.Meta.Pagination.More || page.Meta.Window.Complete || page.Meta.Window.Limit != 1000 {
		t.Fatal("fixed window misrepresented")
	}
	second := p3Wire(t, h, "OFF02", "GET", base+"/offline-tasks?page_size=500&cursor="+page.Meta.Pagination.Next, "", nil, 200, nil)
	if err := json.Unmarshal(second.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Data) != 500 || page.Meta.Pagination.More {
		t.Fatal("invented additional upstream page")
	}
	u.mu.Lock()
	if u.calls["list_task"] != 1 {
		t.Fatal("local cursor incorrectly re-queried upstream")
	}
	u.mu.Unlock()
	var requests atomic.Int32
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	defer source.Close()
	j := p5Submit(t, h, nil, "OFF01", base, "/offline-tasks", p5JSON(map[string]any{"source_urls": []string{source.URL + "/must-not-fetch"}, "save_path": "/round"}))
	p3Wait(t, h, j, "succeeded")
	if requests.Load() != 0 {
		t.Fatal("service fetched offline source")
	}
	u.mu.Lock()
	u.drop = "add_task"
	u.mu.Unlock()
	unknown := p5Submit(t, h, nil, "OFF01", base, "/offline-tasks", `{"source_urls":["magnet:?xt=urn:btih:incomplete-window"],"save_path":"/round"}`)
	p3Wait(t, h, unknown, "interrupted")
	p3Wire(t, h, "JOB10", "POST", "/api/v1/jobs/"+unknown+"/reconcile", `{"dry_run":true}`, nil, 409, nil)
	u.mu.Lock()
	before := u.calls["add_task"]
	u.mu.Unlock()
	p3Wire(t, h, "JOB10", "POST", "/api/v1/jobs/"+unknown+"/reconcile", `{}`, nil, 202, nil)
	p3Wait(t, h, unknown, "interrupted")
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.calls["add_task"] != before {
		t.Fatal("incomplete-window reconciliation replayed a write")
	}
}

func TestP5CancellationKeepsRemoteEffectsAndQueuedItems(t *testing.T) {
	runtime, _, u, _ := p5Runtime(t)
	h := runtime.Handler
	id := p3Account(t, h, "WRITE_OWNER", nil)
	base := "/api/v1/accounts/" + id
	idB := p3Account(t, h, "OTHER_OWNER", nil)
	baseB := "/api/v1/accounts/" + idB
	u.seed("OTHER_OWNER", "/round/source", "proof", false)
	u.seed("WRITE_OWNER", "/round/source", "proof", false)
	u.mu.Lock()
	u.blockCopies = true
	u.mu.Unlock()
	defer close(u.copyRelease)
	submit := func(accountBase, name string) string {
		return p5Submit(t, h, nil, "FIL09", accountBase, "/file-copies", fmt.Sprintf(`{"items":[{"from":"/round/source","to":"/round/%s"},{"from":"/round/source","to":"/round/%s-unsubmitted"}]}`, name, name))
	}
	a := submit(base, "blocking-a")
	select {
	case <-u.copyEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("A not submitting")
	}
	b := submit(baseB, "blocking-b")
	select {
	case <-u.copyEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("B not submitting")
	}
	queued := submit(base, "queued")
	p3Wire(t, h, "JOB06", "POST", "/api/v1/jobs/"+queued+"/cancel", `{}`, nil, 202, nil)
	p3Wait(t, h, queued, "cancelled")
	items, _ := runtime.store.TransferItems(context.Background(), queued, 100)
	if len(items) != 2 || items[0].State != "cancelled" || items[0].RemoteEffect != "none" || items[1].State != "cancelled" {
		t.Fatal("queued cancellation omitted no-effect outcomes")
	}
	p3Wire(t, h, "JOB06", "POST", "/api/v1/jobs/"+a+"/cancel", `{}`, nil, 202, nil)
	p3Wait(t, h, a, "interrupted")
	state, _ := runtime.store.Transfer(context.Background(), b)
	if state.State != "running" {
		t.Fatal("cancelling A disturbed B")
	}
	u.mu.Lock()
	_, changed := u.files["WRITE_OWNER\x00/round/blocking-a"]
	_, notSubmitted := u.files["WRITE_OWNER\x00/round/blocking-a-unsubmitted"]
	u.mu.Unlock()
	if !changed || notSubmitted {
		t.Fatal("cancel invented rollback or submitted another unit")
	}
	// A persisted interrupted state can become visible before the original
	// worker's deferred target/slot cleanup ends. JOB10 correctly rejects that
	// short draining window; retry only this explicit non-mutating conflict.
	deadline := time.Now().Add(5 * time.Second)
	for {
		reply := request(t, h, "POST", "/api/v1/jobs/"+a+"/reconcile", testToken, "", "", `{}`)
		if reply.Code == 202 {
			break
		}
		if reply.Code != 409 || !strings.Contains(reply.Body.String(), "原执行器仍在排空") || time.Now().After(deadline) {
			wantStatus(t, reply, 202)
		}
		time.Sleep(10 * time.Millisecond)
	}
	p3Wait(t, h, a, "partially_succeeded")
	items, _ = runtime.store.TransferItems(context.Background(), a, 100)
	if len(items) != 2 || items[0].State != "succeeded" || items[1].State != "cancelled" {
		t.Fatal("reconcile retried an unsubmitted unit")
	}
	p3Wire(t, h, "JOB06", "POST", "/api/v1/jobs/"+b+"/cancel", `{}`, nil, 202, nil)
	p3Wait(t, h, b, "interrupted")
}

func TestP5WriteOwnershipAllowlistAndReadScope(t *testing.T) {
	runtime, _, u, _ := p5Runtime(t)
	h := runtime.Handler
	id := p3Account(t, h, "WRITE_OWNER", nil)
	base := "/api/v1/accounts/" + id
	u.seed("WRITE_OWNER", "/round/source", "proof", false)
	adminJob := p5Submit(t, h, nil, "FIL07", base, "/directories", `{"paths":["/round/admin-owned"]}`)
	p3Wait(t, h, adminJob, "succeeded")
	p3Wire(t, h, "JOB02", "GET", "/api/v1/jobs/"+adminJob, "", map[string]string{"Authorization": "Bearer " + p5WriterToken}, 403, nil)
	body := `{"selector":{"kind":"paths","values":["/round/source"]}}`
	p3Wire(t, h, "FIL08", "POST", base+"/file-deletions", body, map[string]string{"Authorization": "Bearer " + p5RestrictedToken}, 403, nil)
	p3Wire(t, h, "REC04", "POST", base+"/recycle/clear", "", map[string]string{"Authorization": "Bearer " + readToken}, 403, nil)
	r := p3Wire(t, h, "FIL08", "POST", base+"/file-deletions", body, map[string]string{"Authorization": "Bearer " + p5WriterToken}, 202, nil)
	j := dataMap(t, r)["job_id"].(string)
	p3Wait(t, h, j, "succeeded")
	p3Wire(t, h, "JOB02", "GET", "/api/v1/jobs/"+j, "", map[string]string{"Authorization": "Bearer " + p5RestrictedToken}, 403, nil)
	p3Wire(t, h, "FIL01", "GET", base+"/files?path=/round", "", map[string]string{"Authorization": "Bearer " + p5WriterToken}, 403, nil)
}

func TestP5UnknownBaiduIdentityCannotAuthorizeNewCredentialAudit(t *testing.T) {
	runtime, _, u, _ := p5Runtime(t)
	h := runtime.Handler
	id := p3Account(t, h, "ZERO_OWNER", nil)
	base := "/api/v1/accounts/" + id
	u.seed("ZERO_OWNER", "/round/source", "proof", false)
	u.mu.Lock()
	u.drop = "copy"
	u.mu.Unlock()
	job := p5Submit(t, h, nil, "FIL09", base, "/file-copies", `{"items":[{"from":"/round/source","to":"/round/unknown"}]}`)
	p3Wait(t, h, job, "interrupted")
	a, err := runtime.store.Account(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	p3Wire(t, h, "ACC06", "PATCH", base+"/credentials", `{"stoken":"replacement"}`, map[string]string{"If-Match": fmt.Sprintf(`"%d"`, a.Revision)}, 200, nil)
	got := p3Wire(t, h, "JOB10", "POST", "/api/v1/jobs/"+job+"/reconcile", `{"dry_run":true}`, nil, 409, nil)
	if !strings.Contains(got.Body.String(), "RECONCILIATION_IDENTITY_UNVERIFIED") {
		t.Fatal("zero UK was treated as verified identity")
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.calls["copy"] != 1 {
		t.Fatal("identity failure replayed a write")
	}
}

func TestP5ShareTransferAuditDownloadsOnlyConfirmedItems(t *testing.T) {
	runtime, _, u, root := p5Runtime(t)
	h := runtime.Handler
	id := p3Account(t, h, "WRITE_OWNER", nil)
	base := "/api/v1/accounts/" + id
	u.seed("WRITE_OWNER", "/round/f-000", "existing conflict", false)
	u.mu.Lock()
	u.drop = "share/transfer"
	u.mu.Unlock()
	body := map[string]any{"share": map[string]string{"input": "https://pan.baidu.com/s/1fixture", "password": "a1B2"}, "target_dir": "/round", "fs_ids": []string{"10000", "10001"}, "options": map[string]any{"download_after": true, "download_destination": map[string]string{"root_id": "p5root", "directory": "audited-child"}, "download_options": map[string]any{"mode": "pcs", "parallel": 1, "file_concurrency": 1, "max_retries": 0, "verify_checksum": true}}}
	job := p5Submit(t, h, nil, "TRF02", base, "/share-transfers", p5JSON(body))
	p3Wait(t, h, job, "interrupted")
	p3Wire(t, h, "JOB10", "POST", "/api/v1/jobs/"+job+"/reconcile", `{}`, nil, 202, nil)
	done := p3Wait(t, h, job, "partially_succeeded")
	children := dataMap(t, done)["child_job_ids"].([]any)
	if len(children) != 1 {
		t.Fatal("missing audited download child")
	}
	p3Wait(t, h, children[0].(string), "succeeded")
	count := 0
	filepath.WalkDir(filepath.Join(root, "audited-child"), func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			count++
			if d.Name() != "f-001" {
				t.Error("child downloaded a failed target")
			}
		}
		return nil
	})
	if count != 1 {
		t.Fatal("child did not restrict to confirmed transfer")
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if len(u.transferIDs) != 1 || u.transferIDs[0] != 10001 {
		t.Fatal("reconcile resubmitted transfer")
	}
}

package bootstrap

import (
	"context"
	"encoding/json"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/transfer"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/infrastructure/baidu"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/infrastructure/persistence"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestP6ContextFreezesSelectionAndIdempotentReplay(t *testing.T) {
	rt, _, _, _, _ := p6Runtime(t)
	h := rt.Handler
	id := p3Account(t, h, "P6_OWNER", nil)
	other := p3Account(t, h, "DIFFERENT_OWNER", nil)
	base := "/api/v1/accounts/" + id
	created := p3Wire(t, h, "CTX01", "POST", "/api/v1/contexts", p5JSON(map[string]string{"account_id": id, "cwd": "/round"}), nil, 201, nil)
	cid := dataMap(t, created)["context_id"].(string)
	for _, request := range []struct{ method, route, body string }{{"GET", "/files?path=/&context_id=" + cid, ""}, {"POST", "/download-links", p5JSON(map[string]any{"context_id": cid, "selector": map[string]any{"kind": "fs_ids", "values": []string{"100"}}})}} {
		p3Wire(t, h, "FIL01", request.method, "/api/v1/accounts/"+other+request.route, request.body, nil, 409, nil)
	}
	p3Wire(t, h, "EXP01", "POST", base+"/exports", p5JSON(map[string]any{"paths": []string{"/round/file"}, "base_path": "/", "context_id": cid}), nil, 400, nil)
	body := p5JSON(map[string]any{"paths": []string{"file"}, "context_id": cid})
	first := p3Wire(t, h, "EXP01", "POST", base+"/exports", body, map[string]string{"Idempotency-Key": "freeze-export"}, 202, nil)
	job := dataMap(t, first)["job_id"].(string)
	p3Wire(t, h, "CTX03", "PATCH", "/api/v1/contexts/"+cid, `{"cwd":"/other"}`, map[string]string{"If-Match": `"1"`}, 200, nil)
	again := p3Wire(t, h, "EXP01", "POST", base+"/exports", body, map[string]string{"Idempotency-Key": "freeze-export"}, 202, nil)
	if dataMap(t, again)["job_id"] != job || !strings.Contains(again.Body.String(), "/round/file") {
		t.Fatal("context update changed accepted selection or replay")
	}
	p6Wait(t, h, job, "succeeded")
	p3Wire(t, h, "CTX03", "PATCH", "/api/v1/contexts/"+cid, `{"cwd":"/round"}`, nil, 428, nil)
	p3Wire(t, h, "CTX03", "PATCH", "/api/v1/contexts/"+cid, `{"cwd":"/round"}`, map[string]string{"If-Match": `W/"2"`}, 400, nil)
}
func TestP6RestartLosesKeyExplicitlyAndCleansUncommittedPlaintext(t *testing.T) {
	rt, o, _, root, _ := p6Runtime(t)
	dir := rt.DataDir
	if e := rt.Close(); e != nil {
		t.Fatal(e)
	}
	os.Mkdir(filepath.Join(root, "output"), 0700)
	os.WriteFile(filepath.Join(root, "source"), []byte("keep-source"), 0600)
	os.WriteFile(filepath.Join(root, "output/.http-tool-orphan"), []byte("partial private plaintext"), 0600)
	key, e := persistence.MasterKey(dir)
	if e != nil {
		t.Fatal(e)
	}
	repo, e := persistence.Open(context.Background(), dir, key, baidu.DefaultConfiguration())
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	record := transfer.Record{ID: "job_secret_restart", Owner: "admin", State: "queued", CreatedAt: now, UpdatedAt: now, Config: baidu.DefaultConfiguration(), ConfigRevision: 1, Request: transfer.Request{Kind: "local_encryption", Public: true, ToolPlan: `{"Units":[{"Local":{"root_id":"p6root","relative_path":"source"}}],"Destination":{"root_id":"p6root","directory":"output"},"Options":{"method":"aes-128-ctr","gzip":true,"remove_source":true},"KeyRef":"secret_previous_process"}`}, EffectiveOptions: json.RawMessage(`{"method":"aes-128-ctr","gzip":true,"remove_source":true}`)}
	if _, e = repo.CreateTransfer(context.Background(), record); e != nil {
		t.Fatal(e)
	}
	repo.TransitionTransfer(context.Background(), record.ID, []string{"queued"}, "running")
	if e = repo.SaveTransferCheckpoint(context.Background(), record.ID, json.RawMessage(`{"Phase":"local_writing","RootID":"p6root","Temp":"output/.http-tool-orphan","Target":"output/source.encrypt"}`)); e != nil {
		t.Fatal(e)
	}
	repo.Close()
	next, e := New(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	defer next.Close()
	read := p3Wire(t, next.Handler, "JOB02", "GET", "/api/v1/jobs/"+record.ID, "", nil, 200, nil)
	if !strings.Contains(read.Body.String(), "TEMPORARY_SECRET_LOST") || !strings.Contains(read.Body.String(), `"state":"interrupted"`) || strings.Contains(read.Body.String(), "secret_previous_process") {
		t.Fatal("missing key not explicit or key ref leaked")
	}
	if _, e = os.Stat(filepath.Join(root, "output/.http-tool-orphan")); !os.IsNotExist(e) {
		t.Fatal("restart left uncommitted plaintext")
	}
	if _, e = os.Stat(filepath.Join(root, "source")); e != nil {
		t.Fatal("restart removed source")
	}
	p3Wire(t, next.Handler, "JOB08", "POST", "/api/v1/jobs/"+record.ID+"/resume", `{}`, nil, 422, nil)
}
func TestP6LoginConcurrentCompleteUpdatesAccountAndKeepsOldVersion(t *testing.T) {
	rt, _, _, _, _ := p6Runtime(t)
	h := rt.Handler
	created := p3Wire(t, h, "ACC01", "POST", "/api/v1/accounts", `{"label":"keep-label","credentials":{"kind":"bduss","bduss":"P6_OWNER"}}`, nil, 201, nil)
	target := dataMap(t, created)["account_id"].(string)
	login := p3Wire(t, h, "LOG01", "POST", "/api/v1/account-login-sessions", `{"method":"password","username":"simple","password":"P6_TEST_PASSWORD"}`, nil, 201, nil)
	sid := dataMap(t, login)["session_id"].(string)
	route := "/api/v1/account-login-sessions/" + sid + "/complete"
	body := p5JSON(map[string]string{"account_id": target})
	results := make(chan int, 2)
	for i := 0; i < 2; i++ {
		go func() { response := request(t, h, "POST", route, testToken, "", "", body); results <- response.Code }()
	}
	for i := 0; i < 2; i++ {
		if code := <-results; code != 200 {
			t.Fatal("concurrent complete failed", code)
		}
	}
	account := p3Wire(t, h, "ACC03", "GET", "/api/v1/accounts/"+target, "", nil, 200, nil)
	v := dataMap(t, account)
	if v["credential_revision"] != "2" || v["label"] != "keep-label" {
		t.Fatal("completion wrote twice or erased label")
	}
	if _, e := rt.store.CredentialVersion(context.Background(), target, 1); e != nil {
		t.Fatal("normal login update revoked old version", e)
	}
	wrong := p3Account(t, h, "DIFFERENT_OWNER", nil)
	p3Wire(t, h, "ACC07", "POST", "/api/v1/accounts/"+wrong+"/validate", "", nil, 200, nil)
	another := p3Wire(t, h, "LOG01", "POST", "/api/v1/account-login-sessions", `{"method":"password","username":"simple","password":"P6_TEST_PASSWORD"}`, nil, 201, nil)
	p3Wire(t, h, "LOG05", "POST", "/api/v1/account-login-sessions/"+dataMap(t, another)["session_id"].(string)+"/complete", p5JSON(map[string]string{"account_id": wrong}), nil, 409, nil)
}
func TestP6CryptoIdempotencySurvivesRestartWithoutPersistingKey(t *testing.T) {
	rt, o, _, root, _ := p6Runtime(t)
	os.WriteFile(filepath.Join(root, "key-source"), []byte("preserved"), 0600)
	body := `{"sources":[{"root_id":"p6root","relative_path":"key-source"}],"destination":{"root_id":"p6root","directory":"key-output"},"options":{"method":"aes-192-ofb"},"key":"P6_ONLY_IN_PROCESS_SECRET"}`
	headers := map[string]string{"Idempotency-Key": "key-replay"}
	first := p3Wire(t, rt.Handler, "LOC02", "POST", "/api/v1/local/encryptions", body, headers, 202, nil)
	id := dataMap(t, first)["job_id"].(string)
	p6Wait(t, rt.Handler, id, "succeeded")
	rt.Close()
	next, e := New(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	defer next.Close()
	again := p3Wire(t, next.Handler, "LOC02", "POST", "/api/v1/local/encryptions", body, headers, 202, nil)
	if dataMap(t, again)["job_id"] != id {
		t.Fatal("restart changed idempotent job")
	}
	p3Wire(t, next.Handler, "LOC02", "POST", "/api/v1/local/encryptions", strings.ReplaceAll(body, "P6_ONLY_IN_PROCESS_SECRET", "different"), headers, 409, nil)
}

package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/content"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestP7MetricsAuthorizationBoundedLabelsAndNonblockingScrape(t *testing.T) {
	rt, _, _ := testRuntime(t)
	for _, tc := range []struct {
		token  string
		status int
	}{{"", 401}, {readToken, 403}, {testToken, 200}} {
		wantStatus(t, request(t, rt.Handler, "GET", "/metrics", tc.token, "", "", ""), tc.status)
	}
	const secret = "PRIVATE_SENTINEL_8bfa"
	for i := 0; i < 40; i++ {
		wantStatus(t, request(t, rt.Handler, "GET", fmt.Sprintf("/private-%s-%d?cookie=%s", secret, i, secret), testToken, "", "", ""), 404)
	}
	wantStatus(t, request(t, rt.Handler, "GET", "/metrics?url="+secret, testToken, "", "", ""), 400)
	// This end-to-end bound complements the held-executor-lock unit test.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- request(t, rt.Handler, "GET", "/metrics", testToken, "", "", "") }()
	var rec *httptest.ResponseRecorder
	select {
	case rec = <-done:
	case <-ctx.Done():
		t.Fatal("metrics scrape blocked")
	}
	wantStatus(t, rec, 200)
	if rec.Header().Get("Content-Type") != "text/plain; version=0.0.4" {
		t.Fatal(rec.Header())
	}
	text := rec.Body.String()
	for _, bad := range []string{secret, testToken, readToken, "account_id=", "job_id=", "owner=", "path=", "url=", "Cookie"} {
		if strings.Contains(text, bad) {
			t.Fatal("private metric label", bad)
		}
	}
	for _, want := range []string{`baidupcs_http_errors_total{operation="SYS07",class="4xx"} 3`, `baidupcs_http_requests_total{operation="unmatched",class="4xx"} 40`, "baidupcs_queue_capacity 16", "baidupcs_executor_capacity 2"} {
		if !strings.Contains(text, want) {
			t.Fatal("missing metric", want)
		}
	}
	if output := os.Getenv("BAIDUPCS_P7_METRICS_RESPONSES"); output != "" {
		rows := []p3Response{{ID: "SYS07", Method: "GET", Status: 200, ContentType: rec.Header().Get("Content-Type"), Headers: rec.Header(), Body: text}}
		b, e := json.MarshalIndent(rows, "", "  ")
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(output, b, 0600); e != nil {
			t.Fatal(e)
		}
	}
}

func TestP7TicketExpiryCurrentPermissionsAndCredentialRevocation(t *testing.T) {
	rt, o, _, _, _ := p6Runtime(t)
	ctx := context.Background()
	id := p3Account(t, rt.Handler, "P6_OWNER", nil)
	claim := content.Ticket{Owner: "p6other", AccountID: id, Path: "/round/file", CredentialRevision: 1, Expires: time.Now().Add(time.Minute).Unix()}
	issue := func(c content.Ticket) string {
		v, e := rt.store.IssueDownloadTicket(ctx, c, "")
		if e != nil {
			t.Fatal(e)
		}
		return "/api/v1/accounts/" + id + "/content?path=%2Fround%2Ffile&ticket=" + url.QueryEscape(v)
	}
	route := issue(claim)
	wantStatus(t, request(t, rt.Handler, "GET", route, "", "", "", ""), 200)
	expired := claim
	expired.Expires = time.Now().Add(-time.Second).Unix()
	wantStatus(t, request(t, rt.Handler, "HEAD", issue(expired), "", "", "", ""), 410)
	// Permissions are read from the current startup identity after restart, not
	// copied out of a ticket. Preserve owner but revoke its account allowlist.
	if e := rt.Close(); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(o.ConfigFile)
	if e != nil {
		t.Fatal(e)
	}
	var cfg startup
	json.Unmarshal(b, &cfg)
	for i := range cfg.Principals {
		if cfg.Principals[i].ID == "p6other" {
			cfg.Principals[i].Accounts = []string{}
		}
	}
	b, _ = json.Marshal(cfg)
	os.WriteFile(o.ConfigFile, b, 0600)
	next, e := New(ctx, o)
	if e != nil {
		t.Fatal(e)
	}
	defer next.Close()
	wantStatus(t, request(t, next.Handler, "GET", route, "", "", "", ""), 403)
	// Changing the credential revision independently invalidates an otherwise
	// unexpired claim. Clearing credentials also revokes old job bindings.
	account := request(t, next.Handler, "GET", "/api/v1/accounts/"+id, testToken, "", "", "")
	rev := dataMap(t, account)["revision"].(string)
	wantStatus(t, request(t, next.Handler, "PATCH", "/api/v1/accounts/"+id+"/credentials", testToken, `"`+rev+`"`, "", `{"cookies":null}`), 200)
	wantStatus(t, request(t, next.Handler, "GET", route, "", "", "", ""), 410)
}

func TestP7SettledJobCountersAndExpiredCredentialBoundary(t *testing.T) {
	rt, _, _, root, _ := p6Runtime(t)
	if e := os.WriteFile(filepath.Join(root, "metric-input"), []byte("fixture"), 0600); e != nil {
		t.Fatal(e)
	}
	j := p5Submit(t, rt.Handler, nil, "INT03", "", "/api/v1/local/checksums", `{"root_id":"p6root","paths":["metric-input"],"options":{"algorithms":["md5"]}}`)
	p6Wait(t, rt.Handler, j, "succeeded")
	deadline := time.Now().Add(time.Second)
	for {
		rec := request(t, rt.Handler, "GET", "/metrics", testToken, "", "", "")
		if strings.Contains(rec.Body.String(), `baidupcs_job_executions_settled_total{kind="local_checksum",state="succeeded"} 1`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("execution not counted")
		}
		time.Sleep(time.Millisecond)
	}
	// A valid API bearer stays valid when Baidu rejects an expired cookie.
	reads, _, _ := p3Runtime(t)
	expired := p3Account(t, reads.Handler, "EXPIRED", nil)
	wantStatus(t, request(t, reads.Handler, "GET", "/api/v1/accounts/"+expired+"/files?path=/", testToken, "", "", ""), 422)
	wantStatus(t, request(t, reads.Handler, "GET", "/api/v1/accounts/"+expired, testToken, "", "", ""), 200)
	wantStatus(t, request(t, reads.Handler, "GET", "/api/v1/jobs", testToken, "", "", ""), 200)
}

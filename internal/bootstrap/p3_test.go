package bootstrap

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/infrastructure/identity"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/infrastructure/localfs"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const p3OtherToken = "P3_OTHER_OWNER_TOKEN_0123456789abcdef"

type readUpstream struct {
	mu           sync.Mutex
	count        int
	fail, repeat bool
	searches     int
	delay        bool
	entered      chan struct{}
	release      chan struct{}
}

func readEntry(p string, index int, dir bool, owner string) map[string]any {
	id := int64(9007199254740993) + int64(index)
	if owner == "ACCOUNT_B" {
		id += 100000
	}
	return map[string]any{"fs_id": id, "path": p, "server_filename": path.Base(p), "isdir": map[bool]int{false: 0, true: 1}[dir], "size": map[bool]int{false: index + 1, true: 0}[dir], "server_ctime": 1700000000, "server_mtime": 1700000001, "ctime": 1700000000, "mtime": 1700000001, "md5": "d41d8cd98f00b204e9800998ecf8427e", "block_list": []string{}}
}
func (u *readUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	owner := ""
	if c, err := r.Cookie("BDUSS"); err == nil {
		owner = c.Value
	}
	respond := func(v any) { w.Header().Set("Content-Type", "application/json"); _ = json.NewEncoder(w).Encode(v) }
	if owner == "EXPIRED" {
		respond(map[string]any{"error_code": 31045, "errno": -6})
		return
	}
	if strings.Contains(r.URL.Path, "quota") {
		if owner == "" && r.URL.Query().Get("access_token") != "P3_TOKEN_ONLY" {
			respond(map[string]any{"error_code": 110})
			return
		}
		respond(map[string]any{"quota": int64(9007199254741999), "used": 1000})
		return
	}
	if r.URL.Path == "/api/user/getinfo" {
		respond(map[string]any{"errno": 0, "records": []any{map[string]any{"uk": uint64(9007199254741999), "uname": "p3-account"}}})
		return
	}
	method := r.URL.Query().Get("method")
	if method == "meta" {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			http.Error(w, "multipart", 400)
			return
		}
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
		}
		var param struct {
			List []struct {
				Path string `json:"path"`
			} `json:"list"`
		}
		_ = json.Unmarshal([]byte(r.FormValue("param")), &param)
		if len(param.List) != 1 {
			http.Error(w, "meta", 400)
			return
		}
		p := param.List[0].Path
		if p == "/missing" {
			respond(map[string]any{"error_code": 31066})
			return
		}
		dir := p == "/data" || p == "/empty" || p == "/" || p == "/bad" || p == "/slow"
		respond(map[string]any{"list": []any{readEntry(p, 1, dir, owner)}})
		return
	}
	if method == "search" {
		u.mu.Lock()
		u.searches++
		u.mu.Unlock()
		n := 5
		if r.URL.Query().Get("wd") == "huge" {
			n = 10001
		}
		list := []any{}
		for i := 0; i < n; i++ {
			list = append(list, readEntry(fmt.Sprintf("/data/file%04d", i), i, false, owner))
		}
		respond(map[string]any{"list": list})
		return
	}
	if r.URL.Path == "/api/list" {
		q := r.URL.Query()
		page, _ := strconv.Atoi(q.Get("page"))
		size, _ := strconv.Atoi(q.Get("num"))
		p := q.Get("dir")
		u.mu.Lock()
		count, fail, repeat, delay := u.count, u.fail, u.repeat, u.delay
		u.mu.Unlock()
		if p == "/slow" && delay {
			select {
			case u.entered <- struct{}{}:
			default:
			}
			select {
			case <-u.release:
			case <-r.Context().Done():
				return
			}
		}
		if p == "/empty" {
			count = 0
		}
		if p == "/bad" || fail && page == 2 {
			respond(map[string]any{"errno": -1})
			return
		}
		if repeat && page > 1 {
			page = 1
		}
		start := (page - 1) * size
		list := []any{}
		for i := start; i < min(start+size, count); i++ {
			list = append(list, readEntry(fmt.Sprintf("%s/file%04d", p, i), i, false, owner))
		}
		respond(map[string]any{"errno": 0, "list": list})
		return
	}
	http.Error(w, "unexpected operation", 400)
}
func p3Transport(t *testing.T, u http.Handler) func(configuration.Client) *http.Transport {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certificate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "p3-test"}, DNSNames: []string{"pan.baidu.com", "pcs.baidu.com", "passport.baidu.com", "wappass.baidu.com", "www.baidu.com", "api.ipify.org"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true, IsCA: true}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	upstream := httptest.NewUnstartedServer(u)
	upstream.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12}
	upstream.StartTLS()
	t.Cleanup(upstream.Close)
	return func(configuration.Client) *http.Transport {
		return &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}, DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, upstream.Listener.Addr().String())
		}}
	}
}
func p3Runtime(t *testing.T) (*Runtime, Options, *readUpstream) {
	t.Helper()
	o, _ := testOptions(t)
	data, err := os.ReadFile(o.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	var cfg startup
	_ = json.Unmarshal(data, &cfg)
	root := t.TempDir()
	cfg.Roots = []localfs.Root{{ID: "p3root", Path: root, Read: true, Write: true}}
	sum := sha256.Sum256([]byte(p3OtherToken))
	cfg.Principals = append(cfg.Principals, identity.Record{ID: "other", TokenSHA256: hex.EncodeToString(sum[:]), Scopes: []string{"read", "local.read"}, Accounts: []string{"*"}, Roots: []string{"p3root"}})
	data, _ = json.Marshal(cfg)
	if err = os.WriteFile(o.ConfigFile, data, 0600); err != nil {
		t.Fatal(err)
	}
	u := &readUpstream{count: 5, entered: make(chan struct{}, 1), release: make(chan struct{})}
	o.ClientTransport = p3Transport(t, u)
	runtime, err := New(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	return runtime, o, u
}

type p3Response struct {
	ID          string      `json:"operation_id"`
	Method      string      `json:"method"`
	Status      int         `json:"status"`
	ContentType string      `json:"content_type"`
	Headers     http.Header `json:"headers"`
	Body        any         `json:"body"`
}

func p3Wire(t *testing.T, h http.Handler, id, method, url, body string, headers map[string]string, status int, rows *[]p3Response) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, url, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testToken)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != status {
		t.Fatalf("%s %s: got %d want %d: %s", method, url, rec.Code, status, rec.Body.String())
	}
	if rows != nil {
		var b any
		if method != "HEAD" && status != 204 {
			if strings.Contains(rec.Header().Get("Content-Type"), "json") {
				if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
					t.Fatal(err)
				}
			} else {
				b = rec.Body.String()
			}
		}
		*rows = append(*rows, p3Response{id, method, status, strings.Split(rec.Header().Get("Content-Type"), ";")[0], rec.Header().Clone(), b})
	}
	return rec
}
func dataMap(t *testing.T, r *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var b struct{ Data map[string]any }
	if err := json.Unmarshal(r.Body.Bytes(), &b); err != nil {
		t.Fatal(err)
	}
	return b.Data
}
func p3Account(t *testing.T, h http.Handler, cookie string, rows *[]p3Response) string {
	t.Helper()
	r := p3Wire(t, h, "ACC01", "POST", "/api/v1/accounts", `{"credentials":{"kind":"bduss","bduss":"`+cookie+`"},"force_login_username":"fallback"}`, nil, 201, rows)
	return dataMap(t, r)["account_id"].(string)
}
func p3Wait(t *testing.T, h http.Handler, id string, states ...string) *httptest.ResponseRecorder {
	t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		r := p3Wire(t, h, "JOB02", "GET", "/api/v1/jobs/"+id, "", nil, 200, nil)
		state := dataMap(t, r)["state"].(string)
		for _, v := range states {
			if v == state {
				return r
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("job state deadline")
	return nil
}
func TestP3AccountsCredentialsQuotaAndVersions(t *testing.T) {
	runtime, options, _ := p3Runtime(t)
	h := runtime.Handler
	rows := []p3Response{}
	id := p3Account(t, h, "ACCOUNT_A", &rows)
	base := "/api/v1/accounts/" + id
	for _, field := range []string{"ptoken", "stoken", "sboxtkn", "baiduid"} {
		p3Wire(t, h, "ACC01", "POST", "/api/v1/accounts", `{"credentials":{"kind":"bduss","bduss":"ACCOUNT_A","`+field+`":""}}`, nil, 400, nil)
	}
	p3Wire(t, h, "ACC02", "GET", "/api/v1/accounts", "", nil, 200, &rows)
	p3Wire(t, h, "ACC03", "GET", base, "", nil, 200, &rows)
	p3Wire(t, h, "ACC04", "PATCH", base, `{"label":"changed"}`, nil, 428, &rows)
	h1 := map[string]string{"If-Match": `"1"`, "Idempotency-Key": "label"}
	changed := p3Wire(t, h, "ACC04", "PATCH", base, `{"label":"changed"}`, h1, 200, &rows)
	if changed.Header().Get("ETag") != `"2"` {
		t.Fatal("label did not increment revision")
	}
	p3Wire(t, h, "ACC04", "PATCH", base, `{"label":"other"}`, map[string]string{"If-Match": `"1"`}, 412, &rows)
	p3Wire(t, h, "ACC06", "PATCH", base+"/credentials", `{"access_token":"P3_TOKEN_ONLY","ptoken":"P3_TOKEN"}`, map[string]string{"If-Match": `"2"`, "Idempotency-Key": "credentials"}, 200, &rows)
	old, err := runtime.store.CredentialVersion(context.Background(), id, 1)
	if err != nil || old.Cookies["BDUSS"] != "ACCOUNT_A" {
		t.Fatal("normal update revoked bound tasks", err)
	}
	p3Wire(t, h, "ACC06", "PATCH", base+"/credentials", `{"cookies":null,"bduss":"x"}`, map[string]string{"If-Match": `"3"`}, 400, &rows)
	p3Wire(t, h, "ACC06", "PATCH", base+"/credentials", `{"stoken":""}`, map[string]string{"If-Match": `"3"`}, 400, &rows)
	p3Wire(t, h, "ACC06", "PATCH", base+"/credentials", `{"stoken":null,"stoken":"duplicate"}`, map[string]string{"If-Match": `"3"`}, 400, &rows)
	p3Wire(t, h, "ACC07", "POST", base+"/validate", "", nil, 200, &rows)
	// Validation changes account metadata revision but not credential revision.
	current := p3Wire(t, h, "ACC03", "GET", base, "", nil, 200, nil)
	rev := dataMap(t, current)["revision"].(string)
	cleared := p3Wire(t, h, "ACC06", "PATCH", base+"/credentials", `{"cookies":null}`, map[string]string{"If-Match": `"` + rev + `"`}, 200, &rows)
	if strings.Contains(cleared.Body.String(), `"bduss":true`) || !strings.Contains(cleared.Body.String(), `"access_token":true`) {
		t.Fatal("jar clear did not preserve independent token")
	}
	if _, err = runtime.store.CredentialVersion(context.Background(), id, 1); err == nil {
		t.Fatal("cleared credential reference remained usable")
	}
	quota := p3Wire(t, h, "ACC08", "GET", base+"/quota", "", nil, 200, &rows)
	if dataMap(t, quota)["total_bytes"] != "9007199254741999" {
		t.Fatal("quota precision")
	}
	caps := p3Wire(t, h, "ACC09", "GET", base+"/capabilities", "", nil, 200, &rows)
	var capabilities struct{ Data []map[string]any }
	_ = json.Unmarshal(caps.Body.Bytes(), &capabilities)
	for _, c := range capabilities.Data {
		if c["id"] == "FIL01" && (c["enabled"] != false || c["availability"] != "disabled") {
			t.Fatal("token-only account advertised cookie capability")
		}
	}
	p3Wire(t, h, "FIL01", "GET", base+"/files", "", nil, 422, &rows)
	if err = runtime.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	replay := p3Wire(t, reopened.Handler, "ACC04", "PATCH", base, `{"label":"changed"}`, h1, 200, nil)
	if replay.Header().Get("ETag") != `"2"` {
		t.Fatal("replay lost original version")
	}
	p3Wire(t, reopened.Handler, "ACC05", "DELETE", base, "", map[string]string{"Idempotency-Key": "delete"}, 204, &rows)
	p3Wire(t, reopened.Handler, "ACC05", "DELETE", base, "", map[string]string{"Idempotency-Key": "delete"}, 204, nil)
	p3Wire(t, reopened.Handler, "ACC03", "GET", base, "", nil, 404, &rows)
	if file := os.Getenv("BAIDUPCS_P3_ACCOUNT_RESPONSES"); file != "" {
		b, _ := json.MarshalIndent(rows, "", "  ")
		if err = os.WriteFile(file, b, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
func TestP3FileReadingSnapshotsScansJobsAndArtifacts(t *testing.T) {
	runtime, _, u := p3Runtime(t)
	h := runtime.Handler
	rows := []p3Response{}
	id := p3Account(t, h, "ACCOUNT_A", nil)
	base := "/api/v1/accounts/" + id
	for _, count := range []int{1001, 2000, 2001} {
		u.mu.Lock()
		u.count = count
		u.mu.Unlock()
		total := 0
		for page := 1; ; page++ {
			r := p3Wire(t, h, "FIL01", "GET", fmt.Sprintf("%s/files?path=/data&page=%d&page_size=1000", base, page), "", nil, 200, &rows)
			entries := dataMap(t, r)["entries"].([]any)
			total += len(entries)
			if len(entries) < 1000 {
				break
			}
		}
		if total != count {
			t.Fatalf("pagination returned %d of %d", total, count)
		}
	}
	projection := p3Wire(t, h, "FIL01", "GET", base+"/files?path=/data&page_size=1&fields=fs_id", "", nil, 200, &rows)
	first := dataMap(t, projection)["entries"].([]any)[0].(map[string]any)
	if first["fs_id"] != "9007199254740993" || first["app_id"] != nil || len(first) != 6 {
		t.Fatal("projection or fs_id precision", first)
	}
	p3Wire(t, h, "FIL01", "GET", base+"/files?path=/empty", "", nil, 200, &rows)
	p3Wire(t, h, "FIL01", "GET", base+"/files?path=relative", "", nil, 400, &rows)
	p3Wire(t, h, "FIL01", "GET", base+"/files?page=0", "", nil, 400, &rows)
	p3Wire(t, h, "FIL01", "GET", base+"/files?path=/data&path=/bad", "", nil, 400, &rows)
	p3Wire(t, h, "FIL02", "GET", base+"/files/metadata?path=file&base_path=/data", "", nil, 200, &rows)
	p3Wire(t, h, "FIL03", "POST", base+"/files/metadata", `{"paths":["/data/file0000","/missing"]}`, nil, 200, &rows)
	search := p3Wire(t, h, "FIL04", "GET", base+"/files/search?path=/data&keyword=file&page_size=2", "", nil, 200, &rows)
	var searchBody struct {
		Meta struct {
			Pagination struct {
				Next string `json:"next_cursor"`
			}
		}
	}
	_ = json.Unmarshal(search.Body.Bytes(), &searchBody)
	u.mu.Lock()
	searches := u.searches
	u.mu.Unlock()
	p3Wire(t, h, "FIL04", "GET", base+"/files/search?path=/data&keyword=file&page_size=2&cursor="+url.QueryEscape(searchBody.Meta.Pagination.Next), "", nil, 200, &rows)
	u.mu.Lock()
	if u.searches != searches {
		t.Error("snapshot fetched upstream twice")
	}
	u.mu.Unlock()
	p3Wire(t, h, "FIL04", "GET", base+"/files/search?path=/data&keyword=changed&page_size=2&cursor="+url.QueryEscape(searchBody.Meta.Pagination.Next), "", nil, 409, &rows)
	other := p3Account(t, h, "ACCOUNT_B", nil)
	p3Wire(t, h, "FIL04", "GET", "/api/v1/accounts/"+other+"/files/search?path=/data&keyword=file&page_size=2&cursor="+url.QueryEscape(searchBody.Meta.Pagination.Next), "", nil, 409, &rows)
	p3Wire(t, h, "FIL04", "GET", base+"/files/search?path=/data&keyword=huge", "", nil, 413, &rows)
	u.mu.Lock()
	u.count = 5
	u.mu.Unlock()
	p3Wire(t, h, "FIL06", "POST", base+"/path-matches", `{"patterns":["/data/file000?","/data/[bad"]}`, nil, 200, &rows)
	p3Wire(t, h, "LOC01", "GET", "/api/v1/local/roots", "", nil, 200, &rows)
	created := p3Wire(t, h, "FIL05", "POST", base+"/file-scans", `{"paths":["/data","/missing"],"options":{"mode":"tree","depth":0,"include_ids":false}}`, map[string]string{"Idempotency-Key": "scan-once"}, 202, &rows)
	job := dataMap(t, created)["job_id"].(string)
	jobURL := "/api/v1/jobs/" + job
	p3Wait(t, h, job, "partially_succeeded")
	p3Wire(t, h, "JOB01", "GET", "/api/v1/jobs?kind=file_scan&account_id="+id, "", nil, 200, &rows)
	p3Wire(t, h, "JOB02", "GET", jobURL, "", nil, 200, &rows)
	items := p3Wire(t, h, "JOB03", "GET", jobURL+"/items?page_size=2", "", nil, 200, &rows)
	if !strings.Contains(items.Body.String(), "next_cursor") {
		t.Fatal("item pagination missing")
	}
	result := p3Wire(t, h, "JOB04", "GET", jobURL+"/result", "", nil, 200, &rows)
	details := dataMap(t, result)["result"].(map[string]any)
	if details["complete"] != false {
		t.Fatal("partial scan claimed complete")
	}
	artifactID := details["artifact_ids"].([]any)[0].(string)
	artifactURL := "/api/v1/artifacts/" + artifactID
	p3Wire(t, h, "JOB11", "GET", jobURL+"/artifacts", "", nil, 200, &rows)
	p3Wire(t, h, "ART01", "GET", artifactURL, "", nil, 200, &rows)
	content := p3Wire(t, h, "ART02", "GET", artifactURL+"/content", "", nil, 200, &rows)
	if strings.Contains(content.Body.String(), "fs_id") {
		t.Fatal("include_ids=false ignored")
	}
	ranged := p3Wire(t, h, "ART02", "GET", artifactURL+"/content", "", map[string]string{"Range": "bytes=0-9"}, 206, &rows)
	if ranged.Body.String() != content.Body.String()[:10] {
		t.Fatal("range content")
	}
	p3Wire(t, h, "ART03", "HEAD", artifactURL+"/content", "", map[string]string{"Range": "bytes=-10"}, 206, &rows)
	p3Wire(t, h, "ART02", "GET", artifactURL+"/content", "", map[string]string{"Range": "bytes=0-9", "If-Range": `"stale"`}, 200, &rows)
	p3Wire(t, h, "ART02", "GET", artifactURL+"/content", "", map[string]string{"Range": "bytes=999999-"}, 416, &rows)
	p3Wire(t, h, "ART03", "HEAD", artifactURL+"/content", "", map[string]string{"Range": "bytes=999999-"}, 416, &rows)
	p3Wire(t, h, "ART02", "GET", artifactURL+"/content", "", map[string]string{"Range": "bytes=0-1,3-4"}, 400, &rows)
	p3Wire(t, h, "ART02", "GET", artifactURL+"/content", "", map[string]string{"Authorization": "Bearer " + p3OtherToken}, 403, &rows)
	events := p3Wire(t, h, "JOB05", "GET", jobURL+"/events", "", nil, 200, &rows)
	if !strings.Contains(events.Body.String(), "event: job.snapshot") {
		t.Fatal("missing initial snapshot")
	}
	replay := p3Wire(t, h, "JOB05", "GET", jobURL+"/events", "", map[string]string{"Last-Event-ID": "evt_" + job + "_0"}, 200, &rows)
	if !strings.Contains(replay.Body.String(), "event: job.finished") || !strings.Contains(replay.Body.String(), "event: item.completed") {
		t.Fatal("durable events missing")
	}
	p3Wire(t, h, "JOB05", "GET", jobURL+"/events", "", map[string]string{"Last-Event-ID": "evt_other_1"}, 400, &rows)
	p3Wire(t, h, "JOB05", "GET", jobURL+"/events", "", map[string]string{"Last-Event-ID": "evt_" + job + "_999999"}, 409, &rows)
	retry := p3Wire(t, h, "JOB09", "POST", jobURL+"/retry", `{}`, map[string]string{"Idempotency-Key": "retry"}, 202, &rows)
	retryJob := dataMap(t, retry)["job_id"].(string)
	if retryJob == job || dataMap(t, retry)["retry_of_job_id"] != job {
		t.Fatal("retry reused original task")
	}
	p3Wait(t, h, retryJob, "failed")
	p3Wire(t, h, "JOB09", "POST", jobURL+"/retry", `{}`, map[string]string{"Idempotency-Key": "retry"}, 202, nil)
	p3Wire(t, h, "JOB06", "POST", jobURL+"/cancel", `{}`, nil, 202, &rows)
	p3Wire(t, h, "ART04", "DELETE", artifactURL, "", map[string]string{"Idempotency-Key": "delete-result"}, 204, &rows)
	p3Wire(t, h, "ART04", "DELETE", artifactURL, "", map[string]string{"Idempotency-Key": "delete-result"}, 204, nil)
	p3Wire(t, h, "ART01", "GET", artifactURL, "", nil, 410, &rows)
	expired := p3Account(t, h, "EXPIRED", nil)
	p3Wire(t, h, "FIL01", "GET", "/api/v1/accounts/"+expired+"/files", "", nil, 422, &rows)
	for _, r := range rows {
		b, _ := json.Marshal(r.Body)
		for _, secret := range []string{"ACCOUNT_A", "ACCOUNT_B", runtime.DataDir} {
			if strings.Contains(string(b), secret) {
				t.Fatal("public data leaked secret")
			}
		}
	}
	if file := os.Getenv("BAIDUPCS_P3_CONTRACT_RESPONSES"); file != "" {
		b, _ := json.MarshalIndent(rows, "", "  ")
		if err := os.WriteFile(file, b, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestP3ScanLimitsCancellationRevocationAndRestart(t *testing.T) {
	runtime, options, u := p3Runtime(t)
	id := p3Account(t, runtime.Handler, "ACCOUNT_A", nil)
	base := "/api/v1/accounts/" + id
	limited := p3Wire(t, runtime.Handler, "FIL05", "POST", base+"/file-scans", `{"paths":["/data"],"options":{"mode":"statistics","max_nodes":"3"}}`, nil, 202, nil)
	job := dataMap(t, limited)["job_id"].(string)
	p3Wait(t, runtime.Handler, job, "succeeded")
	result := p3Wire(t, runtime.Handler, "JOB04", "GET", "/api/v1/jobs/"+job+"/result", "", nil, 200, nil)
	if v := dataMap(t, result)["result"].(map[string]any); v["complete"] != false || v["truncation_reason"] != "max_nodes" {
		t.Fatal("node limit silently claimed a complete scan")
	}
	u.mu.Lock()
	u.delay = true
	u.mu.Unlock()
	started := p3Wire(t, runtime.Handler, "FIL05", "POST", base+"/file-scans", `{"paths":["/slow"],"options":{"depth":0}}`, nil, 202, nil)
	job = dataMap(t, started)["job_id"].(string)
	select {
	case <-u.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("slow scan not started")
	}
	blocked := p3Wire(t, runtime.Handler, "ACC05", "DELETE", base, "", nil, 409, nil)
	if !strings.Contains(blocked.Body.String(), "ACCOUNT_IN_USE") {
		t.Fatal("active account deletion bypassed task protection")
	}
	before, seq, err := runtime.store.EventSnapshot(context.Background(), job)
	if err != nil || before.State != "running" {
		t.Fatal(err)
	}
	// A normal update keeps this submitted scan on its original revision.
	p3Wire(t, runtime.Handler, "ACC06", "PATCH", base+"/credentials", `{"ptoken":"NEW_TOKEN"}`, map[string]string{"If-Match": `"1"`}, 200, nil)
	if _, err = runtime.store.CredentialVersion(context.Background(), id, 1); err != nil {
		t.Fatal("normal update broke running job")
	}
	if err = runtime.Close(); err != nil {
		t.Fatal(err)
	}
	u.mu.Lock()
	u.delay = false
	u.mu.Unlock()
	reopened, err := New(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	restored, after, err := reopened.store.EventSnapshot(context.Background(), job)
	if err != nil || restored.State != "interrupted" || after <= seq || restored.Version <= before.Version {
		t.Fatal("scan recovery lost durable lifecycle", err)
	}
	p3Wire(t, reopened.Handler, "JOB08", "POST", "/api/v1/jobs/"+job+"/resume", `{}`, nil, 422, nil)
	p3Wire(t, reopened.Handler, "JOB06", "POST", "/api/v1/jobs/"+job+"/cancel", `{}`, nil, 202, nil)
	retry := p3Wire(t, reopened.Handler, "JOB09", "POST", "/api/v1/jobs/"+job+"/retry", `{"scope":"failed_and_cancelled"}`, nil, 202, nil)
	p3Wait(t, reopened.Handler, dataMap(t, retry)["job_id"].(string), "succeeded")
	// Clearing a session must abort an already open upstream request as well.
	u.mu.Lock()
	u.delay = true
	u.mu.Unlock()
	revokeScan := p3Wire(t, reopened.Handler, "FIL05", "POST", base+"/file-scans", `{"paths":["/slow"]}`, nil, 202, nil)
	revokedJob := dataMap(t, revokeScan)["job_id"].(string)
	select {
	case <-u.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("revocation scan not started")
	}
	current := p3Wire(t, reopened.Handler, "ACC03", "GET", base, "", nil, 200, nil)
	revision := dataMap(t, current)["revision"].(string)
	p3Wire(t, reopened.Handler, "ACC06", "PATCH", base+"/credentials", `{"cookies":null}`, map[string]string{"If-Match": `"` + revision + `"`}, 200, nil)
	revoked := p3Wait(t, reopened.Handler, revokedJob, "failed")
	if !strings.Contains(revoked.Body.String(), "CREDENTIAL_REVOKED") {
		t.Fatal("revoked job did not report revoked credential")
	}
}

type slowEventWriter struct {
	*httptest.ResponseRecorder
	entered, release chan struct{}
	once             sync.Once
}

func (w *slowEventWriter) Write(b []byte) (int, error) {
	w.once.Do(func() { close(w.entered); <-w.release })
	return w.ResponseRecorder.Write(b)
}
func TestP3SSESlowConsumerDisconnectsWithoutBlockingJob(t *testing.T) {
	runtime, _, u := p3Runtime(t)
	id := p3Account(t, runtime.Handler, "ACCOUNT_A", nil)
	u.mu.Lock()
	u.count = 100
	u.delay = true
	u.mu.Unlock()
	started := p3Wire(t, runtime.Handler, "FIL05", "POST", "/api/v1/accounts/"+id+"/file-scans", `{"paths":["/slow"],"options":{"depth":0}}`, nil, 202, nil)
	job := dataMap(t, started)["job_id"].(string)
	select {
	case <-u.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("scan not blocked")
	}
	req := httptest.NewRequest("GET", "/api/v1/jobs/"+job+"/events", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	writer := &slowEventWriter{ResponseRecorder: httptest.NewRecorder(), entered: make(chan struct{}), release: make(chan struct{})}
	done := make(chan struct{})
	go func() { runtime.Handler.ServeHTTP(writer, req); close(done) }()
	select {
	case <-writer.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("SSE did not send initial snapshot")
	}
	close(u.release)
	p3Wait(t, runtime.Handler, job, "succeeded")
	close(writer.release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("slow SSE retained unbounded backlog")
	}
	if strings.Count(writer.Body.String(), "data:") != 1 || !strings.Contains(writer.Body.String(), "job.snapshot") {
		t.Fatal("slow subscriber was not disconnected at its queue bound")
	}
	// Durable replay remains available after disconnection, with no job failure.
	replay := p3Wire(t, runtime.Handler, "JOB05", "GET", "/api/v1/jobs/"+job+"/events", "", map[string]string{"Last-Event-ID": "evt_" + job + "_0"}, 200, nil)
	if !strings.Contains(replay.Body.String(), "job.finished") {
		t.Fatal("slow-consumer disconnect lost durable events")
	}
}
func TestP3ExplicitLegacyAccountsImportIsStableAndReadOnly(t *testing.T) {
	runtime, options, _ := p3Runtime(t)
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(t.TempDir(), "pcs_config.json")
	source := []byte(`{"baidu_active_uid":24,"baidu_user_list":[{"uid":24,"name":"legacy-a","bduss":"LEGACY_A","ptoken":"LEGACY_PT"},{"uid":24,"name":"legacy-b","cookies":"BDUSS=LEGACY_B;STOKEN=LEGACY_ST","accesstoken":"LEGACY_TOKEN"}],"max_parallel":99}`)
	if err := os.WriteFile(legacy, source, 0600); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(options.ConfigFile)
	var config startup
	_ = json.Unmarshal(data, &config)
	config.LegacyAccountsFile = legacy
	data, _ = json.Marshal(config)
	if err := os.WriteFile(options.ConfigFile, data, 0600); err != nil {
		t.Fatal(err)
	}
	service, err := New(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	list := p3Wire(t, service.Handler, "ACC02", "GET", "/api/v1/accounts", "", nil, 200, nil)
	var result struct{ Data []map[string]any }
	_ = json.Unmarshal(list.Body.Bytes(), &result)
	if len(result.Data) != 2 || result.Data[0]["account_id"] == result.Data[1]["account_id"] {
		t.Fatal("legacy duplicate UID merged accounts")
	}
	for _, a := range result.Data {
		if a["baidu_uid"] != nil || a["identity_verified"] != false {
			t.Fatal("legacy fallback UID claimed verified identity")
		}
	}
	snapshot, err := service.store.Read(context.Background())
	if err != nil || snapshot.Client().MaxParallel == 99 {
		t.Fatal("account import changed service client configuration")
	}
	if err = service.Close(); err != nil {
		t.Fatal(err)
	}
	service, err = New(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	list = p3Wire(t, service.Handler, "ACC02", "GET", "/api/v1/accounts", "", nil, 200, nil)
	_ = json.Unmarshal(list.Body.Bytes(), &result)
	if len(result.Data) != 2 {
		t.Fatal("restart duplicated legacy import")
	}
	after, err := os.ReadFile(legacy)
	if err != nil || !bytes.Equal(source, after) {
		t.Fatal("legacy source was rewritten")
	}
}

func TestP3SSESnapshotFollowsThroughTerminalCommit(t *testing.T) {
	runtime, _, u := p3Runtime(t)
	id := p3Account(t, runtime.Handler, "ACCOUNT_A", nil)
	u.mu.Lock()
	u.delay = true
	u.mu.Unlock()
	created := p3Wire(t, runtime.Handler, "FIL05", "POST", "/api/v1/accounts/"+id+"/file-scans", `{"paths":["/slow"],"options":{"depth":0}}`, nil, 202, nil)
	job := dataMap(t, created)["job_id"].(string)
	select {
	case <-u.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("scan did not start")
	}
	server := httptest.NewServer(runtime.Handler)
	defer server.Close()
	req, _ := http.NewRequest("GET", server.URL+"/api/v1/jobs/"+job+"/events", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	var initial strings.Builder
	for {
		line, e := reader.ReadString('\n')
		if e != nil {
			t.Fatal(e)
		}
		initial.WriteString(line)
		if line == "\n" {
			break
		}
	}
	if !strings.Contains(initial.String(), "job.snapshot") {
		t.Fatal("missing initial snapshot")
	}
	close(u.release)
	done := make(chan string, 1)
	go func() { body, _ := io.ReadAll(reader); done <- string(body) }()
	select {
	case body := <-done:
		if !strings.Contains(body, "event: job.finished") {
			t.Fatal("terminal state became visible without its SSE event", body)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("SSE did not finish")
	}
}

func TestP3SSEHeartbeatIsCommentWithoutID(t *testing.T) {
	runtime, _, u := p3Runtime(t)
	id := p3Account(t, runtime.Handler, "ACCOUNT_A", nil)
	u.mu.Lock()
	u.delay = true
	u.mu.Unlock()
	created := p3Wire(t, runtime.Handler, "FIL05", "POST", "/api/v1/accounts/"+id+"/file-scans", `{"paths":["/slow"]}`, nil, 202, nil)
	job := dataMap(t, created)["job_id"].(string)
	select {
	case <-u.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("scan not blocked")
	}
	server := httptest.NewServer(runtime.Handler)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 22*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/v1/jobs/"+job+"/events", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	frame := func() string {
		t.Helper()
		var out strings.Builder
		for {
			line, e := reader.ReadString('\n')
			if e != nil {
				t.Fatal(e)
			}
			out.WriteString(line)
			if line == "\n" {
				return out.String()
			}
		}
	}
	if initial := frame(); !strings.Contains(initial, "job.snapshot") {
		t.Fatal("missing snapshot")
	}
	if heartbeat := frame(); heartbeat != ": heartbeat\n\n" || strings.Contains(heartbeat, "id:") {
		t.Fatal("heartbeat acquired an event ID")
	}
	_ = response.Body.Close()
	cancel()
	close(u.release)
	p3Wait(t, runtime.Handler, job, "succeeded")
}

func TestP3RetryScanPreparationFailureCreatesFreshJob(t *testing.T) {
	runtime, _, _ := p3Runtime(t)
	id := p3Account(t, runtime.Handler, "ACCOUNT_A", nil)
	ctx := context.Background()
	cfg, err := runtime.store.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	value := cfg.Client()
	value.EnableHTTPS = false
	changed, err := runtime.store.Commit(ctx, cfg.Revision(), value, configuration.Mutation{})
	if err != nil {
		t.Fatal(err)
	}
	created := p3Wire(t, runtime.Handler, "FIL05", "POST", "/api/v1/accounts/"+id+"/file-scans", `{"paths":["/data"]}`, nil, 202, nil)
	job := dataMap(t, created)["job_id"].(string)
	failed := p3Wait(t, runtime.Handler, job, "failed")
	if !strings.Contains(failed.Body.String(), "HTTPS_REQUIRED") || !strings.Contains(failed.Body.String(), `"available_actions":["retry"]`) {
		t.Fatal("preparation failure has no retry action")
	}
	value.EnableHTTPS = true
	if _, err = runtime.store.Commit(ctx, changed.Snapshot.Revision(), value, configuration.Mutation{}); err != nil {
		t.Fatal(err)
	}
	retried := p3Wire(t, runtime.Handler, "JOB09", "POST", "/api/v1/jobs/"+job+"/retry", `{}`, map[string]string{"Idempotency-Key": "preparation-retry"}, 202, nil)
	fresh := dataMap(t, retried)["job_id"].(string)
	if fresh == job || dataMap(t, retried)["retry_of_job_id"] != job {
		t.Fatal("retry reused failed job")
	}
	p3Wait(t, runtime.Handler, fresh, "succeeded")
	p3Wire(t, runtime.Handler, "JOB09", "POST", "/api/v1/jobs/"+job+"/retry", `{"scope":"failed_and_cancelled"}`, map[string]string{"Idempotency-Key": "preparation-retry"}, 409, nil)
}

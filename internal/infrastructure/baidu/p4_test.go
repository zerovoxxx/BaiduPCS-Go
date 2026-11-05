package baidu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	apptransfers "github.com/qjfoidnh/BaiduPCS-Go/internal/application/transfers"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/account"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/transfer"
)

func batchRequest(kind, id string, b transfer.Batch) transfer.Request {
	raw, _ := json.Marshal(b)
	options := strings.TrimSuffix(strings.TrimPrefix(uploadOptions, `"options":`), "")
	if kind == "download" {
		options = strings.TrimPrefix(downloadOptions, `"options":`)
	}
	return transfer.Request{Kind: kind, AccountID: id, RootID: "test", RelativePath: "out", RemotePath: "/round", Batch: string(raw), Options: options, Public: true}
}
func TestP4DirectoryHierarchyEmptyFilesAndOptions(t *testing.T) {
	f := newTransferFixture(t)
	a := f.account(t, "P4_DIRECTORY")
	for _, dir := range []string{"a/empty", "b", "out"} {
		if err := os.MkdirAll(filepath.Join(f.root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for p, body := range map[string][]byte{"a/x": bytes.Repeat([]byte("a"), (5<<20)+17), "a/zero": {}, "b/x": []byte("second")} {
		if err := os.WriteFile(filepath.Join(f.root, p), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	req := batchRequest("upload", a.ID, transfer.Batch{Sources: []transfer.Source{{Type: "local", RootID: "test", RelativePath: "a"}, {Type: "local", RootID: "test", RelativePath: "b"}}})
	req.Options = strings.Replace(req.Options, `"file_concurrency":1`, `"file_concurrency":2`, 1)
	req.Options = strings.Replace(req.Options, `"parallel_per_file":1`, `"parallel_per_file":2`, 1)
	job, err := f.service.Submit(context.Background(), transferAdmin, req, "")
	if err != nil {
		t.Fatal(err)
	}
	f.wait(t, job.ID, "succeeded")
	items, err := f.service.Items(context.Background(), transferAdmin, job.ID)
	if err != nil || len(items) != 6 {
		t.Fatalf("directory units %d: %v", len(items), err)
	}
	req = batchRequest("download", a.ID, transfer.Batch{Paths: []string{"/round/a", "/round/b", "/round/a/x"}, Directory: "out"})
	req.RemotePath = "/round/a"
	req.Options = strings.Replace(req.Options, `"parallel":1`, `"parallel":2`, 1)
	req.Options = strings.Replace(req.Options, `"preserve_mtime":false`, `"preserve_mtime":true`, 1)
	req.Options = strings.Replace(req.Options, `"executable":false`, `"executable":true`, 1)
	req.Options = strings.Replace(req.Options, `"report_workers":false`, `"report_workers":true`, 1)
	job, err = f.service.Submit(context.Background(), transferAdmin, req, "")
	if err != nil {
		t.Fatal(err)
	}
	f.wait(t, job.ID, "succeeded")
	for _, p := range []string{"a/x", "a/zero", "b/x"} {
		want, _ := os.ReadFile(filepath.Join(f.root, p))
		got, err := os.ReadFile(filepath.Join(f.root, "out", p))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("hierarchy/bytes %s: %v", p, err)
		}
	}
	if info, err := os.Stat(filepath.Join(f.root, "out/a/empty")); err != nil || !info.IsDir() {
		t.Fatal("empty directory lost")
	}
	info, _ := os.Stat(filepath.Join(f.root, "out/a/x"))
	if info.ModTime().Unix() != 1700000000 || info.Mode().Perm() != 0700 {
		t.Fatal("mtime/mode ignored")
	}
	items, err = f.service.Items(context.Background(), transferAdmin, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	workers := 0
	for _, item := range items {
		workers += len(item.Workers)
	}
	if workers == 0 {
		t.Fatal("requested worker reporting was ignored")
	}
	// Existing destinations are explicit per-item conflicts; healthy units survive.
	retryReq := req
	retryReq.Options = strings.Replace(req.Options, `"overwrite":false`, `"overwrite":true`, 1)
	j, err := f.service.Submit(context.Background(), transferAdmin, retryReq, "")
	if err != nil {
		t.Fatal(err)
	}
	f.wait(t, j.ID, "succeeded")
	// test performs transfer/checksum and leaves no final file.
	testReq := batchRequest("download", a.ID, transfer.Batch{Paths: []string{"/round/b/x"}, Directory: "test-output"})
	testReq.RemotePath = "/round/b/x"
	testReq.Options = strings.Replace(testReq.Options, `"test":false`, `"test":true`, 1)
	j, err = f.service.Submit(context.Background(), transferAdmin, testReq, "")
	if err != nil {
		t.Fatal(err)
	}
	f.wait(t, j.ID, "succeeded")
	if _, err = os.Stat(filepath.Join(f.root, "test-output/x")); !os.IsNotExist(err) {
		t.Fatal("test wrote final file")
	}
}
func TestP4PauseResumeIsolationAndChangedSource(t *testing.T) {
	f := newTransferFixture(t)
	a := f.account(t, "P4_CONTROL")
	os.WriteFile(filepath.Join(f.root, "input"), bytes.Repeat([]byte("p"), 1<<20), 0600)
	first := f.submit(t, a, "upload", "input", "/pause-A", "")
	select {
	case <-f.upstream.blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("A not executing")
	}
	if _, err := f.service.Pause(context.Background(), transferAdmin, first.ID, structControl()); err != nil {
		t.Fatal(err)
	}
	f.wait(t, first.ID, "paused")
	b := f.submit(t, a, "upload", "input", "/B", "")
	f.wait(t, b.ID, "succeeded")
	c := f.submit(t, a, "upload", "input", "/C", "")
	f.wait(t, c.ID, "succeeded")
	if _, err := f.service.Resume(context.Background(), transferAdmin, first.ID); err != nil {
		t.Fatal(err)
	}
	f.wait(t, first.ID, "succeeded")
	changed := f.submit(t, a, "upload", "input", "/pause-source", "")
	select {
	case <-f.upstream.blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("source not executing")
	}
	if _, err := f.service.Pause(context.Background(), transferAdmin, changed.ID, structControl()); err != nil {
		t.Fatal(err)
	}
	f.wait(t, changed.ID, "paused")
	os.WriteFile(filepath.Join(f.root, "input"), []byte("changed"), 0600)
	if _, err := f.service.Resume(context.Background(), transferAdmin, changed.ID); err != nil {
		t.Fatal(err)
	}
	r := f.wait(t, changed.ID, "failed")
	if r.FailureCode != "CHECKPOINT_SOURCE_CHANGED" {
		t.Fatal("changed source resumed")
	}
}
func TestP4HandshakesResumeExpiryAndChecksums(t *testing.T) {
	for _, rapid := range []bool{false, true} {
		t.Run(fmt.Sprint("rapid-", rapid), func(t *testing.T) {
			f := newTransferFixture(t)
			a := f.account(t, "P4_HANDSHAKE")
			f.upstream.rapidNine = rapid
			os.WriteFile(filepath.Join(f.root, "input"), bytes.Repeat([]byte("r"), 9<<20), 0600)
			req := transfer.Request{Kind: "upload", AccountID: a.ID, RootID: "test", RelativePath: "input", RemotePath: "/restart-handshake", Public: true, Options: strings.TrimPrefix(uploadOptions, `"options":`)}
			if rapid {
				req.Options = strings.Replace(req.Options, `"skip_rapid_check":true`, `"skip_rapid_check":false`, 1)
			}
			job, err := f.service.Submit(context.Background(), transferAdmin, req, "")
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-f.upstream.blocked:
			case <-time.After(5 * time.Second):
				t.Fatal("restart point not reached")
			}
			f.close(t)
			f.open(t)
			if _, err = f.service.Resume(context.Background(), transferAdmin, job.ID); err != nil {
				t.Fatal(err)
			}
			f.wait(t, job.ID, "succeeded")
			f.upstream.mu.Lock()
			parts := f.upstream.parts["P4_HANDSHAKE\x00/restart-handshake"]
			first := parts[0]
			f.upstream.mu.Unlock()
			if first != 1 {
				t.Fatal("confirmed part repeated")
			}
		})
	}
	f := newTransferFixture(t)
	a := f.account(t, "P4_CHECK")
	os.WriteFile(filepath.Join(f.root, "input"), []byte("checksum"), 0600)
	f.upstream.badChecksum = true
	j := f.submit(t, a, "upload", "input", "/bad", "")
	r := f.wait(t, j.ID, "failed")
	if r.FailureCode != "CHECKSUM_MISMATCH" {
		t.Fatal("wrong block accepted")
	}
	f.upstream.badChecksum = false
	f.upstream.expireUpload = true
	req := transfer.Request{Kind: "upload", AccountID: a.ID, RootID: "test", RelativePath: "input", RemotePath: "/expired", Public: true, Options: strings.Replace(strings.TrimPrefix(uploadOptions, `"options":`), `"max_retries":0`, `"max_retries":1`, 1)}
	j, err := f.service.Submit(context.Background(), transferAdmin, req, "")
	if err != nil {
		t.Fatal(err)
	}
	f.wait(t, j.ID, "succeeded")
}
func TestP4MultipartReferencesAndContentHTTP(t *testing.T) {
	f := newTransferFixture(t)
	h := testHTTPAPI(t, f)
	a := f.account(t, "P4_STAGE")
	var rows []liveResponse
	var idsMu sync.Mutex
	ids := []string{}
	var wg sync.WaitGroup
	for n := 0; n < 3; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var body bytes.Buffer
			mw := multipart.NewWriter(&body)
			part, _ := mw.CreateFormFile("file", "same.txt")
			part.Write([]byte("payload"))
			mw.WriteField("filename", "display.txt")
			mw.Close()
			req := httptest.NewRequest("POST", "/api/v1/staging-files", &body)
			req.Header.Set("Authorization", "Bearer "+httpTestToken)
			req.Header.Set("Content-Type", mw.FormDataContentType())
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != 201 {
				t.Error(rec.Code, rec.Body.String())
				return
			}
			id := resourceID(t, rec, "staged_file_id")
			idsMu.Lock()
			ids = append(ids, id)
			rows = append(rows, liveResponse{"UPL02", "POST", 201, "application/json", rec.Header().Clone(), json.RawMessage(rec.Body.Bytes())})
			idsMu.Unlock()
		}()
	}
	wg.Wait()
	if len(ids) != 3 || ids[0] == ids[1] || ids[1] == ids[2] {
		t.Fatal("concurrent multipart overwrote allocation")
	}
	wire(t, h, "UPL03", "GET", "/api/v1/staging-files/"+ids[0], "", "", 200, &rows)
	body := fmt.Sprintf(`{"sources":[{"type":"staged","staged_file_id":%q}],"target_dir":"/pause-stage",%s}`, ids[0], uploadOptions)
	job := wire(t, h, "UPL01", "POST", "/api/v1/accounts/"+a.ID+"/uploads", "stage", body, 202, nil)
	id := resourceID(t, job, "job_id")
	select {
	case <-f.upstream.blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("staged upload not entered")
	}
	wire(t, h, "UPL04", "DELETE", "/api/v1/staging-files/"+ids[0], "", "", 409, &rows)
	wire(t, h, "JOB07", "POST", "/api/v1/jobs/"+id+"/pause", "pause", `{}`, 202, &rows)
	waitHTTP(t, h, id, "paused")
	wire(t, h, "JOB08", "POST", "/api/v1/jobs/"+id+"/resume", "resume", `{}`, 202, &rows)
	waitHTTP(t, h, id, "succeeded")
	// Raw streams, HEAD errors, single-range validation and scope rejection.
	for _, tc := range []struct {
		method, rangeHeader string
		status              int
		want                string
	}{{"GET", "bytes=1-3", 206, "ayl"}, {"GET", "bytes=100-", 416, ""}, {"GET", "bytes=0-1,3-4", 400, ""}, {"HEAD", "bytes=1-3", 206, ""}, {"HEAD", "bytes=100-", 416, ""}} {
		req := httptest.NewRequest(tc.method, "/api/v1/accounts/"+a.ID+"/content?path=/pause-stage/display.txt", nil)
		req.Header.Set("Authorization", "Bearer "+httpTestToken)
		req.Header.Set("Range", tc.rangeHeader)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tc.status {
			t.Fatalf("range status %d: %s", rec.Code, rec.Body.String())
		}
		if tc.want != "" && rec.Body.String() != tc.want {
			t.Fatal("range bytes")
		}
		if tc.method == "HEAD" && rec.Body.Len() != 0 {
			t.Fatal("HEAD emitted body")
		}
		operation := "DWN03"
		if tc.method == "HEAD" {
			operation = "DWN04"
		}
		var response any
		if tc.method != "HEAD" {
			if strings.HasPrefix(rec.Header().Get("Content-Type"), "application/problem+json") {
				response = json.RawMessage(rec.Body.Bytes())
			} else {
				response = rec.Body.String()
			}
		}
		rows = append(rows, liveResponse{operation, tc.method, rec.Code, strings.Split(rec.Header().Get("Content-Type"), ";")[0], rec.Header().Clone(), response})
	}
	wire(t, h, "DWN02", "POST", "/api/v1/accounts/"+a.ID+"/download-links", "", `{"selector":{"kind":"paths","values":["/pause-stage/display.txt"]},"source":"direct"}`, 200, &rows)
	ticketResponse := wire(t, h, "DWN05", "POST", "/api/v1/accounts/"+a.ID+"/download-tickets", "ticket", `{"path":"/pause-stage/display.txt","expires_in_seconds":60}`, 201, &rows)
	var ticket struct {
		Data struct {
			URL string `json:"url"`
		}
	}
	json.Unmarshal(ticketResponse.Body.Bytes(), &ticket)
	for _, target := range []struct {
		url    string
		status int
	}{{ticket.Data.URL, 200}, {strings.Replace(ticket.Data.URL, "path=%2Fpause-stage%2Fdisplay.txt", "path=%2Fother", 1), 403}} {
		req := httptest.NewRequest("GET", target.url, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != target.status {
			t.Fatal("ticket scope", rec.Code, rec.Body.String())
		}
	}
	parsed, _ := url.Parse(ticket.Data.URL)
	ticketValue := parsed.Query().Get("ticket")
	req := httptest.NewRequest("GET", "/api/v1/jobs/not-found?ticket="+url.QueryEscape(ticketValue), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatal("ticket granted another operation")
	}
	deleted := wire(t, h, "UPL04", "DELETE", "/api/v1/staging-files/"+ids[0], "delete", "", 204, nil)
	rows = append(rows, liveResponse{"UPL04", "DELETE", 204, "", deleted.Header().Clone(), nil})
	if file := os.Getenv("BAIDUPCS_P4_CONTRACT_RESPONSES"); file != "" {
		raw, _ := json.MarshalIndent(rows, "", "  ")
		os.WriteFile(file, raw, 0600)
	}
}
func TestP4NoOverwriteConcurrentCommit(t *testing.T) {
	f := newTransferFixture(t)
	a := f.account(t, "P4_COMMIT")
	f.upstream.files["P4_COMMIT\x00/source"] = []byte("remote")
	os.Mkdir(filepath.Join(f.root, "out"), 0700)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.service.Submit(context.Background(), transferAdmin, transfer.Request{Kind: "download", AccountID: a.ID, RootID: "test", RelativePath: "out/file", RemotePath: "/source"}, "")
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil && fault.As(err).Code != "TARGET_EXISTS" {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(filepath.Join(f.root, "out/file"))
		if err == nil {
			if string(b) != "remote" {
				t.Fatal("bad final")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no committed file")
}

func structControl() apptransfers.Control { return apptransfers.Control{} }

func TestP4ContentSlowStreamDisconnectReleasesUpstream(t *testing.T) {
	f := newTransferFixture(t)
	a := f.account(t, "P4_SLOW")
	f.upstream.files["P4_SLOW\x00/slow-proxy"] = bytes.Repeat([]byte("s"), 1<<20)
	server := httptest.NewServer(testHTTPAPI(t, f))
	defer server.Close()
	req, _ := http.NewRequest("GET", server.URL+"/api/v1/accounts/"+a.ID+"/content?path=/slow-proxy", nil)
	req.Header.Set("Authorization", "Bearer "+httpTestToken)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode)
	}
	one := make([]byte, 1)
	if _, err = io.ReadFull(resp.Body, one); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	for {
		select {
		case state := <-f.upstream.blocked:
			if state == "slow-proxy-closed" {
				return
			}
		case <-time.After(5 * time.Second):
			t.Fatal("disconnected proxy retained upstream")
		}
	}
}
func TestP4PartialRetryRetainsNestedTargetsAndFreshCheckpoints(t *testing.T) {
	f := newTransferFixture(t)
	a := f.account(t, "P4_RETRY")
	os.MkdirAll(filepath.Join(f.root, "tree/a"), 0700)
	os.WriteFile(filepath.Join(f.root, "tree/a/x"), []byte("original"), 0600)
	os.WriteFile(filepath.Join(f.root, "tree/a/y"), []byte("healthy"), 0600)
	f.upstream.directories["P4_RETRY\x00/round/tree/a/x"] = true
	request := batchRequest("upload", a.ID, transfer.Batch{Sources: []transfer.Source{{Type: "local", RootID: "test", RelativePath: "tree"}}})
	job, err := f.service.Submit(context.Background(), transferAdmin, request, "")
	if err != nil {
		t.Fatal(err)
	}
	f.wait(t, job.ID, "partially_succeeded")
	f.upstream.mu.Lock()
	delete(f.upstream.directories, "P4_RETRY\x00/round/tree/a/x")
	f.upstream.mu.Unlock()
	os.WriteFile(filepath.Join(f.root, "tree/a/x"), []byte("fresh"), 0600)
	retry, err := f.service.Retry(context.Background(), transferAdmin, job.ID, "failed", "")
	if err != nil {
		t.Fatal(err)
	}
	if retry.ID == job.ID || len(retry.Checkpoint) > 0 {
		t.Fatal("retry reused old task state")
	}
	f.wait(t, retry.ID, "succeeded")
	f.upstream.mu.Lock()
	got := f.upstream.files["P4_RETRY\x00/round/tree/a/x"]
	_, flattened := f.upstream.files["P4_RETRY\x00/round/x"]
	f.upstream.mu.Unlock()
	if string(got) != "fresh" || flattened {
		t.Fatal("retry changed nested target")
	}
}

func TestP4FileIDResolutionPreservesPrecisionAndRequestOrder(t *testing.T) {
	server, pool := trustedServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/filemetas" {
			t.Error("wrong endpoint")
			return
		}
		if r.URL.Query().Get("fsids") != "[9007199254740993,2]" {
			t.Error("ID precision changed")
		}
		io.WriteString(w, `{"errno":0,"info":[{"fs_id":2,"path":"/b/x"},{"fs_id":9007199254740993,"path":"/a/x"}]}`)
	}))
	factory := connectedFactory(t, server, pool)
	defer factory.Close()
	session, err := factory.OpenContent(context.Background(), configuration.NewSnapshot(1, DefaultConfiguration()), account.Credentials{AccountID: "acct_ids", Revision: 1, Cookies: map[string]string{"BDUSS": "ID_ONLY_EXAMPLE"}})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	paths, err := session.ResolveIDs([]int64{9007199254740993, 2})
	if err != nil || len(paths) != 2 || paths[0] != "/a/x" || paths[1] != "/b/x" {
		t.Fatal("ID resolution lost order", err)
	}
}

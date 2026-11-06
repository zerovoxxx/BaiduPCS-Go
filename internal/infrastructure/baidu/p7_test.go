package baidu

import (
	"context"
	"encoding/json"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/transfer"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestP7RsyncComparesSizeAndDryRunHasNoTransfer(t *testing.T) {
	f := newTransferFixture(t)
	a := f.account(t, "P7_POLICY")
	if e := os.WriteFile(filepath.Join(f.root, "source"), []byte("aaaa"), 0600); e != nil {
		t.Fatal(e)
	}
	req := batchRequest("upload", a.ID, transfer.Batch{Sources: []transfer.Source{{Type: "local", RootID: "test", RelativePath: "source"}}})
	j, e := f.service.Submit(context.Background(), transferAdmin, req, "")
	if e != nil {
		t.Fatal(e)
	}
	f.wait(t, j.ID, "succeeded")
	if e = os.WriteFile(filepath.Join(f.root, "source"), []byte("bbbb"), 0600); e != nil {
		t.Fatal(e)
	}
	req.Options = strings.Replace(req.Options, `"policy":"skip"`, `"policy":"rsync"`, 1)
	j, e = f.service.Submit(context.Background(), transferAdmin, req, "")
	if e != nil {
		t.Fatal(e)
	}
	f.wait(t, j.ID, "succeeded")
	items, e := f.service.Items(context.Background(), transferAdmin, j.ID)
	if e != nil || len(items) != 1 || items[0].State != "skipped" {
		t.Fatal("rsync compared more than size", items, e)
	}
	f.upstream.mu.Lock()
	body := string(f.upstream.files["P7_POLICY\x00/round/source"])
	f.upstream.mu.Unlock()
	if body != "aaaa" {
		t.Fatal("rsync changed equal-size target")
	}
	req = batchRequest("download", a.ID, transfer.Batch{Paths: []string{"/round/source"}, Directory: "dry", DryRun: true})
	j, e = f.service.Submit(context.Background(), transferAdmin, req, "")
	if e != nil {
		t.Fatal(e)
	}
	r := f.wait(t, j.ID, "succeeded")
	if r.ScanResult == nil || r.ScanResult.Transferred != 0 || r.ScanResult.Written != 0 {
		t.Fatal("dry-run transferred bytes")
	}
	if _, e = os.Stat(filepath.Join(f.root, "dry/source")); !os.IsNotExist(e) {
		t.Fatal("dry-run wrote file", e)
	}
	var plan transfer.Plan
	if e = json.Unmarshal(r.Plan, &plan); e != nil || !plan.DryRun {
		t.Fatal("dry-run plan lost", e)
	}
	req = batchRequest("download", a.ID, transfer.Batch{Paths: []string{"/round/source"}, Directory: "full"})
	req.Options = strings.Replace(req.Options, `"path_mode":"relative"`, `"path_mode":"full"`, 1)
	j, e = f.service.Submit(context.Background(), transferAdmin, req, "")
	if e != nil {
		t.Fatal(e)
	}
	f.wait(t, j.ID, "succeeded")
	if got, e := os.ReadFile(filepath.Join(f.root, "full/round/source")); e != nil || string(got) != "aaaa" {
		t.Fatal("full path hierarchy lost", e)
	}
	req = batchRequest("download", a.ID, transfer.Batch{Paths: []string{"/round/source"}, Directory: "test"})
	req.Options = strings.Replace(req.Options, `"test":false`, `"test":true`, 1)
	j, e = f.service.Submit(context.Background(), transferAdmin, req, "")
	if e != nil {
		t.Fatal(e)
	}
	r = f.wait(t, j.ID, "succeeded")
	if r.ScanResult == nil || r.ScanResult.Transferred != 4 || r.ScanResult.Written != 0 {
		t.Fatal("test mode byte accounting", r.ScanResult)
	}
	h := testHTTPAPI(t, f)
	bodyJSON := `{"selector":{"kind":"paths","values":["/round/source"]},"destination":{"root_id":"test","directory":"http-test"},` + strings.Replace(downloadOptions, `"test":false`, `"test":true`, 1) + `}`
	created := wire(t, h, "DWN01", "POST", "/api/v1/accounts/"+a.ID+"/downloads", "", bodyJSON, 202, nil)
	jobID := resourceID(t, created, "job_id")
	waitHTTP(t, h, jobID, "succeeded")
	response := wire(t, h, "JOB02", "GET", "/api/v1/jobs/"+jobID, "", "", 200, nil)
	if !strings.Contains(response.Body.String(), `"written_bytes":"0"`) || !strings.Contains(response.Body.String(), `"transferred_bytes":"4"`) {
		t.Fatal("single-file HTTP byte projection", response.Body.String())
	}
}

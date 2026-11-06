package bootstrap

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/account"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/infrastructure/baidu"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/infrastructure/localfs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// This harness reuses the existing authorized cookie. It never opens a login
// session, requests SMS, polls for a human scan, or reads account-wide changes.
func TestP6UpstreamDedicatedAccount(t *testing.T) {
	credentialJSON := os.Getenv("BAIDUPCS_P3_CREDENTIALS_JSON")
	remoteBase := strings.TrimSuffix(os.Getenv("BAIDUPCS_P3_LIVE_REMOTE_DIR"), "/")
	if credentialJSON == "" || remoteBase == "" {
		t.Skip("explicit existing credentials and approved remote root required")
	}
	if remoteBase != "/BaiduPCS-Go-P2-Verification" {
		t.Fatal("unapproved verification root")
	}
	var input struct {
		Credentials credentialLiveInput `json:"credentials"`
	}
	if json.Unmarshal([]byte(credentialJSON), &input) != nil {
		t.Fatal("invalid private credential input")
	}
	v := input.Credentials
	c, e := account.Normalize(account.Import{Kind: v.Kind, BDUSS: v.BDUSS, PToken: v.PToken, SToken: v.SToken, SBoxTKN: v.SBoxTKN, BAIDUID: v.BAIDUID, Cookies: v.Cookies, AccessToken: v.AccessToken})
	if e != nil {
		t.Fatal("invalid credential format")
	}
	c.AccountID = "p6_live_setup"
	c.Revision = 1
	stages := map[string]string{}
	report := map[string]any{"schema_version": 1, "started_at": time.Now().UTC().Format(time.RFC3339Nano), "go_version": runtime.Version(), "platform": runtime.GOOS + "/" + runtime.GOARCH, "scope": "P6 existing cookie; new dedicated directory only; login, account-wide delta and CLI config use controlled evidence", "credential_kind": v.Kind, "tls_certificate_verification": true, "allowed_remote_root": remoteBase + "/", "stages": stages, "not_claimed_as_upstream_verified": []string{"password/captcha/SMS/email/QR login confirmation", "FIL12 account-wide changes", "multiple accounts", "full 128GiB transfers", "disk-full and SIGKILL injection"}}
	defer func() {
		report["finished_at"] = time.Now().UTC().Format(time.RFC3339Nano)
		report["passed"] = !t.Failed()
		if name := os.Getenv("BAIDUPCS_P6_LIVE_REPORT"); name != "" {
			raw, _ := json.MarshalIndent(report, "", "  ")
			if e := os.WriteFile(name, append(raw, '\n'), 0600); e != nil {
				t.Error("cannot save sanitized live report")
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	clients := baidu.NewClientFactory()
	defer clients.Close()
	cfg := baidu.DefaultConfiguration()
	cfg.Proxy = os.Getenv("HTTPS_PROXY")
	setup, e := clients.New(ctx, configuration.NewSnapshot(1, cfg), c)
	if e != nil {
		t.Fatal("cannot bind existing credential")
	}
	defer setup.Close()
	if _, e = clients.ValidateAccount(ctx, configuration.NewSnapshot(1, cfg), c); e != nil {
		stages["existing_credential_validate"] = safeUpstreamCode(e)
		t.Fatal("existing credential validation failed; no new login attempted")
	}
	stages["existing_credential_validate"] = "passed"
	var nonce [12]byte
	if _, e = rand.Read(nonce[:]); e != nil {
		t.Fatal(e)
	}
	runDir := remoteBase + "/p6-" + hex.EncodeToString(nonce[:])
	if e = setup.PCS.Mkdir(runDir); e != nil {
		stages["setup"] = safeUpstreamCode(e)
		t.Fatal("cannot create this run directory")
	}
	defer func() {
		clean, done := context.WithTimeout(context.Background(), 30*time.Second)
		defer done()
		s, e := clients.New(clean, configuration.NewSnapshot(1, cfg), c)
		if e == nil {
			e = s.PCS.Remove(runDir)
			s.Close()
		}
		if e != nil {
			stages["cleanup"] = safeUpstreamCode(e)
			t.Error("dedicated cleanup failed")
		} else {
			stages["cleanup"] = "passed"
		}
	}()
	options, _ := testOptions(t)
	raw, _ := os.ReadFile(options.ConfigFile)
	var startupConfig startup
	json.Unmarshal(raw, &startupConfig)
	root := t.TempDir()
	startupConfig.Roots = []localfs.Root{{ID: "p6live", Path: root, Read: true, Write: true}}
	raw, _ = json.Marshal(startupConfig)
	os.WriteFile(options.ConfigFile, raw, 0600)
	rt, e := New(ctx, options)
	if e != nil {
		t.Fatal("cannot assemble service")
	}
	defer rt.Close()
	h := rt.Handler
	if cfg.Proxy != "" {
		changed := request(t, h, "PATCH", "/api/v1/configuration", testToken, `"1"`, "", p5JSON(map[string]string{"proxy": cfg.Proxy}))
		if changed.Code != 200 {
			t.Fatal("cannot bind configured session proxy")
		}
	}
	imported := request(t, h, "POST", "/api/v1/accounts", testToken, "", "", credentialJSON)
	if imported.Code != 201 {
		t.Fatal("HTTP credential import failed")
	}
	id := dataMap(t, imported)["account_id"].(string)
	base := "/api/v1/accounts/" + id
	contextReply := p3Wire(t, h, "CTX01", "POST", "/api/v1/contexts", p5JSON(map[string]string{"account_id": id, "cwd": runDir}), nil, 201, nil)
	contextID := dataMap(t, contextReply)["context_id"].(string)
	stages["CTX01"] = "passed"
	content := bytesForP6Live()
	os.WriteFile(filepath.Join(root, "source.txt"), content, 0600)
	upload := p5Submit(t, h, nil, "UPL01", base, "/uploads", p5JSON(map[string]any{"sources": []any{map[string]string{"type": "local", "root_id": "p6live", "relative_path": "source.txt"}}, "target_dir": ".", "context_id": contextID, "options": map[string]any{"file_concurrency": 1, "parallel_per_file": 1, "max_retries": 0, "policy": "skip", "skip_rapid_check": true}}))
	p3Wait(t, h, upload, "succeeded")
	stages["context_upload_fixture"] = "passed"
	inspected := p3Wire(t, h, "INT01", "POST", base+"/file-checksums/inspect", p5JSON(map[string]any{"paths": []string{"source.txt"}, "context_id": contextID}), nil, 200, nil)
	items := dataMap(t, inspected)["items"].([]any)
	if items[0].(map[string]any)["status"] != "succeeded" {
		stages["INT01"] = "upstream_checksum_unavailable"
	} else {
		stages["INT01"] = "passed"
	}
	for _, test := range []struct {
		id, route string
		body      any
	}{{"INT04", "/file-verifications", map[string]any{"items": []any{map[string]any{"path": "source.txt", "local_file": map[string]string{"root_id": "p6live", "relative_path": "source.txt"}}}, "context_id": contextID, "options": map[string]any{"algorithms": []string{"size", "md5"}}}}, {"EXP01", "/exports", map[string]any{"paths": []string{"source.txt"}, "context_id": contextID, "options": map[string]any{"format": "jsonl", "recursive": false, "max_retries": 0}}}, {"INT02", "/file-checksums/fix", map[string]any{"paths": []string{"source.txt"}, "context_id": contextID}}} {
		job := p5Submit(t, h, nil, test.id, base, test.route, p5JSON(test.body))
		done := p3Wait(t, h, job, "succeeded", "partially_succeeded", "failed", "interrupted")
		state := dataMap(t, done)["state"].(string)
		stages[test.id] = state
		if state != "succeeded" {
			result := p3Wire(t, h, "JOB03", "GET", "/api/v1/jobs/"+job+"/items", "", nil, 200, nil)
			var items struct {
				Data []struct {
					Problem *struct {
						Code string `json:"code"`
					} `json:"problem"`
				}
			}
			json.Unmarshal(result.Body.Bytes(), &items)
			if len(items.Data) > 0 && items.Data[0].Problem != nil {
				stages[test.id] += "/" + items.Data[0].Problem.Code
			}
		}
	}
	links := p3Wire(t, h, "DWN02", "POST", base+"/download-links", p5JSON(map[string]any{"selector": map[string]any{"kind": "paths", "values": []string{"source.txt"}}, "context_id": contextID, "source": "direct"}), nil, 200, nil)
	l := dataMap(t, links)["items"].([]any)[0].(map[string]any)
	if l["status"] == "succeeded" {
		ref := l["links"].([]any)[0].(map[string]any)["link_ref"].(string)
		sum := sha256.Sum256(content)
		read := request(t, h, "POST", base+"/file-checksums/from-link", testToken, "", "", p5JSON(map[string]any{"link_ref": ref, "expected_checksum": map[string]string{"algorithm": "sha256", "value": hex.EncodeToString(sum[:])}}))
		if read.Code == 200 {
			stages["INT05"] = "passed"
		} else {
			stages["INT05"] = "upstream_rejected"
		}
	} else {
		stages["INT05"] = "link_unavailable"
	}
	pcs := request(t, h, "GET", base+"/diagnostics/pcs-servers", testToken, "", "", "")
	stages["SYS06"] = "upstream_unavailable"
	if pcs.Code == 200 {
		stages["SYS06"] = "passed"
	}
	p3Wire(t, h, "CTX02", "GET", "/api/v1/contexts/"+contextID, "", nil, 200, nil)
	p3Wire(t, h, "CTX03", "PATCH", "/api/v1/contexts/"+contextID, `{"cwd":".","include_listing":true}`, map[string]string{"If-Match": `"1"`}, 200, nil)
	p3Wire(t, h, "CTX04", "DELETE", "/api/v1/contexts/"+contextID, "", nil, 204, nil)
	stages["CTX02_CTX03_CTX04"] = "passed"
	// Record only safe stage outcomes. No account, link, UK, cookie or proxy value.
}
func bytesForP6Live() []byte {
	return []byte(strings.Repeat("BaiduPCS-Go P6 dedicated existing-session verification.\n", 97))
}

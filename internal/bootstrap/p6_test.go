package bootstrap

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/infrastructure/identity"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/infrastructure/localfs"
	"hash/crc32"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const p6Other = "P6_OTHER_ADMIN_TEST_TOKEN_0123456789abcdef"

type p6Upstream struct {
	*writeUpstream
	image           []byte
	diffUnavailable bool
	dropFix         bool
}

func (u *p6Upstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	respond := func(v any) { w.Header().Set("Content-Type", "application/json"); json.NewEncoder(w).Encode(v) }
	if r.Host == "api.ipify.org" {
		fmt.Fprint(w, "203.0.113.7")
		return
	}
	if r.URL.Path == "/wp/api/security/antireplaytoken" {
		respond(map[string]any{"errno": 0, "time": "abc123"})
		return
	}
	if strings.HasPrefix(r.URL.Path, "/static/touch/js/") {
		fmt.Fprint(w, "default key fixture")
		return
	}
	if r.URL.Path == "/" || r.URL.Path == "/disk/main" {
		fmt.Fprint(w, "login fixture")
		return
	}
	if r.URL.Path == "/wp/api/login" {
		r.ParseForm()
		username := r.Form.Get("username")
		if (username == "captcha" || username == "unknown") && r.Form.Get("verifycode") == "" {
			no := "500001"
			if username == "unknown" {
				no = "400408"
			}
			respond(map[string]any{"errInfo": map[string]string{"no": no}, "data": map[string]string{"codeString": "PRIVATE_CAPTCHA_STATE"}})
			return
		}
		if (username == "sms" || username == "email" || username == "sms2" || username == "email2") && r.Form.Get("verifycode") == "" {
			if _, e := r.Cookie("BDUSS"); e != nil {
				fields := map[string]string{"phone": "***", "authid": "PRIVATE_AUTH_ID", "verifyUrl": "https://wappass.baidu.com/verify?authid=PRIVATE_AUTH_ID&x=1", "loginProxy": "https://wappass.baidu.com/login-proxy", "authsid": "PRIVATE_AUTH_SID"}
				if strings.HasPrefix(username, "email") {
					delete(fields, "phone")
					fields["email"] = "***"
				}
				if strings.HasSuffix(username, "2") {
					delete(fields, "authid")
					fields["verifyUrl"] = "https://wappass.baidu.com/verify?token=PRIVATE_VERIFY_TOKEN&x=1"
					fields["token"] = "PRIVATE_VERIFY_TOKEN"
					fields["u"] = "https://wappass.baidu.com/login-proxy"
				}
				respond(map[string]any{"errInfo": map[string]string{"no": "400023"}, "data": fields})
				return
			}
		}
		loginCookies(w)
		respond(map[string]any{"errInfo": map[string]string{"no": "0"}, "data": map[string]string{}})
		return
	}
	if r.URL.Path == "/v3/api/auth/widget" {
		var in struct {
			Action string `json:"action"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		if in.Action == "send" {
			respond(map[string]string{"send": "success", "status": "unauth"})
		} else {
			respond(map[string]string{"check": "success", "status": "authed"})
		}
		return
	}
	if r.URL.Path == "/passport/authwidget" {
		if r.URL.Query().Get("action") == "send" {
			fmt.Fprint(w, `<p class="mod-tipinfo-subtitle"> 验证码已发送 </p>`)
		} else {
			fmt.Fprint(w, `jsonp1({"errInfo":{"no":"0"},"data":{"authsid":"PRIVATE_SECOND_AUTH_SID"}})`)
		}
		return
	}
	if r.URL.Path == "/login-proxy" {
		loginCookies(w)
		fmt.Fprint(w, "verified")
		return
	}
	if r.URL.Path == "/cgi-bin/genimage" || r.URL.Path == "/qrcode-image" {
		w.Header().Set("Content-Type", "image/png")
		w.Write(u.image)
		return
	}
	if r.URL.Path == "/v2/api/getqrcode" {
		respond(map[string]any{"errno": 0, "sign": "PRIVATE_QR_SIGN", "imgurl": "passport.baidu.com/qrcode-image"})
		return
	}
	if r.URL.Path == "/channel/unicast" {
		respond(map[string]any{"errno": 0, "channel_v": `{"status":0,"v":"PRIVATE_QR_EXCHANGE"}`})
		return
	}
	if r.URL.Path == "/v3/login/main/qrbdusslogin" {
		loginCookies(w)
		fmt.Fprint(w, `bd__cbs__login({"errInfo":{"no":0}})`)
		return
	}
	if r.URL.Path == "/v3/login/api/auth/" {
		respond(map[string]any{"errno": 0})
		return
	}
	op := r.URL.Query().Get("method")
	if r.URL.Path == "/rest/2.0/pcs/quota" {
		respond(map[string]any{"quota": 1000000, "used": 12})
		return
	}
	if r.URL.Path == "/api/batch/filediff" {
		if u.diffUnavailable {
			respond(map[string]any{"errno": -1})
			return
		}
		respond(map[string]any{"errno": 0, "cursor": "opaque-upstream-cursor", "has_more": 1, "reset": 0, "entries": []any{map[string]any{"fs_id": 9007199254740993, "path": "/round/file", "size": 5, "isdir": 0, "md5": writeDigest([]byte("hello"))}, map[string]any{"fs_id": 100, "isdelete": 1}}})
		return
	}
	if op == "locateupload" {
		respond(map[string]any{"servers": []any{map[string]any{"server": "https://c.pcs.baidu.com"}}, "server": []string{"pcs.baidu.com"}})
		return
	}
	if op == "locatedownload" {
		respond(map[string]any{"urls": []any{map[string]string{"url": "https://pcs.baidu.com/rest/2.0/pcs/file?method=download&path=" + url.QueryEscape(r.URL.Query().Get("path"))}}})
		return
	}
	if op == "rapidupload" || op == "create" && strings.Contains(r.URL.Path, "xpan/file") {
		if u.dropFix {
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
			return
		}
		respond(map[string]any{"error_code": 0})
		return
	}
	if op == "download" {
		cookie, e := r.Cookie("BDUSS")
		if e != nil {
			http.Error(w, "cookie required", 401)
			return
		}
		u.mu.Lock()
		v, ok := u.files[cookie.Value+"\x00"+r.URL.Query().Get("path")]
		u.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		start, last := int64(0), int64(len(v.Body)-1)
		if strings.Contains(v.Path, "redirect") {
			w.Header().Set("Location", "https://127.0.0.1/secret")
			w.WriteHeader(302)
			return
		}
		w.Header().Set("Content-MD5", writeDigest(v.Body))
		if !strings.Contains(v.Path, "no-crc") {
			w.Header().Set("x-bs-meta-crc32", strconv.FormatUint(uint64(crc32.ChecksumIEEE(v.Body)), 10))
		}
		if r.Header.Get("Range") != "" && len(v.Body) > 0 {
			fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &last)
			last = min(last, int64(len(v.Body)-1))
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, last, len(v.Body)))
			w.Header().Set("Content-Length", fmt.Sprint(last-start+1))
			w.WriteHeader(206)
		} else {
			w.Header().Set("Content-Length", fmt.Sprint(len(v.Body)))
		}
		if last >= 0 {
			w.Write(v.Body[start : last+1])
		}
		return
	}
	u.writeUpstream.ServeHTTP(w, r)
}
func loginCookies(w http.ResponseWriter) {
	for _, c := range []*http.Cookie{{Name: "BDUSS", Value: "P6_OWNER"}, {Name: "STOKEN", Value: "P6_TEST_STOKEN"}, {Name: "BAIDUID", Value: "P6_TEST_BAIDUID"}} {
		c.Domain = ".baidu.com"
		c.Path = "/"
		c.Secure = true
		http.SetCookie(w, c)
	}
}
func p6Runtime(t *testing.T) (*Runtime, Options, *p6Upstream, string, *lockedBuffer) {
	t.Helper()
	o, logs := testOptions(t)
	raw, _ := os.ReadFile(o.ConfigFile)
	var cfg startup
	json.Unmarshal(raw, &cfg)
	root := t.TempDir()
	cfg.Roots = []localfs.Root{{ID: "p6root", Path: root, Read: true, Write: true}}
	sum := sha256.Sum256([]byte(p6Other))
	cfg.Principals = append(cfg.Principals, identity.Record{ID: "p6other", TokenSHA256: hex.EncodeToString(sum[:]), Scopes: []string{"admin"}, Accounts: []string{"*"}, Roots: []string{"p6root"}})
	cfg.LegacyAccountsFile = filepath.Join(t.TempDir(), "cli.json")
	legacy := map[string]any{"baidu_active_uid": 1, "baidu_user_list": []any{map[string]any{"uid": 1, "name": "test", "bduss": "P6_OWNER", "workdir": "/round"}}}
	b, _ := json.Marshal(legacy)
	os.WriteFile(cfg.LegacyAccountsFile, b, 0600)
	raw, _ = json.Marshal(cfg)
	os.WriteFile(o.ConfigFile, raw, 0600)
	var imageBytes bytes.Buffer
	png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 16, 16)))
	u := &p6Upstream{writeUpstream: newWriteUpstream(), image: imageBytes.Bytes()}
	u.seed("P6_OWNER", "/", "", true)
	u.seed("P6_OWNER", "/round", "", true)
	u.seed("P6_OWNER", "/other", "", true)
	u.seed("P6_OWNER", "/round/file", "hello", false)
	u.seed("P6_OWNER", "/round/no-crc", "no checksum", false)
	u.seed("P6_OWNER", "/round/nested", "", true)
	u.seed("P6_OWNER", "/round/nested/deep", "deep", false)
	o.ClientTransport = p3Transport(t, u)
	rt, e := New(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := rt.Close(); e != nil {
			t.Error(e)
		}
	})
	return rt, o, u, root, logs
}
func TestP6IntegrityExportAndLocalCryptoHTTP(t *testing.T) {
	rt, _, _, root, logs := p6Runtime(t)
	h := rt.Handler
	id := p3Account(t, h, "P6_OWNER", nil)
	base := "/api/v1/accounts/" + id
	os.WriteFile(filepath.Join(root, "input"), []byte("hello"), 0600)
	var rows []p3Response
	inspect := p3Wire(t, h, "INT01", "POST", base+"/file-checksums/inspect", `{"paths":["/round/file","/round/no-crc","/absent"]}`, nil, 200, &rows)
	if !strings.Contains(inspect.Body.String(), `"crc32":null`) || !strings.Contains(inspect.Body.String(), `"failed":"1"`) {
		t.Fatal(inspect.Body.String())
	}
	for _, test := range []struct{ id, route, body, state string }{{"INT02", "/file-checksums/fix", `{"paths":["/round/file","/round/no-crc"]}`, "partially_succeeded"}, {"INT04", "/file-verifications", `{"items":[{"path":"/round/file","local_file":{"root_id":"p6root","relative_path":"input"}}]}`, "succeeded"}, {"EXP01", "/exports", `{"paths":["/round"],"options":{"recursive":true,"root_path":"virtual","format":"jsonl","max_retries":0}}`, "succeeded"}} {
		job := p5Submit(t, h, &rows, test.id, base, test.route, test.body)
		p6Wait(t, h, job, test.state)
		result := p3Wire(t, h, "JOB04", "GET", "/api/v1/jobs/"+job+"/result", "", nil, 200, &rows)
		if test.id == "EXP01" {
			artifact := dataMap(t, result)["result"].(map[string]any)["artifact_ids"].([]any)[0].(string)
			content := request(t, h, "GET", "/api/v1/artifacts/"+artifact+"/content", testToken, "", "", "")
			if !strings.Contains(content.Body.String(), "virtual/round/nested/deep") || strings.Contains(content.Body.String(), "P6_OWNER") {
				t.Fatal("export root rewrite or redaction")
			}
		}
	}
	for _, format := range []string{"legacy_command", "legacy_link"} {
		job := p5Submit(t, h, nil, "EXP01", base, "/exports", fmt.Sprintf(`{"paths":["/round/file","/round/no-crc"],"options":{"format":%q,"root_path":"relative","destination":{"root_id":"p6root","directory":"exports"}}}`, format))
		state := "partially_succeeded"
		if format == "legacy_link" {
			state = "succeeded"
		}
		p6Wait(t, h, job, state)
	}
	job := p5Submit(t, h, &rows, "INT03", "", "/api/v1/local/checksums", `{"root_id":"p6root","paths":["input"],"options":{"algorithms":["md5","slice_md5","crc32","block_md5"]}}`)
	p6Wait(t, h, job, "succeeded")
	p3Wire(t, h, "JOB04", "GET", "/api/v1/jobs/"+job+"/result", "", nil, 200, &rows)
	links := p3Wire(t, h, "DWN02", "POST", base+"/download-links", `{"selector":{"kind":"paths","values":["/round/file"]},"source":"direct"}`, nil, 200, nil)
	items := dataMap(t, links)["items"].([]any)
	link := items[0].(map[string]any)["links"].([]any)[0].(map[string]any)["link_ref"].(string)
	p3Wire(t, h, "INT05", "POST", base+"/file-checksums/from-link", p5JSON(map[string]any{"link_ref": link}), nil, 200, &rows)
	sum := sha256.Sum256([]byte("hello"))
	p3Wire(t, h, "INT05", "POST", base+"/file-checksums/from-link", p5JSON(map[string]any{"link_ref": link, "expected_checksum": map[string]string{"algorithm": "sha256", "value": hex.EncodeToString(sum[:])}}), nil, 200, &rows)
	p3Wire(t, h, "INT05", "POST", base+"/file-checksums/from-link", `{"link_ref":"https://localhost/secrets"}`, nil, 400, nil)
	p3Wire(t, h, "INT06", "POST", base+"/legacy/rapid-uploads", `{"path":"/round/old","size_bytes":"5","md5":"5d41402abc4b2a76b9719d911017c592","slice_md5":"5d41402abc4b2a76b9719d911017c592","crc32":"907060870"}`, nil, 503, &rows)
	p3Wire(t, h, "FIL12", "GET", base+"/file-changes?cursor=original-cursor", "", nil, 200, &rows)
	encBody := `{"sources":[{"root_id":"p6root","relative_path":"input"}],"destination":{"root_id":"p6root","directory":"enc"},"options":{"method":"aes-256-ctr"},"key":"P6_SECRET_KEY_NEVER_PUBLIC"}`
	job = p5Submit(t, h, &rows, "LOC02", "", "/api/v1/local/encryptions", encBody)
	done := p6Wait(t, h, job, "succeeded")
	if strings.Contains(done.Body.String(), "P6_SECRET_KEY") {
		t.Fatal("key in job")
	}
	if _, e := os.Stat(filepath.Join(root, "input")); e != nil {
		t.Fatal("source removed by default")
	}
	decBody := `{"sources":[{"root_id":"p6root","relative_path":"enc/input.encrypt"}],"destination":{"root_id":"p6root","directory":"dec"},"options":{"method":"aes-256-ctr","remove_source":true},"key":"P6_SECRET_KEY_NEVER_PUBLIC"}`
	job = p5Submit(t, h, &rows, "LOC03", "", "/api/v1/local/decryptions", decBody)
	p6Wait(t, h, job, "succeeded")
	out, _ := os.ReadFile(filepath.Join(root, "dec/input"))
	if string(out) != "hello" {
		t.Fatal("crypto round trip")
	}
	if _, e := os.Stat(filepath.Join(root, "enc/input.encrypt")); !os.IsNotExist(e) {
		t.Fatal("explicit source removal ignored")
	}
	job = p5Submit(t, h, nil, "LOC02", "", "/api/v1/local/encryptions", encBody)
	p6Wait(t, h, job, "succeeded")
	bad := strings.ReplaceAll(decBody, "P6_SECRET_KEY_NEVER_PUBLIC", "wrong")
	job = p5Submit(t, h, nil, "LOC03", "", "/api/v1/local/decryptions", bad)
	p6Wait(t, h, job, "failed")
	p3Wire(t, h, "JOB04", "GET", "/api/v1/jobs/"+job+"/result", "", nil, 200, &rows)
	if strings.Contains(logs.String(), "P6_SECRET_KEY_NEVER_PUBLIC") || strings.Contains(logs.String(), "P6_OWNER") {
		t.Fatal("secret in logs")
	}
	p5Export(t, "BAIDUPCS_P6_TOOL_RESPONSES", rows)
}
func TestP6ContextsCLIAndReloadHTTP(t *testing.T) {
	rt, o, _, _, _ := p6Runtime(t)
	h := rt.Handler
	var rows []p3Response
	cli := p3Wire(t, h, "CFG08", "GET", "/api/v1/configuration/cli-defaults", "", nil, 200, &rows)
	mapping := dataMap(t, cli)
	id := mapping["default_account_id"].(string)
	base := "/api/v1/accounts/" + id
	contextReply := p3Wire(t, h, "CTX01", "POST", "/api/v1/contexts", p5JSON(map[string]any{"account_id": id, "cwd": "/round"}), nil, 201, &rows)
	cid := dataMap(t, contextReply)["context_id"].(string)
	route := "/api/v1/contexts/" + cid
	p3Wire(t, h, "CTX02", "GET", route, "", nil, 200, &rows)
	p3Wire(t, h, "CTX03", "PATCH", route, `{"cwd":"../other","include_listing":true}`, map[string]string{"If-Match": `"1"`}, 200, &rows)
	p3Wire(t, h, "FIL03", "POST", base+"/files/metadata", p5JSON(map[string]any{"paths": []string{"../round/file"}, "context_id": cid}), nil, 200, nil)
	denied := request(t, h, "GET", route, p6Other, "", "", "")
	wantStatus(t, denied, 403)
	p3Wire(t, h, "CTX03", "PATCH", route, `{"cwd":"/round"}`, map[string]string{"If-Match": `"1"`}, 412, nil)
	body := p5JSON(map[string]any{"default_account_id": nil, "accounts": []any{map[string]string{"account_id": id, "cwd": "/other"}}})
	p3Wire(t, h, "CFG09", "PATCH", "/api/v1/configuration/cli-defaults", body, map[string]string{"If-Match": cli.Header().Get("ETag"), "Idempotency-Key": "cli-once"}, 200, &rows)
	p3Wire(t, h, "CFG09", "PATCH", "/api/v1/configuration/cli-defaults", body, map[string]string{"If-Match": cli.Header().Get("ETag"), "Idempotency-Key": "cli-once"}, 200, nil)
	raw, _ := os.ReadFile(o.ConfigFile)
	var config startup
	json.Unmarshal(raw, &config)
	config.Client = json.RawMessage(`{"max_parallel":3}`)
	raw, _ = json.Marshal(config)
	os.WriteFile(o.ConfigFile, raw, 0600)
	p3Wire(t, h, "CFG06", "POST", "/api/v1/configuration/reload", `{"source":"service"}`, map[string]string{"If-Match": `"1"`}, 200, &rows)
	p3Wire(t, h, "CFG06", "POST", "/api/v1/configuration/reload", `{"source":"service","dry_run":false}`, map[string]string{"If-Match": `"1"`, "Idempotency-Key": "reload-once"}, 200, &rows)
	p3Wire(t, h, "CFG06", "POST", "/api/v1/configuration/reload", `{"source":"service","dry_run":false}`, map[string]string{"If-Match": `"1"`, "Idempotency-Key": "reload-once"}, 200, nil)
	config.Listen = "0.0.0.0:9999"
	raw, _ = json.Marshal(config)
	os.WriteFile(o.ConfigFile, raw, 0600)
	p3Wire(t, h, "CFG06", "POST", "/api/v1/configuration/reload", `{"source":"service","dry_run":false}`, map[string]string{"If-Match": `"2"`}, 422, nil)
	p3Wire(t, h, "SYS05", "GET", "/api/v1/diagnostics/network", "", nil, 200, &rows)
	p3Wire(t, h, "SYS05", "GET", "/api/v1/diagnostics/network?active_probe=true", "", nil, 200, &rows)
	p3Wire(t, h, "SYS06", "GET", base+"/diagnostics/pcs-servers", "", nil, 200, &rows)
	p3Wire(t, h, "CTX04", "DELETE", route, "", nil, 204, &rows)
	p3Wire(t, h, "CTX02", "GET", route, "", nil, 410, nil)
	p5Export(t, "BAIDUPCS_P6_MANAGEMENT_RESPONSES", rows)
}
func TestP6LoginSessionsHTTP(t *testing.T) {
	rt, _, _, _, logs := p6Runtime(t)
	h := rt.Handler
	var rows []p3Response
	for _, username := range []string{"simple", "captcha", "sms", "email", "sms2", "email2"} {
		created := p3Wire(t, h, "LOG01", "POST", "/api/v1/account-login-sessions", fmt.Sprintf(`{"method":"password","username":%q,"password":"P6_TEST_PASSWORD"}`, username), nil, 201, &rows)
		sid := dataMap(t, created)["session_id"].(string)
		route := "/api/v1/account-login-sessions/" + sid
		v := dataMap(t, created)
		if username == "captcha" {
			p3Wire(t, h, "LOG03", "GET", route+"/qrcode", "", nil, 200, &rows)
		}
		for v["state"] == "challenge_required" {
			c := v["challenge"].(map[string]any)
			answer := "1234"
			if (c["type"] == "sms" || c["type"] == "email") && strings.Contains(c["prompt"].(string), "send") {
				answer = "send"
			}
			respond := p3Wire(t, h, "LOG04", "POST", route+"/responses", p5JSON(map[string]string{"challenge_id": c["challenge_id"].(string), "response": answer}), nil, 200, &rows)
			v = dataMap(t, respond)
		}
		denied := request(t, h, "GET", route, p6Other, "", "", "")
		wantStatus(t, denied, 403)
		p3Wire(t, h, "LOG05", "POST", route+"/complete", `{"bduss":"not-an-exchange"}`, nil, 400, nil)
		complete := p3Wire(t, h, "LOG05", "POST", route+"/complete", `{"label":"http-login"}`, nil, 200, &rows)
		again := p3Wire(t, h, "LOG05", "POST", route+"/complete", `{"label":"http-login"}`, nil, 200, nil)
		if dataMap(t, complete)["account_id"] != dataMap(t, again)["account_id"] {
			t.Fatal("login complete not idempotent")
		}
		p3Wire(t, h, "LOG06", "DELETE", route, "", nil, 204, &rows)
		p3Wire(t, h, "LOG05", "POST", route+"/complete", `{}`, nil, 410, nil)
	}
	qr := p3Wire(t, h, "LOG01", "POST", "/api/v1/account-login-sessions", `{"method":"qrcode"}`, map[string]string{"Idempotency-Key": "qr-create"}, 201, &rows)
	sid := dataMap(t, qr)["session_id"].(string)
	route := "/api/v1/account-login-sessions/" + sid
	p3Wire(t, h, "LOG03", "GET", route+"/qrcode", "", nil, 200, &rows)
	p3Wire(t, h, "LOG02", "GET", route, "", nil, 200, &rows)
	p3Wire(t, h, "LOG05", "POST", route+"/complete", `{}`, nil, 200, &rows)
	p3Wire(t, h, "LOG01", "POST", "/api/v1/account-login-sessions", `{"method":"qrcode"}`, map[string]string{"Idempotency-Key": "qr-create"}, 201, nil)
	for _, secret := range []string{"PRIVATE_QR_SIGN", "PRIVATE_QR_EXCHANGE", "PRIVATE_CAPTCHA_STATE", "PRIVATE_AUTH_ID", "P6_TEST_PASSWORD", "P6_OWNER"} {
		for _, row := range rows {
			if b, _ := json.Marshal(row.Body); bytes.Contains(b, []byte(secret)) {
				t.Fatalf("login response leaked %s", secret)
			}
		}
		if strings.Contains(logs.String(), secret) {
			t.Fatal("login log leak")
		}
	}
	p5Export(t, "BAIDUPCS_P6_LOGIN_RESPONSES", rows)
}
func TestP6ReceptionOffsetRecoveryAndHTTP(t *testing.T) {
	rt, o, _, _, _ := p6Runtime(t)
	h := rt.Handler
	var rows []p3Response
	sum := md5.Sum([]byte("hello"))
	body := p5JSON(map[string]any{"filename": "received.txt", "size_bytes": "5", "expected_checksum": map[string]string{"algorithm": "md5", "value": hex.EncodeToString(sum[:])}})
	created := p3Wire(t, h, "UPL05", "POST", "/api/v1/staging-upload-sessions", body, map[string]string{"Idempotency-Key": "receive-once"}, 201, &rows)
	sid := dataMap(t, created)["session_id"].(string)
	route := "/api/v1/staging-upload-sessions/" + sid
	patch := func(offset int, content string, status int) {
		req := httptest.NewRequest("PATCH", route+"/content", strings.NewReader(content))
		req.Header.Set("Authorization", "Bearer "+testToken)
		req.Header.Set("Content-Type", "application/octet-stream")
		req.Header.Set("Upload-Offset", fmt.Sprint(offset))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		wantStatus(t, rec, status)
		if status == 200 {
			var b any
			json.Unmarshal(rec.Body.Bytes(), &b)
			rows = append(rows, p3Response{ID: "UPL07", Method: "PATCH", Status: rec.Code, ContentType: rec.Header().Get("Content-Type"), Headers: rec.Header(), Body: b})
		}
	}
	patch(0, "he", 200)
	patch(0, "ll", 409)
	p3Wire(t, h, "UPL08", "POST", route+"/complete", "", nil, 409, nil)
	p3Wire(t, h, "UPL06", "GET", route, "", nil, 200, &rows)
	// Simulate a killed process after file fsync and before offset COMMIT.
	r, e := rt.store.Resource(context.Background(), "reception", sid)
	if e != nil {
		t.Fatal(e)
	}
	var v struct{ StageID string }
	json.Unmarshal(r.Data, &v)
	f, e := os.OpenFile(filepath.Join(rt.DataDir, "staging", v.StageID), os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	f.WriteString("UNCOMMITTED")
	f.Sync()
	f.Close()
	if e = rt.Close(); e != nil {
		t.Fatal(e)
	}
	next, e := New(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	defer next.Close()
	h = next.Handler
	p3Wire(t, h, "UPL06", "GET", route, "", nil, 200, nil)
	patch(2, "llo", 200)
	complete := p3Wire(t, h, "UPL08", "POST", route+"/complete", "", nil, 201, &rows)
	again := p3Wire(t, h, "UPL08", "POST", route+"/complete", "", nil, 201, nil)
	if dataMap(t, complete)["staged_file_id"] != dataMap(t, again)["staged_file_id"] {
		t.Fatal("complete changed stage id")
	}
	patch(5, "x", 409)
	p3Wire(t, h, "UPL09", "DELETE", route, "", nil, 204, &rows)
	zero := p3Wire(t, h, "UPL05", "POST", "/api/v1/staging-upload-sessions", `{"filename":"zero","size_bytes":"0"}`, nil, 201, nil)
	p3Wire(t, h, "UPL08", "POST", "/api/v1/staging-upload-sessions/"+dataMap(t, zero)["session_id"].(string)+"/complete", "", nil, 201, nil)
	p5Export(t, "BAIDUPCS_P6_RECEPTION_RESPONSES", rows)
}

func p6Wait(t *testing.T, h http.Handler, id string, wanted string) *httptest.ResponseRecorder {
	t.Helper()
	var last *httptest.ResponseRecorder
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		last = p3Wire(t, h, "JOB02", "GET", "/api/v1/jobs/"+id, "", nil, 200, nil)
		state := dataMap(t, last)["state"].(string)
		if state == wanted {
			return last
		}
		if state == "failed" || state == "succeeded" || state == "partially_succeeded" {
			t.Fatalf("job state %s want %s: %s", state, wanted, last.Body.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("job deadline: %s", last.Body.String())
	return nil
}

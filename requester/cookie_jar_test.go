package requester

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func mustURL(raw string) *url.URL { u, _ := url.Parse(raw); return u }
func valueAt(jar http.CookieJar, raw, name string) string {
	for _, c := range jar.Cookies(mustURL(raw)) {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}

func TestCookieSnapshotKeepsScopeAndDeletion(t *testing.T) {
	jar, err := NewCookieJar(nil)
	if err != nil {
		t.Fatal(err)
	}
	jar.SetCookies(mustURL("https://passport.baidu.com/v3/login"), []*http.Cookie{
		{Name: "STOKEN", Value: "passport-test", Domain: ".passport.baidu.com", Path: "/", Secure: true, HttpOnly: true},
		{Name: "PTOKEN", Value: "host-only-test", Path: "/v3", Secure: true},
		{Name: "BDUSS", Value: "bduss-test", Domain: ".baidu.com", Path: "/", MaxAge: 3600},
	})
	jar.SetCookies(mustURL("https://pan.baidu.com/disk/main"), []*http.Cookie{
		{Name: "STOKEN", Value: "pan-test", Domain: ".pan.baidu.com", Path: "/", Secure: true},
		{Name: "deleted", Value: "remove-me", Path: "/"},
		{Name: "invalid-domain", Value: "must-not-save", Domain: ".example.org", Path: "/"},
	})
	jar.SetCookies(mustURL("https://pan.baidu.com/"), []*http.Cookie{{Name: "deleted", MaxAge: -1, Path: "/"}})
	data, err := json.Marshal(jar.Entries())
	if err != nil {
		t.Fatal(err)
	}
	var entries []CookieEntry
	if err = json.Unmarshal(data, &entries); err != nil {
		t.Fatal(err)
	}
	restored, err := NewCookieJar(entries)
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range []http.CookieJar{jar, restored} {
		if got := valueAt(j, "https://pan.baidu.com/", "STOKEN"); got != "pan-test" {
			t.Fatalf("pan scope: %q", got)
		}
		if got := valueAt(j, "https://passport.baidu.com/", "STOKEN"); got != "passport-test" {
			t.Fatalf("passport scope: %q", got)
		}
		if got := valueAt(j, "http://pan.baidu.com/", "STOKEN"); got != "" {
			t.Fatal("Secure cookie leaked to HTTP")
		}
		if got := valueAt(j, "https://tieba.baidu.com/", "STOKEN"); got != "" {
			t.Fatal("STOKEN leaked to sibling domain")
		}
		if valueAt(j, "https://passport.baidu.com/v3/ok", "PTOKEN") == "" {
			t.Fatal("path cookie not restored")
		}
		if valueAt(j, "https://passport.baidu.com/other", "PTOKEN") != "" {
			t.Fatal("path cookie escaped its path")
		}
		if valueAt(j, "https://sub.passport.baidu.com/v3/ok", "PTOKEN") != "" {
			t.Fatal("host-only cookie escaped its host")
		}
		if valueAt(j, "https://pan.baidu.com/", "BDUSS") == "" {
			t.Fatal("shared domain cookie lost")
		}
	}
	for _, entry := range entries {
		if entry.Name == "deleted" || entry.Name == "invalid-domain" {
			t.Fatalf("invalid/deleted cookie persisted: %s", entry.Name)
		}
		if entry.Name == "BDUSS" && entry.Expires <= time.Now().Unix() {
			t.Fatal("Max-Age did not become an expiry")
		}
		if strings.Contains(entry.String(), entry.Value) {
			t.Fatal("cookie String exposes value")
		}
	}
}

func TestExpiredCookieCannotBeResurrected(t *testing.T) {
	jar, err := NewCookieJar([]CookieEntry{{Name: "BDUSS", Value: "expired-test", Domain: "baidu.com", Path: "/", Expires: time.Now().Add(-time.Hour).Unix()}})
	if err != nil {
		t.Fatal(err)
	}
	if len(jar.Entries()) != 0 || len(jar.Cookies(mustURL("https://pan.baidu.com/"))) != 0 {
		t.Fatal("expired cookie restored")
	}
	for _, entry := range []CookieEntry{
		{Name: "x", Value: "test", Domain: "com", Path: "/"},
		{Name: "x", Value: "test", Domain: "evil.example/path", Path: "/"},
		{Name: "x", Value: "test", Domain: "baidu.com", Path: "relative"},
	} {
		if _, err := NewCookieJar([]CookieEntry{entry}); err == nil {
			t.Fatal("invalid stored attributes accepted")
		}
	}
}

func TestHTTPClientVerifiesCertificatesByDefault(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer server.Close()
	client := NewSecureHTTPClient()
	client.SetProxy("")
	client.transport.Proxy = nil
	client.transport.DialContext = nil
	if resp, err := client.Get(server.URL); err == nil {
		resp.Body.Close()
		t.Fatal("untrusted certificate accepted")
	}
	client.SetHTTPSecure(false)
	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	client.CloseIdleConnections()
}

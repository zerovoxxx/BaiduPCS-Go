package pcsconfig

import (
	"bytes"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcslogin"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

func fakeQRSession(uid uint64) *pcslogin.Session {
	return &pcslogin.Session{UID: uid, Name: "qr-test-user", Cookies: []requester.CookieEntry{
		{Name: "BDUSS", Value: "qr-bduss-test", Domain: "baidu.com", Path: "/"},
		{Name: "BAIDUID", Value: "qr-baiduid-test", Domain: "baidu.com", Path: "/"},
		{Name: "STOKEN", Value: "pan-test", Domain: "pan.baidu.com", Path: "/", Secure: true},
		{Name: "STOKEN", Value: "passport-test", Domain: "passport.baidu.com", Path: "/", Secure: true},
	}}
}

func newTestConfig(t *testing.T) *PCSConfig {
	t.Helper()
	c := NewConfig(filepath.Join(t.TempDir(), ConfigName))
	if err := c.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func scopedValue(jar http.CookieJar, host, name string) string {
	u, _ := url.Parse("https://" + host + "/")
	for _, cookie := range jar.Cookies(u) {
		if cookie.Name == name {
			return cookie.Value
		}
	}
	return ""
}

func TestQRAccountSurvivesRestartAndKeepsAccountSettings(t *testing.T) {
	c := newTestConfig(t)
	old := &Baidu{BaiduBase: BaiduBase{UID: 42, Name: "old-name"}, BDUSS: "old-test", Workdir: "/kept", AccessToken: "access-token-test", Sex: "test", Age: 3}
	other := &Baidu{BaiduBase: BaiduBase{UID: 55, Name: "other-test"}, BDUSS: "other-token-test", Workdir: "/"}
	c.BaiduUserList = BaiduUserList{old, other}
	c.setupNewUser(other)
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	account, err := c.SaveQRSession(fakeQRSession(42))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.BaiduUserList) != 2 || account.Workdir != "/kept" || account.AccessToken != "access-token-test" || account.Age != 3 {
		t.Fatal("refresh lost other accounts or user settings")
	}
	if c.ActiveUser() != account || c.BaiduActiveUID != 42 {
		t.Fatal("new account not activated after save")
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(c.configFilePath)
		if info.Mode().Perm() != 0600 {
			t.Fatal("credentials not private")
		}
	}
	c.Close()
	fresh := NewConfig(c.configFilePath)
	defer fresh.Close()
	if err = fresh.Init(); err != nil {
		t.Fatal(err)
	}
	if fresh.ActiveUser().UID != 42 || fresh.ActiveUser().Name != "qr-test-user" {
		t.Fatal("verified identity lost on restart")
	}
	jar := fresh.ActiveUserBaiduPCS().GetClient().Jar
	if scopedValue(jar, "pan.baidu.com", "STOKEN") != "pan-test" || scopedValue(jar, "passport.baidu.com", "STOKEN") != "passport-test" {
		t.Fatal("scoped cookies lost on restart")
	}
	if fresh.ActiveUserBaiduPCS().GetBAIDUID() != "qr-baiduid-test" {
		t.Fatal("BAIDUID lost when explicit STOKEN was saved")
	}
	fresh.ActiveUserBaiduPCS().UpdatePCSCookies(true)
	if scopedValue(jar, "passport.baidu.com", "STOKEN") != "passport-test" {
		t.Fatal("cookie update flattened scoped session")
	}
}

func TestQRSaveFailurePreservesOldFileAndActiveAccount(t *testing.T) {
	c := newTestConfig(t)
	old := &Baidu{BaiduBase: BaiduBase{UID: 42, Name: "old-test"}, BDUSS: "old-bduss-test", Workdir: "/old"}
	c.BaiduUserList = BaiduUserList{old}
	c.setupNewUser(old)
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(c.configFilePath)
	if err != nil {
		t.Fatal(err)
	}
	// A real serialization failure occurs before replacing the original file.
	old.Age = math.NaN()
	if _, err = c.SaveQRSession(fakeQRSession(42)); err == nil {
		t.Fatal("save failure reported as success")
	}
	after, err := os.ReadFile(c.configFilePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || c.ActiveUser() != old || len(c.BaiduUserList) != 1 || c.BaiduUserList[0] != old || c.BaiduActiveUID != 42 {
		t.Fatal("failed login replaced the existing account")
	}
	files, _ := filepath.Glob(filepath.Join(filepath.Dir(c.configFilePath), ".pcs-config-*"))
	if len(files) != 0 {
		t.Fatal("temporary config containing credentials was not removed")
	}
}

func TestAtomicSaveRenameFailureRemovesTemporaryCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing-directory")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	c := NewConfig(path)
	c.InitDefaultConfig()
	if err := c.Save(); err == nil {
		t.Fatal("directory was replaced by config")
	}
	files, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".pcs-config-*"))
	if len(files) != 0 {
		t.Fatal("temporary credentials left after rename failure")
	}
	info, _ := os.Stat(path)
	if !info.IsDir() {
		t.Fatal("old target removed")
	}
}

func TestLegacyCookiesKeepBAIDUIDWithExplicitSTOKEN(t *testing.T) {
	c := newTestConfig(t)
	b := &Baidu{BDUSS: "bduss-test", STOKEN: "explicit-test", COOKIES: "BDUSS=bduss-test; STOKEN=old-test; BAIDUID=baiduid-test"}
	pcs := b.newPCS(c)
	if pcs.GetBAIDUID() != "baiduid-test" || scopedValue(pcs.GetClient().Jar, "pan.baidu.com", "STOKEN") != "explicit-test" {
		t.Fatal("legacy full cookies lost with explicit token")
	}
	for _, header := range []string{"", "STOKEN=token-test", "XBDUSS=wrong-test;"} {
		if _, err := c.SetupUserByBDUSS("", "", "", header); err == nil {
			t.Fatal("missing BDUSS accepted")
		}
	}
	// ForceLogin avoids an external identity request when exercising legacy parsing.
	c.ForceLogin = "parse-test"
	if _, err := c.SetupUserByBDUSS("", "", "", "BDUSS=trailing-token-test"); err != nil {
		t.Fatal("BDUSS without trailing semicolon rejected")
	}
}

func TestExpiredStructuredCookiesDoNotFallBackToLegacySecrets(t *testing.T) {
	c := newTestConfig(t)
	b := &Baidu{BDUSS: "must-not-revive", COOKIES: "BDUSS=must-not-revive; STOKEN=legacy-test", CookieEntries: []requester.CookieEntry{
		{Name: "BDUSS", Value: "expired-test", Domain: "baidu.com", Path: "/", Expires: time.Now().Add(-time.Hour).Unix()},
	}}
	if b.newPCS(c).GetBDUSS() != "" {
		t.Fatal("expired structured session resurrected via legacy fields")
	}
}

func TestRefreshSameActiveQRAccountDoesNotSaveItsOldJar(t *testing.T) {
	c := newTestConfig(t)
	if _, err := c.SaveQRSession(fakeQRSession(42)); err != nil {
		t.Fatal(err)
	}
	newSession := fakeQRSession(42)
	for i := range newSession.Cookies {
		if newSession.Cookies[i].Name == "BDUSS" {
			newSession.Cookies[i].Value = "refreshed-bduss-test"
		}
	}
	if _, err := c.SaveQRSession(newSession); err != nil {
		t.Fatal(err)
	}
	fresh := NewConfig(c.configFilePath)
	defer fresh.Close()
	if err := fresh.Init(); err != nil {
		t.Fatal(err)
	}
	if fresh.ActiveUserBaiduPCS().GetBDUSS() != "refreshed-bduss-test" {
		t.Fatal("old active jar overwrote the new QR credentials")
	}
}

func TestReloadPicksUpSameAccountCredentialsChangedByAnotherProcess(t *testing.T) {
	c := newTestConfig(t)
	if _, err := c.SaveQRSession(fakeQRSession(42)); err != nil {
		t.Fatal(err)
	}
	other := NewConfig(c.configFilePath)
	defer other.Close()
	if err := other.Init(); err != nil {
		t.Fatal(err)
	}
	session := fakeQRSession(42)
	for i := range session.Cookies {
		if session.Cookies[i].Name == "BDUSS" {
			session.Cookies[i].Value = "other-process-test"
		}
	}
	if _, err := other.SaveQRSession(session); err != nil {
		t.Fatal(err)
	}
	if err := c.Reload(); err != nil {
		t.Fatal(err)
	}
	if c.ActiveUserBaiduPCS().GetBDUSS() != "other-process-test" {
		t.Fatal("reload reused stale credentials for the same UID")
	}
}

func TestCookieRefreshAndClearingArePersistedWithoutLegacyFallback(t *testing.T) {
	c := newTestConfig(t)
	if _, err := c.SaveQRSession(fakeQRSession(42)); err != nil {
		t.Fatal(err)
	}
	pan, _ := url.Parse("https://pan.baidu.com/")
	jar := c.ActiveUserBaiduPCS().GetClient().Jar.(*requester.CookieJar)
	jar.SetCookies(pan, []*http.Cookie{{Name: "STOKEN", Value: "updated-pan-test", Domain: ".pan.baidu.com", Path: "/", Secure: true}})
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	fresh := NewConfig(c.configFilePath)
	defer fresh.Close()
	if err := fresh.Init(); err != nil {
		t.Fatal(err)
	}
	if scopedValue(fresh.ActiveUserBaiduPCS().GetClient().Jar, "pan.baidu.com", "STOKEN") != "updated-pan-test" {
		t.Fatal("updated session cookie was not persisted")
	}
	for _, entry := range jar.Entries() {
		u, _ := url.Parse("https://" + entry.Domain + entry.Path)
		domain := entry.Domain
		if entry.HostOnly {
			domain = ""
		}
		jar.SetCookies(u, []*http.Cookie{{Name: entry.Name, Domain: domain, Path: entry.Path, MaxAge: -1}})
	}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	cleared := NewConfig(c.configFilePath)
	defer cleared.Close()
	if err := cleared.Init(); err != nil {
		t.Fatal(err)
	}
	account := cleared.ActiveUser()
	if account.CookieEntries == nil || len(account.CookieEntries) != 0 || account.BDUSS != "" || account.COOKIES != "" || cleared.ActiveUserBaiduPCS().GetBDUSS() != "" {
		t.Fatal("cleared session revived compatibility credentials")
	}
}

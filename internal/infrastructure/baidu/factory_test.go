package baidu

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/account"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

func trustedServer(t *testing.T, handler http.Handler) (*httptest.Server, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "pan.baidu.com"}, DNSNames: []string{"pan.baidu.com", "pcs.baidu.com"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(parsed)
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	t.Cleanup(server.Close)
	return server, pool
}
func connectedFactory(t *testing.T, server *httptest.Server, pool *x509.CertPool) *ClientFactory {
	t.Helper()
	factory := NewClientFactory()
	factory.newTransport = func(cfg configuration.Client) *http.Transport {
		transport := buildTransport(cfg)
		transport.TLSClientConfig.RootCAs = pool
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
		}
		return transport
	}
	t.Cleanup(func() { factory.Close() })
	return factory
}
func TestFactoryIsolationAgainstLegacyGlobalsAndMutableInputs(t *testing.T) {
	seen := make(chan string, 32)
	server, pool := trustedServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, _ := r.Cookie("BDUSS")
		value := ""
		if cookie != nil {
			value = cookie.Value
		}
		seen <- value + "|" + r.UserAgent()
		io.WriteString(w, `{"quota":1024,"used":10}`)
	}))
	factory := connectedFactory(t, server, pool)
	oldProxy, oldRules, oldUA := requester.ProxyAddr, requester.ProxyHostnameRules, requester.UserAgent
	requester.ProxyAddr = "http://127.0.0.1:1"
	requester.ProxyHostnameRules = "evil.invalid"
	requester.UserAgent = "legacy-global-UA"
	t.Cleanup(func() {
		requester.ProxyAddr = oldProxy
		requester.ProxyHostnameRules = oldRules
		requester.UserAgent = oldUA
	})
	cfg := DefaultConfiguration()
	cfg.UserAgent = "snapshot-UA"
	cfg.PCSUA = "snapshot-PCS-UA"
	original := configuration.NewSnapshot(1, cfg)
	credentials := account.Credentials{AccountID: "account_a", Revision: 3, Cookies: map[string]string{"BDUSS": "account-a-secret", "STOKEN": "stoken-a"}}
	a, err := factory.New(context.Background(), original, credentials)
	if err != nil {
		t.Fatal(err)
	}
	credentials.Cookies["BDUSS"] = "changed-after-bind"
	credentialsB := account.Credentials{AccountID: "account_b", Revision: 4, Cookies: map[string]string{"BDUSS": "account-b-secret"}}
	b, err := factory.New(context.Background(), original, credentialsB)
	if err != nil {
		t.Fatal(err)
	}
	if a.PCS == b.PCS || a.HTTP == b.HTTP || a.transport == b.transport || a.HTTP.Jar == b.HTTP.Jar {
		t.Fatal("clients share mutable state")
	}
	if a.ConfigRevision != 1 || a.CredentialRevision != 3 || a.AccountID != "account_a" || a.PCS.GetBDUSS() != "account-a-secret" || b.PCS.GetBDUSS() != "account-b-secret" {
		t.Fatal("wrong account/config binding")
	}
	if a.transport.Proxy != nil || a.transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("inherited legacy proxy/TLS behavior")
	}
	var group sync.WaitGroup
	for _, session := range []*Session{a, b} {
		session := session
		group.Add(1)
		go func() {
			defer group.Done()
			for i := 0; i < 4; i++ {
				quota, _, err := session.PCS.QuotaInfo()
				if err != nil || quota != 1024 {
					t.Errorf("isolated request failed: %v", err)
				}
			}
		}()
	}
	group.Wait()
	for i := 0; i < 8; i++ {
		entry := <-seen
		if entry != "account-a-secret|snapshot-PCS-UA" && entry != "account-b-secret|snapshot-PCS-UA" {
			t.Fatalf("wrong cookie or UA: %s", entry)
		}
	}
	cfg.EnableHTTPS = false
	cfg.UserAgent = "new-UA"
	cfg.PCSUA = "new-PCS-UA"
	next, err := factory.New(context.Background(), configuration.NewSnapshot(2, cfg), account.Credentials{AccountID: "account_a", Revision: 5, Cookies: map[string]string{"BDUSS": "new-a-secret"}})
	if err != nil {
		t.Fatal(err)
	}
	if next.PCS.URL().Scheme != "http" || a.PCS.URL().Scheme != "https" || a.HTTP.UserAgent != "snapshot-UA" || a.PCS.GetBDUSS() != "account-a-secret" {
		t.Fatal("new binding changed existing client")
	}
	a.PCS.SetStoken("a-only-update")
	origin, _ := url.Parse("https://pan.baidu.com/")
	for _, cookie := range b.HTTP.Jar.Cookies(origin) {
		if cookie.Name == "STOKEN" {
			t.Fatal("cookie update leaked to other account")
		}
	}
	if _, err := a.HTTP.Get("https://evil.invalid/"); err == nil {
		t.Fatal("arbitrary host accepted")
	}
	if err = factory.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = factory.New(context.Background(), original, credentialsB); err == nil {
		t.Fatal("closed factory admitted a client")
	}
}
func TestFactoryBindsCancellationAndRejectsUnsafeRedirect(t *testing.T) {
	entered := make(chan struct{})
	exited := make(chan struct{})
	server, pool := trustedServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "https://evil.invalid/private", http.StatusFound)
			return
		}
		close(entered)
		<-r.Context().Done()
		close(exited)
	}))
	factory := connectedFactory(t, server, pool)
	ctx, cancel := context.WithCancel(context.Background())
	session, err := factory.New(ctx, configuration.NewSnapshot(1, DefaultConfiguration()), account.Credentials{AccountID: "account_a", Revision: 1, Cookies: map[string]string{"BDUSS": "a"}})
	if err != nil {
		t.Fatal(err)
	}
	if response, err := session.HTTP.Get("https://pan.baidu.com/redirect"); err == nil {
		response.Body.Close()
		t.Fatal("unsafe redirect accepted")
	} else if response != nil {
		response.Body.Close()
	}
	done := make(chan error, 1)
	go func() {
		response, err := session.HTTP.Get("https://pan.baidu.com/wait")
		if response != nil {
			response.Body.Close()
		}
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream not reached")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("request did not carry binding context: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("request did not cancel")
	}
	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream not canceled")
	}
}
func TestProxyRulesAreExactAndTransportLocal(t *testing.T) {
	cfg := DefaultConfiguration()
	cfg.Proxy = "http://user:secret@127.0.0.1:3128"
	cfg.ProxyHostnames = []string{"pan.baidu.com", "*.baidupcs.com"}
	transport := buildTransport(cfg)
	cases := map[string]bool{"pan.baidu.com": true, "x.pan.baidu.com": false, "notpan.baidu.com": false, "baidupcs.com": false, "x.baidupcs.com": true, "evilbaidupcs.com": false}
	for host, want := range cases {
		req, _ := http.NewRequest("GET", "https://"+host+"/", nil)
		proxy, err := transport.Proxy(req)
		if err != nil || (proxy != nil) != want {
			t.Fatalf("proxy rule %s got %v err %v", host, proxy, err)
		}
	}
	cfg.ProxyHostnames[0] = "evil.invalid"
	request, _ := http.NewRequest("GET", "https://pan.baidu.com/", nil)
	proxy, _ := transport.Proxy(request)
	if proxy == nil || !strings.Contains(proxy.Host, "127.0.0.1") {
		t.Fatal("proxy rules alias caller configuration")
	}
}

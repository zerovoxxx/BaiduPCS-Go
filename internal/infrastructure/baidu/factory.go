package baidu

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/account"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
	"golang.org/x/net/publicsuffix"
)

func DefaultConfiguration() configuration.Client {
	return configuration.Client{AppID: 266719, CacheSize: 65536, MaxParallel: 1, MaxUploadParallel: 4, MaxDownloadLoad: 1, MaxUploadLoad: 4, UserAgent: requester.DefaultUserAgent, PCSAddr: baidupcs.PCSBaiduCom, PanUA: baidupcs.NetdiskUA, EnableHTTPS: true, NoCheck: true, IgnoreIllegal: true, UploadPolicy: "skip", ProxyHostnames: []string{}, LocalAddrs: []string{}}
}

// ClientFactory is infrastructure shared by the future Baidu/transfer adapters.
// It creates a fresh PCS, signature/cache state, Jar, resolver and Transport for
// each explicit credential/configuration binding; it never reuses CopyPCS.
type ClientFactory struct {
	mu           sync.Mutex
	closed       bool
	sessions     map[*Session]struct{}
	newTransport func(configuration.Client) *http.Transport
}

func NewClientFactory() *ClientFactory {
	return &ClientFactory{sessions: map[*Session]struct{}{}, newTransport: buildTransport}
}

type Session struct {
	PCS                *baidupcs.BaiduPCS
	HTTP               *requester.HTTPClient
	AccountID          string
	ConfigRevision     uint64
	CredentialRevision uint64
	cancel             context.CancelFunc
	transport          *http.Transport
	owner              *ClientFactory
	once               sync.Once
}

func (s *Session) Close() error {
	s.once.Do(func() {
		s.cancel()
		s.transport.CloseIdleConnections()
		s.owner.mu.Lock()
		delete(s.owner.sessions, s)
		s.owner.mu.Unlock()
	})
	return nil
}
func (f *ClientFactory) Close() error {
	f.mu.Lock()
	f.closed = true
	sessions := make([]*Session, 0, len(f.sessions))
	for session := range f.sessions {
		sessions = append(sessions, session)
	}
	f.mu.Unlock()
	for _, session := range sessions {
		session.Close()
	}
	return nil
}
func (f *ClientFactory) New(ctx context.Context, snapshot configuration.Snapshot, credentials account.Credentials) (*Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cfg := snapshot.Client()
	credentials = credentials.Clone()
	if !configuration.ValidID(credentials.AccountID) || credentials.Revision == 0 || snapshot.Revision() == 0 {
		return nil, fault.New(fault.Invalid, "INVALID_ACCOUNT_BINDING", "账号和配置必须绑定明确版本")
	}
	if issues := configuration.Validate(cfg); len(issues) > 0 {
		return nil, &fault.Error{Kind: fault.Unsupported, Code: "INVALID_CONFIGURATION", Message: "客户端配置无效", Issues: issues}
	}
	if len(credentials.Cookies) == 0 && credentials.AccessToken == "" {
		return nil, fault.New(fault.Unsupported, "BAIDU_CREDENTIAL_MISSING", "账号没有可用凭据")
	}
	jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	if err != nil {
		return nil, err
	}
	origin := &url.URL{Scheme: "https", Host: baidupcs.PanBaiduCom, Path: "/"}
	cookies := make([]*http.Cookie, 0, len(credentials.Cookies))
	for name, value := range credentials.Cookies {
		cookie := &http.Cookie{Name: name, Value: value, Domain: ".baidu.com", Path: "/"}
		if err := cookie.Valid(); err != nil {
			return nil, fault.New(fault.Invalid, "INVALID_CREDENTIAL_FORMAT", "Cookie格式无效")
		}
		cookies = append(cookies, cookie)
	}
	jar.SetCookies(origin, cookies)
	lifetime, cancel := context.WithCancel(ctx)
	transport := f.newTransport(cfg)
	client := requester.NewHTTPClientWithTransport(transport, jar, cfg.UserAgent)
	client.Timeout = 30 * time.Second
	client.Transport = &boundTransport{transport: transport, lifetime: lifetime}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !allowedURL(req.URL) {
			return errors.New("download/upstream redirect forbidden")
		}
		return nil
	}
	pcs := baidupcs.NewPCSWithClient(cfg.AppID, client)
	pcs.SetHTTPS(cfg.EnableHTTPS)
	pcs.SetPCSUserAgent(cfg.PCSUA)
	pcs.SetPanUserAgent(cfg.PanUA)
	pcs.SetPCSAddr(cfg.PCSAddr)
	pcs.SetStaticPCSAddr(cfg.FixPCSAddr)
	pcs.SetUID(credentials.UID)
	pcs.SetaccessToken(credentials.AccessToken)
	// Eagerly create the instance's PanHome object before any concurrent request.
	pcs.GetClient()
	session := &Session{PCS: pcs, HTTP: client, AccountID: credentials.AccountID, ConfigRevision: snapshot.Revision(), CredentialRevision: credentials.Revision, cancel: cancel, transport: transport, owner: f}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		cancel()
		transport.CloseIdleConnections()
		return nil, fault.New(fault.Unavailable, "SERVICE_DRAINING", "客户端工厂正在关闭")
	}
	f.sessions[session] = struct{}{}
	return session, nil
}
func allowedURL(u *url.URL) bool {
	if u == nil || u.User != nil || u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "baidu.com" || strings.HasSuffix(host, ".baidu.com") || host == "baidupcs.com" || strings.HasSuffix(host, ".baidupcs.com")
}
func buildTransport(cfg configuration.Client) *http.Transport {
	var next atomic.Uint64
	resolver := &net.Resolver{PreferGo: true}
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		d := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second, Resolver: resolver}
		if len(cfg.LocalAddrs) > 0 {
			index := (next.Add(1) - 1) % uint64(len(cfg.LocalAddrs))
			d.LocalAddr = &net.TCPAddr{IP: net.ParseIP(cfg.LocalAddrs[index])}
		}
		return d.DialContext(ctx, network, address)
	}
	transport := &http.Transport{DialContext: dial, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 20 * time.Second, ResponseHeaderTimeout: 25 * time.Second, ExpectContinueTimeout: time.Second, MaxIdleConns: 100, MaxIdleConnsPerHost: 60, IdleConnTimeout: 90 * time.Second, ForceAttemptHTTP2: true}
	if cfg.Proxy != "" {
		proxy, _ := url.Parse(cfg.Proxy)
		rules := append([]string(nil), cfg.ProxyHostnames...)
		transport.Proxy = func(req *http.Request) (*url.URL, error) {
			if len(rules) == 0 {
				return proxy, nil
			}
			host := strings.ToLower(req.URL.Hostname())
			for _, rule := range rules {
				rule = strings.ToLower(rule)
				if strings.HasPrefix(rule, "*.") {
					suffix := rule[1:]
					if strings.HasSuffix(host, suffix) && host != rule[2:] {
						return proxy, nil
					}
				} else if host == rule {
					return proxy, nil
				}
			}
			return nil, nil
		}
	}
	return transport
}

type boundTransport struct {
	transport *http.Transport
	lifetime  context.Context
}

func (b *boundTransport) CloseIdleConnections() { b.transport.CloseIdleConnections() }

func (b *boundTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !allowedURL(req.URL) {
		return nil, errors.New("upstream host forbidden")
	}
	if err := b.lifetime.Err(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(req.Context())
	stop := context.AfterFunc(b.lifetime, cancel)
	response, err := b.transport.RoundTrip(req.Clone(ctx))
	if err != nil {
		stop()
		cancel()
		return nil, err
	}
	response.Body = &boundBody{ReadCloser: response.Body, release: func() { stop(); cancel() }}
	return response, nil
}

type boundBody struct {
	io.ReadCloser
	release func()
	once    sync.Once
}

func (b *boundBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		b.once.Do(b.release)
	}
	return n, err
}
func (b *boundBody) Close() error { err := b.ReadCloser.Close(); b.once.Do(b.release); return err }

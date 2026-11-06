package requester

import (
	"crypto/tls"
	"net/http"
	"net/http/cookiejar"
	"time"
)

// HTTPClient http client
type HTTPClient struct {
	http.Client
	transport *http.Transport
	https     bool
	UserAgent string
}

// NewHTTPClient 返回 HTTPClient 的指针,
// 预设了一些配置
func NewHTTPClient() *HTTPClient {
	h := &HTTPClient{
		Client: http.Client{
			Timeout: 50 * time.Second,
		},
		https:     true,
		UserAgent: UserAgent,
	}
	h.Client.Jar, _ = cookiejar.New(nil)
	return h
}

// NewHTTPClientWithTransport constructs a fully initialized client without reading
// requester globals. Ownership of transport and jar passes to this instance.
// The legacy NewHTTPClient path keeps its existing proxy/TLS behavior.
func NewHTTPClientWithTransport(transport *http.Transport, jar http.CookieJar, userAgent string) *HTTPClient {
	if transport == nil {
		transport = &http.Transport{}
	}
	if jar == nil {
		jar, _ = cookiejar.New(nil)
	}
	return &HTTPClient{
		Client:    http.Client{Transport: transport, Jar: jar, Timeout: 50 * time.Second},
		transport: transport,
		UserAgent: userAgent,
	}
}

func (h *HTTPClient) lazyInit() {
	if h.transport == nil {
		h.transport = &http.Transport{
			Proxy:       proxyFunc,
			DialContext: dialContext,
			// DialTLS:     h.dialTLSFunc(),
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: !h.https,
			},
			TLSHandshakeTimeout:   20 * time.Second,
			DisableKeepAlives:     false,
			DisableCompression:    false, // gzip
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   60,
			ForceAttemptHTTP2:     true,
			IdleConnTimeout:       90 * time.Second,
			ResponseHeaderTimeout: 25 * time.Second,
			ExpectContinueTimeout: 10 * time.Second,
		}
		h.Client.Transport = h.transport
	}
}

// SetUserAgent 设置 UserAgent 浏览器标识
func (h *HTTPClient) SetUserAgent(ua string) {
	h.UserAgent = ua
}

// SetProxy 设置代理
func (h *HTTPClient) SetProxy(proxyAddr string) {
	h.lazyInit()
	u, err := checkProxyAddr(proxyAddr)
	if err != nil {
		h.transport.Proxy = http.ProxyFromEnvironment
		return
	}

	h.transport.Proxy = http.ProxyURL(u)
}

// SetCookiejar 设置 cookie
func (h *HTTPClient) SetCookiejar(jar http.CookieJar) {
	h.Client.Jar = jar
}

// ResetCookiejar 清空 cookie
func (h *HTTPClient) ResetCookiejar() {
	h.Jar, _ = cookiejar.New(nil)
}

// SetHTTPSecure 设置是否校验 HTTPS 证书。新客户端默认校验。
func (h *HTTPClient) SetHTTPSecure(b bool) {
	h.https = b
	h.lazyInit()
	h.transport.TLSClientConfig = &tls.Config{
		InsecureSkipVerify: !b,
	}
}

// NewSecureHTTPClient initializes a certificate-verifying client with the
// project's proxy, DNS and local-address settings, for context-aware requests.
func NewSecureHTTPClient() *HTTPClient {
	h := NewHTTPClient()
	h.SetHTTPSecure(true)
	return h
}

// SetKeepAlive 设置 Keep-Alive
func (h *HTTPClient) SetKeepAlive(b bool) {
	h.lazyInit()
	h.transport.DisableKeepAlives = !b
}

// SetGzip 是否启用Gzip
func (h *HTTPClient) SetGzip(b bool) {
	h.lazyInit()
	h.transport.DisableCompression = !b
}

// SetResponseHeaderTimeout 设置目标服务器响应超时时间
func (h *HTTPClient) SetResponseHeaderTimeout(t time.Duration) {
	h.lazyInit()
	h.transport.ResponseHeaderTimeout = t
}

// SetTLSHandshakeTimeout 设置tls握手超时时间
func (h *HTTPClient) SetTLSHandshakeTimeout(t time.Duration) {
	h.lazyInit()
	h.transport.TLSHandshakeTimeout = t
}

// SetTimeout 设置 http 请求超时时间, 默认30s
func (h *HTTPClient) SetTimeout(t time.Duration) {
	h.Client.Timeout = t
}

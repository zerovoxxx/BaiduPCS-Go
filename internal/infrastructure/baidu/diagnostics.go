package baidu

import (
	"context"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// The CLI's ipify source is fixed; no URL, proxy, cookie, or host is accepted
// from the diagnostics request, and redirects never broaden this authority.
func (f *ClientFactory) ProbeOutgoing(ctx context.Context, c configuration.Snapshot) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	t := f.newTransport(c.Client())
	defer t.CloseIdleConnections()
	client := &http.Client{Transport: t, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r, e := http.NewRequestWithContext(ctx, "GET", "https://api.ipify.org", nil)
	if e != nil {
		return "", e
	}
	resp, e := client.Do(r)
	if e != nil {
		return "", upstreamFault(e)
	}
	defer resp.Body.Close()
	data, e := io.ReadAll(io.LimitReader(resp.Body, 128))
	if e != nil {
		return "", upstreamFault(e)
	}
	ip := net.ParseIP(strings.TrimSpace(string(data)))
	if resp.StatusCode != 200 || ip == nil {
		return "", fault.New(fault.BadGateway, "UPSTREAM_PROTOCOL_ERROR", "出口探测响应无效")
	}
	return ip.String(), nil
}

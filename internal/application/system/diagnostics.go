package system

import (
	"context"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/auth"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/system"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/ports"
	"net"
	"time"
)

type Diagnostics struct {
	cfg     ports.ConfigurationRepository
	network ports.NetworkEnvironment
	probe   ports.NetworkProbe
}

func NewDiagnostics(c ports.ConfigurationRepository, n ports.NetworkEnvironment, p ports.NetworkProbe) *Diagnostics {
	return &Diagnostics{c, n, p}
}
func (s *Diagnostics) Network(ctx context.Context, p auth.Principal, active bool) (system.NetworkDiagnostics, error) {
	out := system.NetworkDiagnostics{LocalAddresses: []string{}, Active: active, Checked: time.Now().UTC().Format(time.RFC3339Nano)}
	if !p.Has("admin") {
		return out, fault.New(fault.Forbidden, "FORBIDDEN", "网络诊断需要管理员权限")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	addresses, e := s.network.LocalAddresses(ctx)
	if e != nil {
		return out, fault.New(fault.Unavailable, "NETWORK_DIAGNOSTICS_UNAVAILABLE", "无法读取本机网络状态")
	}
	for _, a := range addresses {
		ip := net.ParseIP(a)
		if ip != nil && ip.IsGlobalUnicast() && !ip.IsPrivate() && len(out.LocalAddresses) < 100 {
			out.LocalAddresses = append(out.LocalAddresses, ip.String())
		}
	}
	if active {
		cfg, e := s.cfg.Read(ctx)
		if e != nil {
			return out, e
		}
		value, e := s.probe.ProbeOutgoing(ctx, cfg)
		if e != nil {
			return out, e
		}
		out.Outbound = &value
	}
	return out, nil
}

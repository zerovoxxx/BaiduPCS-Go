package local

import (
	"context"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/artifact"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/auth"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/ports"
)

type Service struct{ roots ports.LocalRoots }

func New(r ports.LocalRoots) *Service { return &Service{r} }
func (s *Service) Roots(ctx context.Context, p auth.Principal) ([]artifact.Root, error) {
	if !p.Has("local.read") {
		return nil, fault.New(fault.Forbidden, "FORBIDDEN", "无权读取本地目录")
	}
	all, err := s.roots.LocalRoots(ctx)
	if err != nil {
		return nil, err
	}
	out := []artifact.Root{}
	for _, r := range all {
		if p.AllowsRoot(r.ID) {
			r.Readable = r.Readable && p.Has("local.read")
			r.Writable = r.Writable && p.Has("local.write")
			out = append(out, r)
		}
	}
	return out, nil
}

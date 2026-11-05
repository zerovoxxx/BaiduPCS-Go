package staging

import (
	"context"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/auth"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/staging"
	"io"
)

func (s *Service) Reception(ctx context.Context, p auth.Principal, id string) (staging.Reception, error) {
	v, e := s.receptions.Reception(ctx, id)
	if v.Owner != "" && (v.Owner != p.ID && !p.Has("admin") || !p.Has("local.write")) {
		return staging.Reception{}, fault.New(fault.Forbidden, "FORBIDDEN", "无权访问接收会话")
	}
	return v, e
}
func (s *Service) CreateReception(ctx context.Context, p auth.Principal, name string, size int64, expected *staging.Expected, key string) (staging.Reception, error) {
	if !p.Has("local.write") {
		return staging.Reception{}, fault.New(fault.Forbidden, "FORBIDDEN", "缺少本地写权限")
	}
	return s.receptions.CreateReception(ctx, p.ID, name, size, expected, key)
}
func (s *Service) PatchReception(ctx context.Context, p auth.Principal, id string, offset int64, reader io.Reader) (staging.Reception, error) {
	if _, e := s.Reception(ctx, p, id); e != nil {
		return staging.Reception{}, e
	}
	return s.receptions.PatchReception(ctx, id, offset, reader)
}
func (s *Service) CompleteReception(ctx context.Context, p auth.Principal, id string) (staging.File, error) {
	if _, e := s.Reception(ctx, p, id); e != nil {
		return staging.File{}, e
	}
	return s.receptions.CompleteReception(ctx, id)
}
func (s *Service) DeleteReception(ctx context.Context, p auth.Principal, id string) error {
	if _, e := s.Reception(ctx, p, id); e != nil {
		if fault.As(e).Kind == fault.Gone {
			return nil
		}
		return e
	}
	return s.receptions.DeleteReception(ctx, id)
}

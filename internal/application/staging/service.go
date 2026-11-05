package staging

import (
	"context"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/auth"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/operation"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/staging"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/ports"
	"io"
)

type Service struct{ files ports.StagingFiles }

func New(files ports.StagingFiles) *Service { return &Service{files} }
func (s *Service) Get(ctx context.Context, p auth.Principal, id string) (staging.File, error) {
	f, err := s.files.Metadata(ctx, id)
	if err != nil {
		return f, err
	}
	if !p.Has("local.write") || f.Owner != p.ID && !p.Has("admin") {
		return staging.File{}, fault.New(fault.Forbidden, "FORBIDDEN", "无权访问暂存文件")
	}
	return f, nil
}
func (s *Service) Receive(ctx context.Context, p auth.Principal, name, key string, reader io.Reader, after func() (string, error)) (staging.File, error) {
	if !p.Has("local.write") {
		return staging.File{}, fault.New(fault.Forbidden, "FORBIDDEN", "无权接收暂存文件")
	}
	return s.files.Receive(ctx, p.ID, name, key, reader, after)
}
func (s *Service) Delete(ctx context.Context, p auth.Principal, id, key string) error {
	f, err := s.files.Metadata(ctx, id)
	if err != nil && fault.As(err).Kind != fault.Gone {
		return err
	}
	if f.Owner != p.ID && !p.Has("admin") || !p.Has("local.write") {
		return fault.New(fault.Forbidden, "FORBIDDEN", "无权访问暂存文件")
	}
	return s.files.Delete(ctx, id, operation.New(p.ID, "UPL04/"+id, key, id))
}

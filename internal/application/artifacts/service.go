package artifacts

import (
	"context"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/artifact"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/auth"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/operation"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/transfer"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/ports"
	"time"
)

type Jobs interface {
	Get(context.Context, auth.Principal, string) (transfer.Record, error)
}
type Service struct {
	files ports.ArtifactFiles
	jobs  Jobs
}
type Content = ports.ArtifactContent

func New(f ports.ArtifactFiles, j Jobs) *Service { return &Service{f, j} }
func (s *Service) authorize(ctx context.Context, p auth.Principal, id string) (artifact.Metadata, error) {
	a, err := s.files.Metadata(ctx, id)
	if err != nil {
		return a, err
	}
	if _, err = s.jobs.Get(ctx, p, a.JobID); err != nil {
		return artifact.Metadata{}, err
	}
	return a, nil
}
func (s *Service) Get(ctx context.Context, p auth.Principal, id string) (artifact.Metadata, error) {
	a, err := s.authorize(ctx, p, id)
	if err != nil {
		return a, err
	}
	expiry, e := time.Parse(time.RFC3339Nano, a.ExpiresAt)
	if e != nil {
		return a, e
	}
	if a.State == "deleted" || !time.Now().Before(expiry) {
		return a, fault.New(fault.Gone, "RESOURCE_EXPIRED", "结果文件已过期或删除")
	}
	if a.State != "ready" {
		return a, fault.New(fault.Conflict, "ARTIFACT_NOT_READY", "结果文件尚未完成")
	}
	return a, nil
}
func (s *Service) Open(ctx context.Context, p auth.Principal, id string) (artifact.Metadata, Content, error) {
	if _, err := s.Get(ctx, p, id); err != nil {
		return artifact.Metadata{}, nil, err
	}
	return s.files.Open(ctx, id)
}
func (s *Service) Delete(ctx context.Context, p auth.Principal, id, key string) error {
	if _, err := s.authorize(ctx, p, id); err != nil {
		return err
	}
	return s.files.Delete(ctx, id, operation.New(p.ID, "ART04/"+id, key, id))
}
func (s *Service) List(ctx context.Context, p auth.Principal, id string) ([]artifact.Metadata, error) {
	if _, err := s.jobs.Get(ctx, p, id); err != nil {
		return nil, err
	}
	all, err := s.files.List(ctx, id)
	if err != nil {
		return nil, err
	}
	out := []artifact.Metadata{}
	for _, a := range all {
		expiry, err := time.Parse(time.RFC3339Nano, a.ExpiresAt)
		if err != nil {
			return nil, err
		}
		if time.Now().Before(expiry) {
			out = append(out, a)
		}
	}
	return out, nil
}

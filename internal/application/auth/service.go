package auth

import (
	"context"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/auth"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/ports"
)

type Service struct{ provider ports.Authenticator }

func New(provider ports.Authenticator) *Service { return &Service{provider: provider} }
func (s *Service) Authenticate(ctx context.Context, token string) (auth.Principal, error) {
	return s.provider.Authenticate(ctx, token)
}
func Authorize(p auth.Principal, scopes []string, accountID string) error {
	for _, scope := range scopes {
		if !p.Has(scope) {
			return fault.New(fault.Forbidden, "FORBIDDEN", "服务身份没有此操作权限")
		}
	}
	if accountID != "" && !p.AllowsAccount(accountID) {
		return fault.New(fault.Forbidden, "FORBIDDEN", "服务身份无权访问此账号")
	}
	return nil
}

func (s *Service) Principal(ctx context.Context, id string) (auth.Principal, error) {
	if directory, ok := s.provider.(interface {
		Principal(context.Context, string) (auth.Principal, error)
	}); ok {
		return directory.Principal(ctx, id)
	}
	return auth.Principal{}, fault.New(fault.Unsupported, "TICKET_NOT_AVAILABLE", "身份提供者不支持票据权限复核")
}

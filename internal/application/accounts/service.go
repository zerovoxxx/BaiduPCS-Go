package accounts

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"
	"unicode/utf8"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/account"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/auth"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/operation"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/ports"
)

type Service struct {
	repo      ports.AccountRepository
	cfg       ports.ConfigurationRepository
	validator ports.AccountValidator
}

func New(repo ports.AccountRepository) *Service { return &Service{repo: repo} }
func NewValidated(repo ports.AccountRepository, cfg ports.ConfigurationRepository, validator ports.AccountValidator) *Service {
	return &Service{repo: repo, cfg: cfg, validator: validator}
}
func (s *Service) Import(ctx context.Context, p auth.Principal, label string, input account.Import) (account.Entry, error) {
	return s.ImportOnce(ctx, p, label, input, "")
}
func (s *Service) ImportOnce(ctx context.Context, p auth.Principal, label string, input account.Import, key string) (account.Entry, error) {
	if !p.Has("admin") || !p.AllowsAccount("*") {
		return account.Entry{}, fault.New(fault.Forbidden, "FORBIDDEN", "导入账号需要管理员和账号创建权限")
	}
	if !utf8.ValidString(label) || utf8.RuneCountInString(label) > 256 {
		return account.Entry{}, fault.New(fault.Invalid, "INVALID_REQUEST", "账号标签无效")
	}
	c, err := account.Normalize(input)
	if err != nil {
		return account.Entry{}, err
	}
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		return account.Entry{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	a := account.Entry{ID: "acct_" + hex.EncodeToString(random[:]), Label: label, CreatedAt: now, UpdatedAt: now, Revision: 1, LoginState: "unverified", Credentials: c, Present: map[string]bool{}}
	for name, value := range c.Cookies {
		a.Present[name] = value != ""
	}
	a.Present["cookies"] = len(c.Cookies) > 0
	a.Present["access_token"] = c.AccessToken != ""
	a.Credentials.AccountID = a.ID
	return s.repo.CreateAccountOnce(ctx, a, operation.New(p.ID, "ACC01", key, struct {
		Label       string
		Credentials account.Import
	}{label, input}))
}

func (s *Service) List(ctx context.Context, p auth.Principal) ([]account.Entry, error) {
	if !p.Has("read") {
		return nil, fault.New(fault.Forbidden, "FORBIDDEN", "无权读取账号")
	}
	all, err := s.repo.Accounts(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]account.Entry, 0)
	for _, a := range all {
		if p.AllowsAccount(a.ID) {
			result = append(result, a)
		}
	}
	if len(result) > 1000 {
		return nil, fault.New(fault.Unsupported, "LIMIT_EXCEEDED", "账号列表超过1000项")
	}
	return result, nil
}
func (s *Service) Get(ctx context.Context, p auth.Principal, id string) (account.Entry, error) {
	if !p.Has("read") || !p.AllowsAccount(id) {
		return account.Entry{}, fault.New(fault.Forbidden, "FORBIDDEN", "无权读取账号")
	}
	return s.repo.Account(ctx, id)
}

type Validation struct {
	Account   account.Entry
	Valid     bool
	CheckedAt string
	Problem   *fault.Error
}

func (s *Service) Validate(ctx context.Context, p auth.Principal, id string) (Validation, error) {
	var result Validation
	a, err := s.Get(ctx, p, id)
	if err != nil {
		return result, err
	}
	if s.cfg == nil || s.validator == nil {
		return result, fault.New(fault.Unavailable, "VALIDATION_UNAVAILABLE", "账号验证不可用")
	}
	snapshot, err := s.cfg.Read(ctx)
	if err != nil {
		return result, err
	}
	identity, validationErr := s.validator.ValidateAccount(ctx, snapshot, a.Credentials)
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	expected := a.Revision
	a.Revision++
	a.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if validationErr == nil {
		a.Identity = identity
		a.LoginState = "valid"
	} else {
		result.Problem = fault.As(validationErr)
		a.LoginState = "unavailable"
		if result.Problem.Code == "BAIDU_CREDENTIAL_EXPIRED" {
			a.LoginState = "expired"
		}
		a.Identity.Verified = false
	}
	if err = s.repo.UpdateAccount(ctx, a, expected); err != nil {
		return result, err
	}
	result.Account, result.Valid, result.CheckedAt = a, validationErr == nil, a.UpdatedAt
	return result, nil
}

package accounts

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/account"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/auth"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/operation"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/system"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/ports"
)

type Service struct {
	revoke    func(context.Context, string) error
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
	return s.ImportNamed(ctx, p, label, input, key, "")
}
func (s *Service) ImportNamed(ctx context.Context, p auth.Principal, label string, input account.Import, key, username string) (account.Entry, error) {
	if !p.Has("admin") || !p.AllowsAccount("*") {
		return account.Entry{}, fault.New(fault.Forbidden, "FORBIDDEN", "导入账号需要管理员和账号创建权限")
	}
	if !utf8.ValidString(label) || utf8.RuneCountInString(label) > 256 || !utf8.ValidString(username) || utf8.RuneCountInString(username) > 256 {
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
	a.CredentialVersion = c.Revision
	if username != "" {
		a.Identity.Username = &username
	}
	return s.repo.CreateAccountOnce(ctx, a, operation.New(p.ID, "ACC01", key, struct {
		Label       string
		Credentials account.Import
		Username    string
	}{label, input, username}))
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

func (s *Service) OnRevoke(f func(context.Context, string) error) { s.revoke = f }
func admin(p auth.Principal, id string) error {
	if !p.Has("admin") || !p.AllowsAccount(id) {
		return fault.New(fault.Forbidden, "FORBIDDEN", "无权管理此账号")
	}
	return nil
}
func (s *Service) Patch(ctx context.Context, p auth.Principal, id string, expected uint64, label *string, patch account.Patch, key string) (account.Entry, error) {
	if err := admin(p, id); err != nil {
		return account.Entry{}, err
	}
	if label != nil && (!utf8.ValidString(*label) || utf8.RuneCountInString(*label) > 256) {
		return account.Entry{}, fault.New(fault.Invalid, "INVALID_REQUEST", "账号标签无效")
	}
	op := "ACC04/" + id
	if patch != nil {
		op = "ACC06/" + id
	}
	m := operation.New(p.ID, op, key, struct {
		Revision uint64
		Label    *string
		Patch    account.Patch
	}{expected, label, patch})
	a, revoked, err := s.repo.MutateAccount(ctx, id, expected, label, patch, m)
	if err == nil && revoked && s.revoke != nil {
		err = s.revoke(ctx, id)
	}
	return a, err
}
func (s *Service) Delete(ctx context.Context, p auth.Principal, id, key string) error {
	if err := admin(p, id); err != nil {
		return err
	}
	if err := s.repo.DeleteAccount(ctx, id, operation.New(p.ID, "ACC05/"+id, key, id)); err != nil {
		return err
	}
	if s.revoke != nil {
		return s.revoke(ctx, id)
	}
	return nil
}
func (s *Service) Quota(ctx context.Context, p auth.Principal, id string) (account.Quota, error) {
	a, err := s.Get(ctx, p, id)
	if err != nil {
		return account.Quota{}, err
	}
	q, ok := s.validator.(ports.QuotaReader)
	if !ok || s.cfg == nil {
		return account.Quota{}, fault.New(fault.Unavailable, "QUOTA_UNAVAILABLE", "容量查询不可用")
	}
	cfg, err := s.cfg.Read(ctx)
	if err != nil {
		return account.Quota{}, err
	}
	return q.AccountQuota(ctx, cfg, a.Credentials)
}

// ImportCanonical is used only by the explicitly configured legacy importer.
// The fingerprint key includes the source index and canonical credentials;
// duplicate legacy UIDs never merge accounts or overwrite existing secrets.
func (s *Service) ImportCanonical(ctx context.Context, p auth.Principal, label, username, key string, c account.Credentials) (account.Entry, error) {
	if !p.Has("admin") || !p.AllowsAccount("*") {
		return account.Entry{}, fault.New(fault.Forbidden, "FORBIDDEN", "无权导入账号")
	}
	if utf8.RuneCountInString(username) > 256 {
		return account.Entry{}, fault.New(fault.Invalid, "INVALID_REQUEST", "旧账号用户名过长")
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return account.Entry{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	id := "acct_" + hex.EncodeToString(random[:])
	c.AccountID = id
	c.Revision = 1
	a := account.Entry{ID: id, Label: label, CreatedAt: now, UpdatedAt: now, Revision: 1, CredentialVersion: 1, Credentials: c, LoginState: "unverified", Present: map[string]bool{}}
	for k, v := range c.Cookies {
		a.Present[k] = v != ""
	}
	a.Present["cookies"] = len(c.Cookies) > 0
	a.Present["access_token"] = c.AccessToken != ""
	if username != "" {
		a.Identity.Username = &username
	}
	return s.repo.CreateAccountOnce(ctx, a, operation.New(p.ID, "ACC01/legacy", key, key))
}

// Capabilities applies credential alternatives and the fixed client snapshot to
// the caller's allowed account without exposing either snapshot's secrets.
func (s *Service) Capabilities(ctx context.Context, p auth.Principal, id string, caps []system.Capability) ([]system.Capability, error) {
	a, err := s.Get(ctx, p, id)
	if err != nil {
		return nil, err
	}
	if s.cfg == nil {
		return nil, fault.New(fault.Unavailable, "CAPABILITIES_UNAVAILABLE", "账号能力配置不可用")
	}
	cfg, err := s.cfg.Read(ctx)
	if err != nil {
		return nil, err
	}
	present := func(k string) bool {
		if k == "access_token" {
			return a.Credentials.AccessToken != ""
		}
		if k == "cookies" {
			return len(a.Credentials.Cookies) > 0
		}
		return a.Credentials.Cookies[strings.ToUpper(k)] != ""
	}
	for i := range caps {
		c := &caps[i]
		missing := false
		for _, k := range c.RequiredCredentials {
			missing = missing || !present(k)
		}
		if len(c.CredentialAlternatives) > 0 {
			available := false
			for _, alternative := range c.CredentialAlternatives {
				all := true
				for _, k := range alternative {
					all = all && present(k)
				}
				available = available || all
			}
			missing = missing || !available
		}
		remote := len(c.RequiredCredentials) > 0 || len(c.CredentialAlternatives) > 0
		reason := "此账号缺少所需凭据"
		if remote && !cfg.Client().EnableHTTPS {
			missing = true
			reason = "当前配置未启用携带百度凭据所必需的HTTPS"
		}
		if remote && a.LoginState == "expired" && c.ID != "ACC07" {
			missing = true
			reason = "此账号凭据已过期，请更新并验证"
		}
		if missing && c.Implementation != "planned" {
			c.Enabled = false
			c.Availability = "disabled"
			c.Reason = &reason
		}
	}
	return caps, nil
}

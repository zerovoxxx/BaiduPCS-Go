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
	"github.com/qjfoidnh/BaiduPCS-Go/internal/ports"
)

type Service struct{ repo ports.AccountRepository }

func New(repo ports.AccountRepository) *Service { return &Service{repo: repo} }
func (s *Service) Import(ctx context.Context, p auth.Principal, label string, input account.Import) (account.Entry, error) {
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
	a := account.Entry{ID: "acct_" + hex.EncodeToString(random[:]), Label: label, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Credentials: c}
	a.Credentials.AccountID = a.ID
	return a, s.repo.CreateAccount(ctx, a)
}

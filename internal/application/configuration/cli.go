package configuration

import (
	"context"
	"fmt"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/application/files"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/auth"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/file"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/operation"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/ports"
	"sync"
)

type CLI struct {
	store  ports.CLIDefaultsStore
	replay ports.CLIDefaultsReplay
	files  *files.Service
	mu     sync.Mutex
}

func NewCLI(store ports.CLIDefaultsStore, replay ports.CLIDefaultsReplay, files *files.Service) *CLI {
	return &CLI{store: store, replay: replay, files: files}
}
func (s *CLI) Read(ctx context.Context, p auth.Principal) (ports.CLIDefaults, error) {
	if e := admin(p); e != nil {
		return ports.CLIDefaults{}, e
	}
	v, e := s.store.ReadDefaults(ctx)
	if e != nil {
		return v, e
	}
	for _, a := range v.Accounts {
		if !p.AllowsAccount(a.ID) {
			return v, fault.New(fault.Forbidden, "FORBIDDEN", "CLI映射包含未授权账号")
		}
	}
	return v, nil
}
func (s *CLI) Update(ctx context.Context, p auth.Principal, rev uint64, key string, account *string, hasAccount bool, accounts []ports.CLIAccount, hasAccounts bool) (ports.CLIDefaults, error) {
	if e := admin(p); e != nil {
		return ports.CLIDefaults{}, e
	}
	m := operation.New(p.ID, "CFG09", key, []any{rev, account, hasAccount, accounts, hasAccounts})
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, e := s.replay.ReplayCLIDefaults(ctx, m); e != nil {
		return ports.CLIDefaults{}, e
	} else if v != nil {
		return *v, nil
	}
	if hasAccount && account != nil {
		if !p.AllowsAccount(*account) {
			return ports.CLIDefaults{}, fault.New(fault.Forbidden, "FORBIDDEN", "默认账号未授权")
		}
	}
	seen := map[string]bool{}
	for _, a := range accounts {
		if seen[a.ID] {
			return ports.CLIDefaults{}, fault.New(fault.Invalid, "INVALID_REQUEST", "账号不能重复")
		}
		seen[a.ID] = true
		remote, e := files.Resolve(a.Cwd, file.Scope{})
		if e != nil {
			return ports.CLIDefaults{}, e
		}
		v, e := s.files.Meta(ctx, p, a.ID, remote)
		if e != nil {
			return ports.CLIDefaults{}, e
		}
		if !v.IsDir {
			return ports.CLIDefaults{}, fault.New(fault.Invalid, "DIRECTORY_REQUIRED", "CLI默认cwd必须为目录")
		}
	}
	v, e := s.store.WriteDefaults(ctx, rev, account, hasAccount, accounts, hasAccounts)
	if e != nil {
		return v, e
	}
	if e = s.replay.SaveCLIDefaults(ctx, v, m); e != nil {
		return v, fault.New(fault.Conflict, "CLI_DEFAULTS_RESULT_UNKNOWN", "CLI配置已写入但回执未持久化，请重新读取核对")
	}
	return v, nil
}

type CLIAccount = ports.CLIAccount

func PublicCLI(v ports.CLIDefaults) any {
	items := []any{}
	for _, a := range v.Accounts {
		items = append(items, map[string]string{"account_id": a.ID, "cwd": a.Cwd})
	}
	return map[string]any{"revision": fmt.Sprint(v.Revision), "default_account_id": v.Default, "accounts": items}
}

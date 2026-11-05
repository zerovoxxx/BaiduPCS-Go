package writes

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/application/contexts"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/application/files"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/application/query"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/account"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/auth"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/file"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/operation"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/write"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/ports"
	"strconv"
)

type Service struct {
	contexts *contexts.Service
	accounts ports.AccountRepository
	cfg      ports.ConfigurationRepository
	repo     ports.BusinessRepository
	shares   ports.ShareGateway
	offline  ports.OfflineGateway
	recycle  ports.RecycleGateway
	queries  *query.Service
	files    *files.Service
}

func New(a ports.AccountRepository, c ports.ConfigurationRepository, r ports.BusinessRepository, s ports.ShareGateway, o ports.OfflineGateway, b ports.RecycleGateway, q *query.Service) *Service {
	return &Service{accounts: a, cfg: c, repo: r, shares: s, offline: o, recycle: b, queries: q}
}
func (s *Service) binding(ctx context.Context, p auth.Principal, id, scope string) (configuration.Snapshot, account.Credentials, error) {
	if !p.Has(scope) || !p.AllowsAccount(id) {
		return configuration.Snapshot{}, account.Credentials{}, fault.New(fault.Forbidden, "FORBIDDEN", "无权访问此账号业务")
	}
	a, err := s.accounts.Account(ctx, id)
	if err != nil {
		return configuration.Snapshot{}, account.Credentials{}, err
	}
	cfg, err := s.cfg.Read(ctx)
	return cfg, a.Credentials, err
}

var validPassword = regexp.MustCompile(`^[A-Za-z0-9]{4}$`)

func (s *Service) CreateShare(ctx context.Context, p auth.Principal, id string, paths []string, password *string, period int, combined bool, key string) (write.Share, error) {
	cfg, c, err := s.binding(ctx, p, id, "write")
	if err != nil {
		return write.Share{}, err
	}
	if period < 0 || int64(period) > 2147483647 || password != nil && !validPassword.MatchString(*password) {
		return write.Share{}, fault.New(fault.Invalid, "INVALID_REQUEST", "分享有效期或提取码无效")
	}
	m := operation.New(p.ID, "SHR01/"+id, key, []any{paths, password, period, combined})
	raw, replayed, err := s.repo.BeginBusinessMutation(ctx, m)
	if err != nil {
		return write.Share{}, err
	}
	type result struct {
		Share   write.Share
		Problem *fault.Error
	}
	var saved result
	if replayed {
		if json.Unmarshal(raw, &saved) != nil {
			return write.Share{}, fault.New(fault.Internal, "STORAGE_INVALID", "分享回执无效")
		}
		if saved.Problem != nil {
			return saved.Share, saved.Problem
		}
		return saved.Share, nil
	}
	g, err := s.shares.OpenShares(ctx, cfg, c)
	if err != nil {
		data, _ := json.Marshal(result{Problem: fault.As(err)})
		flush, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if e := s.repo.FinishBusinessMutation(flush, m, data); e != nil {
			return write.Share{}, e
		}
		return write.Share{}, err
	}
	defer g.Close()
	pwd := ""
	if password != nil {
		pwd = *password
	}
	v, err := g.Create(paths, pwd, period, combined)
	if err != nil && fault.As(err).Code == "REMOTE_RESULT_UNKNOWN" {
		return v, err
	}
	saved.Share = v
	if err != nil {
		saved.Problem = fault.As(err)
	}
	data, _ := json.Marshal(saved)
	flush, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	if saveErr := s.repo.FinishBusinessMutation(flush, m, data); saveErr != nil {
		return v, fault.New(fault.Conflict, "REMOTE_RESULT_UNKNOWN", "分享结果未持久化，请先核验")
	}
	return v, err
}
func (s *Service) Shares(ctx context.Context, p auth.Principal, id string, page int, passwords bool) ([]write.Share, error) {
	cfg, c, err := s.binding(ctx, p, id, "read")
	if err != nil {
		return nil, err
	}
	g, err := s.shares.OpenShares(ctx, cfg, c)
	if err != nil {
		return nil, err
	}
	defer g.Close()
	return g.List(page, passwords)
}
func (s *Service) Share(ctx context.Context, p auth.Principal, id, share string) (write.Share, error) {
	cfg, c, err := s.binding(ctx, p, id, "read")
	if err != nil {
		return write.Share{}, err
	}
	g, err := s.shares.OpenShares(ctx, cfg, c)
	if err != nil {
		return write.Share{}, err
	}
	defer g.Close()
	return g.Detail(share)
}
func (s *Service) inspection(ctx context.Context, p auth.Principal, id, inspection string) (write.Inspection, configuration.Snapshot, account.Credentials, error) {
	cfg, c, err := s.binding(ctx, p, id, "read")
	if err != nil {
		return write.Inspection{}, cfg, c, err
	}
	v, err := s.repo.Inspection(ctx, inspection)
	if err != nil {
		return v, cfg, c, err
	}
	if v.Access.AccountID != id || v.Access.Owner != p.ID && !p.Has("admin") {
		return v, cfg, c, fault.New(fault.Forbidden, "FORBIDDEN", "分享检查归属不符")
	}
	if v.Access.CredentialRevision != c.Revision {
		return v, cfg, c, fault.New(fault.Gone, "SHARE_INSPECTION_EXPIRED", "分享检查凭据版本已变化")
	}
	return v, cfg, c, nil
}
func (s *Service) Inspect(ctx context.Context, p auth.Principal, id, input string, password *string, inspection, dir, cursor string, size int) (write.Inspection, query.Page, error) {
	var v write.Inspection
	var cfg configuration.Snapshot
	var c account.Credentials
	var err error
	if inspection == "" {
		if input == "" || dir != "" || cursor != "" {
			return v, query.Page{}, fault.New(fault.Invalid, "INVALID_REQUEST", "首次检查需要input")
		}
		cfg, c, err = s.binding(ctx, p, id, "read")
		if err != nil {
			return v, query.Page{}, err
		}
		g, err := s.shares.OpenShares(ctx, cfg, c)
		if err != nil {
			return v, query.Page{}, err
		}
		pwd := ""
		if password != nil {
			pwd = *password
		}
		v.Access, err = g.Inspect(input, pwd)
		g.Close()
		if err != nil {
			return v, query.Page{}, err
		}
		var random [16]byte
		if _, err = rand.Read(random[:]); err != nil {
			return v, query.Page{}, err
		}
		v.ID = "inspection_" + hex.EncodeToString(random[:])
		v.Access.Owner = p.ID
		v.Access.AccountID = id
		v.Access.CredentialRevision = c.Revision
		v.Access.ExpiresAt = time.Now().Add(600 * time.Second).UTC().Format(time.RFC3339Nano)
		if err = s.repo.SaveInspection(ctx, v); err != nil {
			return v, query.Page{}, err
		}
	} else {
		if input != "" || password != nil {
			return v, query.Page{}, fault.New(fault.Invalid, "INVALID_REQUEST", "后续检查不能混合input/password")
		}
		v, cfg, c, err = s.inspection(ctx, p, id, inspection)
		if err != nil {
			return v, query.Page{}, err
		}
	}
	binding := []any{"share-inspection", p.ID, id, v.ID, dir, size}
	page, err := s.queries.Page(ctx, binding, cursor, size, func() ([]json.RawMessage, error) {
		entries := []file.Entry{}
		if dir == "" {
			rootIDs := map[string]bool{}
			for _, id := range v.Access.RootIDs {
				rootIDs[id] = true
			}
			for _, e := range v.Access.Entries {
				if rootIDs[e.ID] {
					entries = append(entries, e)
				}
			}
		} else {
			directory := ""
			for _, e := range v.Access.Entries {
				if e.ID == dir && e.IsDir {
					directory = e.Path
				}
			}
			if directory == "" {
				return nil, fault.New(fault.Forbidden, "SHARE_SELECTION_FORBIDDEN", "目录ID不属于此分享检查")
			}
			g, err := s.shares.OpenShares(ctx, cfg, c)
			if err != nil {
				return nil, err
			}
			entries, err = g.SharedDirectory(v.Access, directory)
			g.Close()
			if err != nil {
				return nil, err
			}
			seen := map[string]bool{}
			for _, e := range v.Access.Entries {
				seen[e.ID] = true
			}
			for _, e := range entries {
				if !seen[e.ID] {
					v.Access.Entries = append(v.Access.Entries, e)
					seen[e.ID] = true
				}
			}
			if len(v.Access.Entries) > 10000 {
				return nil, fault.New(fault.TooLarge, "INSPECTION_LIMIT", "分享检查最多10000项")
			}
			if err = s.repo.UpdateInspection(ctx, v); err != nil {
				return nil, err
			}
		}
		return query.Encode(entries)
	})
	return v, page, err
}
func (s *Service) Access(ctx context.Context, p auth.Principal, id, inspection, input string, password *string) (write.Access, error) {
	if !p.Has("write") || !p.Has("transfer") || !p.AllowsAccount(id) {
		return write.Access{}, fault.New(fault.Forbidden, "FORBIDDEN", "缺少转存权限")
	}
	p = p.Clone()
	p.Scopes = append(p.Scopes, "read")
	if inspection != "" {
		v, _, _, err := s.inspection(ctx, p, id, inspection)
		return v.Access, err
	}
	v, _, err := s.Inspect(ctx, p, id, input, password, "", "", "", 1000)
	return v.Access, err
}
func (s *Service) OfflineList(ctx context.Context, p auth.Principal, id, cursor string, size int) (query.Page, error) {
	cfg, c, err := s.binding(ctx, p, id, "read")
	if err != nil {
		return query.Page{}, err
	}
	return s.queries.Page(ctx, []any{"offline-window", p.ID, id, c.Revision, size}, cursor, size, func() ([]json.RawMessage, error) {
		g, err := s.offline.OpenOffline(ctx, cfg, c)
		if err != nil {
			return nil, err
		}
		defer g.Close()
		v, err := g.List()
		if err != nil {
			return nil, err
		}
		return query.Encode(v)
	})
}
func (s *Service) OfflineQuery(ctx context.Context, p auth.Principal, id string, ids []string) ([]write.Offline, error) {
	if len(ids) > 100 {
		return nil, fault.New(fault.TooLarge, "LIMIT_EXCEEDED", "离线任务查询最多100个ID")
	}
	cfg, c, err := s.binding(ctx, p, id, "read")
	if err != nil {
		return nil, err
	}
	g, err := s.offline.OpenOffline(ctx, cfg, c)
	if err != nil {
		return nil, err
	}
	defer g.Close()
	return g.Query(ids)
}
func (s *Service) Recycle(ctx context.Context, p auth.Principal, id string, page int) ([]write.Recycled, error) {
	cfg, c, err := s.binding(ctx, p, id, "read")
	if err != nil {
		return nil, err
	}
	g, err := s.recycle.OpenRecycle(ctx, cfg, c)
	if err != nil {
		return nil, err
	}
	defer g.Close()
	return g.List(page)
}

func (s *Service) Contexts(c *contexts.Service) { s.contexts = c }
func (s *Service) Selectors(f *files.Service)   { s.files = f }
func (s *Service) Selection(ctx context.Context, p auth.Principal, id, kind string, values []string, scope file.Scope) ([]write.Unit, error) {
	if !p.Has("write") || !p.AllowsAccount(id) {
		return nil, fault.New(fault.Forbidden, "FORBIDDEN", "缺少写权限")
	}
	// Selection is part of an authorized write. It does not grant a public read
	// endpoint or return credential-bearing metadata to the caller.
	reader := p.Clone()
	reader.Scopes = append(reader.Scopes, "read")
	if scope.ContextID != "" {
		resolved, e := s.contexts.ResolveScope(ctx, p, id, scope)
		if e != nil {
			return nil, e
		}
		scope = resolved
	}
	out := []write.Unit{}
	for _, v := range values {
		switch kind {
		case "paths", "patterns":
			remote, err := files.Resolve(v, scope)
			if err != nil {
				return nil, err
			}
			if kind == "patterns" {
				paths, err := s.files.Match(ctx, reader, id, remote)
				if err != nil {
					return nil, err
				}
				for _, p := range paths {
					out = append(out, write.Unit{From: p})
				}
			} else {
				out = append(out, write.Unit{From: remote})
			}
		case "fs_ids":
			number, err := strconv.ParseInt(v, 10, 64)
			if err != nil || number <= 0 || strconv.FormatInt(number, 10) != v {
				return nil, fault.New(fault.Invalid, "INVALID_REQUEST", "文件ID无效")
			}
			out = append(out, write.Unit{ID: v})
		default:
			return nil, fault.New(fault.Invalid, "INVALID_REQUEST", "选择器类型无效")
		}
	}
	if len(out) > 10000 {
		return nil, fault.New(fault.TooLarge, "LIMIT_EXCEEDED", "写入选择最多10000项")
	}
	unique := []write.Unit{}
	seen := map[string]bool{}
	for _, v := range out {
		key := v.ID + "\x00" + v.From
		if !seen[key] {
			unique = append(unique, v)
			seen[key] = true
		}
	}
	return unique, nil
}

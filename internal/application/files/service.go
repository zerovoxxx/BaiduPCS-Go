package files

import (
	"context"
	"encoding/json"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/application/query"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/auth"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/file"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/ports"
	"path"
	"strings"
	"unicode/utf8"
)

type Service struct {
	accounts ports.AccountRepository
	cfg      ports.ConfigurationRepository
	reader   ports.FileReader
	queries  *query.Service
}

func New(a ports.AccountRepository, c ports.ConfigurationRepository, r ports.FileReader, q *query.Service) *Service {
	return &Service{a, c, r, q}
}
func Resolve(p string, scope file.Scope) (string, error) {
	if scope.BasePath != "" && scope.ContextID != "" {
		return "", fault.New(fault.Invalid, "AMBIGUOUS_PATH_SCOPE", "base_path与context_id不能同时提供")
	}
	if scope.ContextID != "" {
		return "", fault.New(fault.Invalid, "UNRESOLVED_CONTEXT", "路径解析前必须固定上下文")
	}
	if scope.BasePath != "" && (!path.IsAbs(scope.BasePath) || path.Clean(scope.BasePath) != scope.BasePath || !validPath(scope.BasePath)) {
		return "", fault.New(fault.Invalid, "INVALID_REQUEST", "base_path必须是规范绝对路径")
	}
	if p == "" {
		p = "/"
	}
	if !validPath(p) {
		return "", fault.New(fault.Invalid, "INVALID_REQUEST", "网盘路径无效")
	}
	if !path.IsAbs(p) {
		if scope.BasePath == "" {
			return "", fault.New(fault.Invalid, "RELATIVE_PATH_REQUIRES_SCOPE", "相对路径需要base_path")
		}
		p = path.Join(scope.BasePath, p)
	}
	return path.Clean(p), nil
}
func validPath(p string) bool {
	return utf8.ValidString(p) && len(p) <= 4096 && !strings.ContainsAny(p, "\x00\r\n")
}
func (s *Service) Open(ctx context.Context, p auth.Principal, id string) (ports.FileSession, error) {
	if !p.Has("read") || !p.AllowsAccount(id) {
		return nil, fault.New(fault.Forbidden, "FORBIDDEN", "无权读取此账号")
	}
	a, err := s.accounts.Account(ctx, id)
	if err != nil {
		return nil, err
	}
	cfg, err := s.cfg.Read(ctx)
	if err != nil {
		return nil, err
	}
	return s.reader.OpenFiles(ctx, cfg, a.Credentials)
}
func (s *Service) List(ctx context.Context, p auth.Principal, id, remote string, o file.ListOptions) ([]file.Entry, error) {
	r, err := s.Open(ctx, p, id)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return r.List(remote, o)
}
func (s *Service) Meta(ctx context.Context, p auth.Principal, id, remote string) (file.Entry, error) {
	r, err := s.Open(ctx, p, id)
	if err != nil {
		return file.Entry{}, err
	}
	defer r.Close()
	return r.Meta(remote)
}
func (s *Service) Search(ctx context.Context, p auth.Principal, id, remote, keyword string, recursive bool, cursor string, size int, fields string) (query.Page, error) {
	// Recheck current account access even when reading an existing snapshot.
	a, err := s.accounts.Account(ctx, id)
	if err != nil {
		return query.Page{}, err
	}
	if !p.Has("read") || !p.AllowsAccount(id) {
		return query.Page{}, fault.New(fault.Forbidden, "FORBIDDEN", "无权读取此账号")
	}
	binding := []any{"search", p.ID, id, a.Credentials.Revision, remote, keyword, recursive, fields, size}
	return s.queries.Page(ctx, binding, cursor, size, func() ([]json.RawMessage, error) {
		r, err := s.Open(ctx, p, id)
		if err != nil {
			return nil, err
		}
		defer r.Close()
		all, err := r.Search(remote, keyword, recursive)
		if err != nil {
			return nil, err
		}
		if len(all) > 10000 {
			return nil, fault.New(fault.TooLarge, "QUERY_SNAPSHOT_LIMIT", "搜索结果超过10000项，请使用扫描任务")
		}
		return query.Encode(all)
	})
}

// Match traverses only pattern components, using the same path.Match grammar as
// the CLI. Every list is paged, bounded and checked for lack of progress.
func (s *Service) Match(ctx context.Context, p auth.Principal, id, pattern string) ([]string, error) {
	if _, err := path.Match(pattern, ""); err != nil {
		return nil, fault.New(fault.Invalid, "INVALID_PATTERN", "通配符格式无效")
	}
	r, err := s.Open(ctx, p, id)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	components := strings.Split(strings.TrimPrefix(pattern, "/"), "/")
	current := []string{"/"}
	budget := 0
	for _, component := range components {
		next := []string{}
		for _, parent := range current {
			if !strings.ContainsAny(component, "*?[\\") {
				next = append(next, path.Join(parent, component))
				continue
			}
			seen := map[string]bool{}
			for page := 1; ; page++ {
				list, err := r.List(parent, file.ListOptions{Page: page, PageSize: 1000, OrderBy: "name", Direction: "asc"})
				if err != nil {
					return nil, err
				}
				for _, e := range list {
					budget++
					if budget > 10000 {
						return nil, fault.New(fault.TooLarge, "QUERY_SNAPSHOT_LIMIT", "通配符遍历超过10000项")
					}
					if seen[e.ID] {
						return nil, fault.New(fault.BadGateway, "UPSTREAM_PAGINATION_STALLED", "百度分页重复条目")
					}
					seen[e.ID] = true
					if ok, _ := path.Match(component, e.Name); ok {
						next = append(next, e.Path)
					}
				}
				if len(list) < 1000 {
					break
				}
			}
		}
		current = next
	}
	return current, nil
}

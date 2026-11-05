package contexts

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/application/files"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/auth"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/file"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/operation"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/session"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/ports"
	"time"
)

type Value struct {
	AccountID string        `json:"account_id"`
	Cwd       string        `json:"cwd"`
	Listing   *[]file.Entry `json:",omitempty"`
}
type Service struct {
	repo  ports.ResourceRepository
	files *files.Service
}

func New(r ports.ResourceRepository, f *files.Service) *Service { return &Service{r, f} }
func ID(prefix string) (string, error) {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", e
	}
	return prefix + hex.EncodeToString(b[:]), nil
}
func (s *Service) Get(ctx context.Context, p auth.Principal, id string) (session.Resource, Value, error) {
	r, e := s.repo.Resource(ctx, "context", id)
	var v Value
	if e != nil {
		return r, v, e
	}
	if e = json.Unmarshal(r.Data, &v); e != nil {
		return r, v, e
	}
	if r.Owner != p.ID || !p.Has("read") || !p.AllowsAccount(v.AccountID) {
		return r, v, fault.New(fault.Forbidden, "FORBIDDEN", "上下文不属于调用方或账号未授权")
	}
	if r.Expires <= time.Now().Unix() {
		return r, v, fault.New(fault.Gone, "RESOURCE_EXPIRED", "上下文已过期或删除")
	}
	return r, v, nil
}
func (s *Service) directory(ctx context.Context, p auth.Principal, id, cwd string) error {
	e, err := s.files.Meta(ctx, p, id, cwd)
	if err != nil {
		return err
	}
	if !e.IsDir {
		return fault.New(fault.Invalid, "DIRECTORY_REQUIRED", "上下文cwd必须为目录")
	}
	return nil
}
func (s *Service) Create(ctx context.Context, p auth.Principal, v Value, key string) (session.Resource, error) {
	m := operation.New(p.ID, "CTX01", key, v)
	if r, e := s.repo.ReplayResource(ctx, m); e != nil {
		return session.Resource{}, e
	} else if r != nil {
		if !p.AllowsAccount(v.AccountID) {
			return *r, fault.New(fault.Forbidden, "FORBIDDEN", "账号未授权")
		}
		return *r, nil
	}
	if e := s.directory(ctx, p, v.AccountID, v.Cwd); e != nil {
		return session.Resource{}, e
	}
	id, e := ID("ctx_")
	if e != nil {
		return session.Resource{}, e
	}
	b, _ := json.Marshal(v)
	return s.repo.PutResource(ctx, session.Resource{Kind: "context", ID: id, Owner: p.ID, Expires: time.Now().Add(24 * time.Hour).Unix(), Data: b}, 0, m)
}
func (s *Service) Update(ctx context.Context, p auth.Principal, id string, rev uint64, accountID, cwd *string, includeListing bool, key string) (session.Resource, Value, error) {
	m := operation.New(p.ID, "CTX03/"+id, key, []any{rev, accountID, cwd, includeListing})
	r, v, e := s.Get(ctx, p, id)
	if e != nil {
		return r, v, e
	}
	if replay, e := s.repo.ReplayResource(ctx, m); e != nil {
		return r, v, e
	} else if replay != nil {
		_ = json.Unmarshal(replay.Data, &v)
		return *replay, v, nil
	}
	if r.Revision != rev {
		return r, v, fault.New(fault.Precondition, "REVISION_MISMATCH", "上下文版本已改变")
	}
	old := v.AccountID
	if accountID != nil {
		v.AccountID = *accountID
	}
	if old != v.AccountID {
		v.Cwd = "/"
	}
	if cwd != nil {
		scope := file.Scope{BasePath: v.Cwd}
		if old != v.AccountID {
			scope = file.Scope{}
		}
		v.Cwd, e = files.Resolve(*cwd, scope)
		if e != nil {
			return r, v, e
		}
	}
	if e = s.directory(ctx, p, v.AccountID, v.Cwd); e != nil {
		return r, v, e
	}
	v.Listing = nil
	if includeListing {
		entries, err := s.files.List(ctx, p, v.AccountID, v.Cwd, file.ListOptions{Page: 1, PageSize: 1000, OrderBy: "name", Direction: "asc"})
		if err != nil {
			return r, v, err
		}
		v.Listing = &entries
	}
	r.Data, _ = json.Marshal(v)
	r.Expires = time.Now().Add(24 * time.Hour).Unix()
	r, e = s.repo.PutResource(ctx, r, rev, m)
	return r, v, e
}
func (s *Service) Delete(ctx context.Context, p auth.Principal, id, key string) error {
	m := operation.New(p.ID, "CTX04/"+id, key, id)
	if r, e := s.repo.ReplayResource(ctx, m); e != nil || r != nil {
		return e
	}
	r, _, e := s.Get(ctx, p, id)
	if e != nil {
		if fault.As(e).Code == "RESOURCE_EXPIRED" && r.Owner == p.ID {
			return nil
		}
		return e
	}
	r.Expires = time.Now().Unix() - 1
	_, e = s.repo.PutResource(ctx, r, r.Revision, m)
	return e
}
func (s *Service) ResolveScope(ctx context.Context, p auth.Principal, accountID string, scope file.Scope) (file.Scope, error) {
	if scope.ContextID == "" {
		return scope, nil
	}
	if scope.BasePath != "" {
		return scope, fault.New(fault.Invalid, "AMBIGUOUS_PATH_SCOPE", "context与base_path不能同时提供")
	}
	_, v, e := s.Get(ctx, p, scope.ContextID)
	if e != nil {
		return scope, e
	}
	if v.AccountID != accountID {
		return scope, fault.New(fault.Conflict, "CONTEXT_ACCOUNT_MISMATCH", "上下文与路径账号不同")
	}
	return file.Scope{BasePath: v.Cwd}, nil
}

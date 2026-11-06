package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/application/contexts"
	appfiles "github.com/qjfoidnh/BaiduPCS-Go/internal/application/files"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/file"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/session"
	"io"
	"net/http"
	"time"
)

func (a *API) resolve(r *http.Request, p string, scope file.Scope) (string, error) {
	scope = a.frozenScope(r, scope)
	if scope.ContextID != "" {
		resolved, e := a.deps.Contexts.ResolveScope(r.Context(), principal(r.Context()), r.PathValue("account_id"), scope)
		if e != nil {
			return "", e
		}
		scope = resolved
	}
	return appfiles.Resolve(p, scope)
}
func contextValue(r session.Resource, v contexts.Value) map[string]any {
	return map[string]any{"context_id": r.ID, "account_id": v.AccountID, "cwd": v.Cwd, "revision": uintString(r.Revision), "expires_at": time.Unix(r.Expires, 0).UTC().Format(time.RFC3339Nano)}
}
func (a *API) serveContexts(id string, w http.ResponseWriter, r *http.Request) error {
	ctx, p := r.Context(), principal(r.Context())
	key, e := idempotencyKey(r)
	if e != nil {
		return e
	}
	if id == "CTX01" {
		var in struct {
			Account string  `json:"account_id"`
			Cwd     *string `json:"cwd,omitempty"`
		}
		if e := decodeRequest(w, r, &in); e != nil {
			return e
		}
		cwd := deref(in.Cwd, "/")
		if !configuration.ValidID(in.Account) {
			return invalid("账号无效")
		}
		resolved, e := appfiles.Resolve(cwd, file.Scope{})
		if e != nil {
			return e
		}
		v := contexts.Value{AccountID: in.Account, Cwd: resolved}
		out, e := a.deps.Contexts.Create(ctx, p, v, key)
		if e != nil {
			return e
		}
		w.Header().Set("Location", "/api/v1/contexts/"+out.ID)
		w.Header().Set("ETag", etag(out.Revision))
		a.respond(w, r, 201, contextValue(out, v))
		return nil
	}
	contextID := r.PathValue("context_id")
	if id == "CTX04" {
		if r.ContentLength > 0 {
			return invalid("删除不接受请求体")
		}
		if e := a.deps.Contexts.Delete(ctx, p, contextID, key); e != nil {
			return e
		}
		w.WriteHeader(204)
		return nil
	}
	var out session.Resource
	var v contexts.Value
	listing := false
	if id == "CTX02" {
		out, v, e = a.deps.Contexts.Get(ctx, p, contextID)
	} else {
		rev, e2 := ifMatch(r)
		if e2 != nil {
			return e2
		}
		var in struct {
			Account *string `json:"account_id,omitempty"`
			Cwd     *string `json:"cwd,omitempty"`
			Listing *bool   `json:"include_listing,omitempty"`
		}
		if e := decodeRequest(w, r, &in); e != nil {
			return e
		}
		if in.Account == nil && in.Cwd == nil && in.Listing == nil {
			return invalid("上下文更新不能为空")
		}
		if in.Account != nil && !configuration.ValidID(*in.Account) || in.Cwd != nil && *in.Cwd == "" {
			return invalid("上下文输入无效")
		}
		listing = deref(in.Listing, false)
		out, v, e = a.deps.Contexts.Update(ctx, p, contextID, rev, in.Account, in.Cwd, listing, key)
	}
	if e != nil {
		return e
	}
	data := contextValue(out, v)
	if listing && v.Listing != nil {
		entries := *v.Listing
		data["listing"] = filePage(entries, []string{"all"})
		data["listing_pagination"] = map[string]any{"page": 1, "page_size": 1000, "total": nil, "has_more": len(entries) == 1000, "consistency": "upstream_live"}
	}
	w.Header().Set("ETag", etag(out.Revision))
	a.success(w, r, data)
	return nil
}

// A request reads its context once. Every path and selector then uses the same
// account/cwd snapshot, even while another request updates that context.
type frozenPathScope struct {
	original, resolved file.Scope
	semantic           string
}
type frozenScopeKey struct{}

func scopedOperation(id string) bool {
	switch id {
	case "FIL01", "FIL02", "FIL03", "FIL04", "FIL05", "FIL06", "FIL07", "FIL08", "FIL09", "FIL10", "FIL11", "UPL01", "DWN01", "DWN02", "DWN03", "DWN04", "DWN05", "SHR01", "TRF02", "OFF01", "INT01", "INT02", "INT04", "INT06", "EXP01":
		return true
	}
	return false
}
func (a *API) freezePathScope(r *http.Request, id string) (*http.Request, error) {
	if !scopedOperation(id) {
		return r, nil
	}
	scope := file.Scope{}
	semantic := ""
	if r.Method == "GET" || r.Method == "HEAD" {
		scope = file.Scope{BasePath: r.URL.Query().Get("base_path"), ContextID: r.URL.Query().Get("context_id")}
	} else {
		body, e := io.ReadAll(r.Body)
		if e != nil {
			var limit *http.MaxBytesError
			if errors.As(e, &limit) {
				return r, &httpError{413, "LIMIT_EXCEEDED", "请求体超出限制"}
			}
			return r, invalid("无法读取请求")
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		var properties map[string]json.RawMessage
		if json.Unmarshal(body, &properties) != nil {
			return r, nil
		}
		for _, field := range []string{"base_path", "context_id"} {
			if raw, ok := properties[field]; ok {
				var value string
				if json.Unmarshal(raw, &value) != nil || value == "" {
					return r, invalid("路径作用域不能为空")
				}
				if field == "base_path" {
					scope.BasePath = value
				} else {
					scope.ContextID = value
				}
			}
		}
		if scope.ContextID != "" {
			canonical, _ := json.Marshal(properties)
			semantic = string(canonical)
		}
	}
	if scope.BasePath != "" && scope.ContextID != "" {
		return r, &httpError{400, "AMBIGUOUS_PATH_SCOPE", "base_path与context_id不能同时提供"}
	}
	if scope.ContextID == "" {
		return r, nil
	}
	if !configuration.ValidID(scope.ContextID) {
		return r, invalid("context_id无效")
	}
	resolved, e := a.deps.Contexts.ResolveScope(r.Context(), principal(r.Context()), r.PathValue("account_id"), scope)
	if e != nil {
		return r, e
	}
	return r.WithContext(context.WithValue(r.Context(), frozenScopeKey{}, frozenPathScope{scope, resolved, semantic})), nil
}
func (a *API) frozenScope(r *http.Request, scope file.Scope) file.Scope {
	if frozen, ok := r.Context().Value(frozenScopeKey{}).(frozenPathScope); ok && scope.ContextID == frozen.original.ContextID {
		return frozen.resolved
	}
	return scope
}
func contextSemantic(r *http.Request) string {
	v, _ := r.Context().Value(frozenScopeKey{}).(frozenPathScope)
	return v.semantic
}

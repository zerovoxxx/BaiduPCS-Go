package httpapi

import (
	"encoding/json"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/application/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"net/http"
)

func (a *API) serveManagement(id string, w http.ResponseWriter, r *http.Request) error {
	ctx, p := r.Context(), principal(r.Context())
	if id == "SYS06" {
		return a.serveP6Tools(id, w, r)
	}
	if id == "SYS05" {
		q, e := queryValues(r, "active_probe")
		if e != nil {
			return e
		}
		active := q.Get("active_probe") == "true"
		if q.Get("active_probe") != "" && q.Get("active_probe") != "true" && q.Get("active_probe") != "false" {
			return invalid("active_probe无效")
		}
		v, e := a.deps.Diagnostics.Network(ctx, p, active)
		if e != nil {
			return e
		}
		a.success(w, r, v)
		return nil
	}
	if id == "CFG06" {
		revision, e := ifMatch(r)
		if e != nil {
			return e
		}
		key, e := idempotencyKey(r)
		if e != nil {
			return e
		}
		var in struct {
			Source string `json:"source"`
			Dry    *bool  `json:"dry_run,omitempty"`
		}
		if e := decodeRequest(w, r, &in); e != nil {
			return e
		}
		v, e := a.deps.Configuration.Reload(ctx, p, revision, key, in.Source, deref(in.Dry, true))
		if e != nil {
			return e
		}
		w.Header().Set("ETag", etag(v.Snapshot.Revision()))
		a.success(w, r, publicChange(v))
		return nil
	}
	if id == "CFG08" {
		v, e := a.deps.CLI.Read(ctx, p)
		if e != nil {
			return e
		}
		w.Header().Set("ETag", etag(v.Revision))
		a.success(w, r, configuration.PublicCLI(v))
		return nil
	}
	revision, e := ifMatch(r)
	if e != nil {
		return e
	}
	key, e := idempotencyKey(r)
	if e != nil {
		return e
	}
	var in struct {
		Default  json.RawMessage `json:"default_account_id,omitempty"`
		Accounts json.RawMessage `json:"accounts,omitempty"`
	}
	if e := decodeObjectMode(w, r, &in, false, true); e != nil {
		return e
	}
	hasDefault := len(in.Default) > 0
	var account *string
	if hasDefault && string(in.Default) != "null" {
		var value string
		if json.Unmarshal(in.Default, &value) != nil || value == "" {
			return invalid("default_account_id无效")
		}
		account = &value
	}
	hasAccounts := len(in.Accounts) > 0
	if !hasDefault && !hasAccounts {
		return invalid("CLI默认值更新不能为空")
	}
	accounts := []configuration.CLIAccount{}
	if hasAccounts {
		var values []struct {
			ID  string `json:"account_id"`
			Cwd string `json:"cwd"`
		}
		if StrictJSON(in.Accounts, &values) != nil {
			return invalid("accounts无效")
		}
		if len(values) > 1000 {
			return fault.New(fault.TooLarge, "LIMIT_EXCEEDED", "账号过多")
		}
		for _, v := range values {
			accounts = append(accounts, configuration.CLIAccount{ID: v.ID, Cwd: v.Cwd})
		}
	}

	v, e := a.deps.CLI.Update(ctx, p, revision, key, account, hasDefault, accounts, hasAccounts)
	if e != nil {
		return e
	}
	w.Header().Set("ETag", etag(v.Revision))
	a.success(w, r, configuration.PublicCLI(v))
	return nil
}

package httpapi

import (
	app "github.com/qjfoidnh/BaiduPCS-Go/internal/application/login"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/login"
	"net/http"
	"time"
	"unicode/utf8"
)

func loginView(v app.View, request string) any {
	var challenge, qrcode any
	if v.Challenge != nil {
		var img any
		if v.Challenge.Image {
			img = "/api/v1/account-login-sessions/" + v.ID + "/qrcode"
		}
		challenge = map[string]any{"challenge_id": v.Challenge.ID, "type": v.Challenge.Type, "prompt": v.Challenge.Prompt, "image_url": img}
	}
	if v.Method == "qrcode" && v.Image {
		qrcode = "/api/v1/account-login-sessions/" + v.ID + "/qrcode"
	}
	return map[string]any{"session_id": v.ID, "method": v.Method, "state": v.State, "challenge": challenge, "qrcode_url": qrcode, "account_id": nullable(v.AccountID), "expires_at": time.Unix(v.Expires, 0).UTC().Format(time.RFC3339Nano), "problem": publicProblem(v.Problem, request)}
}
func (a *API) serveLogin(id string, w http.ResponseWriter, r *http.Request) error {
	ctx, p, sessionID := r.Context(), principal(r.Context()), r.PathValue("session_id")
	key, e := idempotencyKey(r)
	if e != nil {
		return e
	}
	var v app.View
	switch id {
	case "LOG01":
		var in struct {
			Method   string  `json:"method"`
			Username *string `json:"username,omitempty"`
			Password *string `json:"password,omitempty"`
		}
		if e := decodeRequest(w, r, &in); e != nil {
			return e
		}
		if in.Method != "password" && in.Method != "qrcode" || in.Method == "qrcode" && (in.Username != nil || in.Password != nil) || in.Method == "password" && (in.Username == nil || in.Password == nil || *in.Username == "" || *in.Password == "" || utf8.RuneCountInString(*in.Username) > 256 || utf8.RuneCountInString(*in.Password) > 8192) {
			return invalid("登录方法或输入无效")
		}
		v, e = a.deps.Login.Create(ctx, p, login.Input{Method: in.Method, Username: deref(in.Username, ""), Password: deref(in.Password, "")}, key)
		if e != nil {
			return e
		}
		w.Header().Set("Location", "/api/v1/account-login-sessions/"+v.ID)
		a.respond(w, r, 201, loginView(v, RequestID(ctx)))
		return nil
	case "LOG02":
		v, e = a.deps.Login.Get(ctx, p, sessionID)
	case "LOG03":
		img, e := a.deps.Login.Image(ctx, p, sessionID)
		if e != nil {
			return e
		}
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(200)
		_, e = w.Write(img)
		return e
	case "LOG04":
		var in struct {
			Challenge string `json:"challenge_id"`
			Response  string `json:"response"`
		}
		if e := decodeRequest(w, r, &in); e != nil {
			return e
		}
		if !configuration.ValidID(in.Challenge) || in.Response == "" || utf8.RuneCountInString(in.Response) > 8192 {
			return invalid("挑战响应无效")
		}
		v, e = a.deps.Login.Respond(ctx, p, sessionID, in.Challenge, in.Response, key)
	case "LOG05":
		var in struct {
			Account string  `json:"account_id,omitempty"`
			Label   *string `json:"label,omitempty"`
		}
		if e := decodeObject(w, r, &in, true); e != nil {
			return e
		}
		if in.Account != "" && !configuration.ValidID(in.Account) || in.Label != nil && utf8.RuneCountInString(*in.Label) > 256 {
			return invalid("完成目标或标签无效")
		}
		v, e := a.deps.Login.Complete(ctx, p, sessionID, in.Account, in.Label)
		if e != nil {
			return e
		}
		w.Header().Set("ETag", etag(v.Revision))
		w.Header().Set("Location", "/api/v1/accounts/"+v.ID)
		a.success(w, r, publicAccount(v))
		return nil
	case "LOG06":
		if r.ContentLength > 0 {
			return invalid("撤销不接受请求体")
		}
		if e := a.deps.Login.Delete(ctx, p, sessionID); e != nil {
			return e
		}
		w.WriteHeader(204)
		return nil
	}
	if e != nil {
		return e
	}
	a.success(w, r, loginView(v, RequestID(ctx)))
	return nil
}

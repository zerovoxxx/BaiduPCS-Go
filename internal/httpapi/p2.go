package httpapi

import (
	"net/http"
	"path"
	"strings"
	"unicode/utf8"

	apptransfers "github.com/qjfoidnh/BaiduPCS-Go/internal/application/transfers"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/account"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/transfer"
)

type credentialInput struct {
	Kind        string  `json:"kind"`
	BDUSS       *string `json:"bduss,omitempty"`
	PToken      *string `json:"ptoken,omitempty"`
	SToken      *string `json:"stoken,omitempty"`
	SBoxTKN     *string `json:"sboxtkn,omitempty"`
	BAIDUID     *string `json:"baiduid,omitempty"`
	Cookies     *string `json:"cookies,omitempty"`
	AccessToken *string `json:"access_token,omitempty"`
}

func deref[T any](p *T, fallback T) T {
	if p == nil {
		return fallback
	}
	return *p
}
func (c credentialInput) domain() (account.Import, error) {
	allowed := map[string]bool{}
	switch c.Kind {
	case "bduss":
		for _, k := range []string{"bduss", "ptoken", "stoken", "sboxtkn", "baiduid"} {
			allowed[k] = true
		}
	case "cookies":
		allowed["cookies"] = true
	case "access_token":
		allowed["access_token"] = true
	default:
		return account.Import{}, invalid("凭据kind无效")
	}
	for k, v := range map[string]*string{"bduss": c.BDUSS, "ptoken": c.PToken, "stoken": c.SToken, "sboxtkn": c.SBoxTKN, "baiduid": c.BAIDUID, "cookies": c.Cookies, "access_token": c.AccessToken} {
		if v != nil && *v == "" {
			return account.Import{}, fault.New(fault.Invalid, "INVALID_CREDENTIAL_FORMAT", "凭据字段不能为空")
		}
		if v != nil && !allowed[k] {
			return account.Import{}, invalid("凭据输入不能混合类型")
		}
	}
	return account.Import{Kind: c.Kind, BDUSS: deref(c.BDUSS, ""), PToken: deref(c.PToken, ""), SToken: deref(c.SToken, ""), SBoxTKN: deref(c.SBoxTKN, ""), BAIDUID: deref(c.BAIDUID, ""), Cookies: deref(c.Cookies, ""), AccessToken: deref(c.AccessToken, "")}, nil
}

type uploadSource struct {
	Type         string  `json:"type"`
	RootID       *string `json:"root_id,omitempty"`
	RelativePath *string `json:"relative_path,omitempty"`
	StagedFileID *string `json:"staged_file_id,omitempty"`
}
type uploadInput struct {
	Sources   []uploadSource             `json:"sources"`
	Target    *string                    `json:"target_dir"`
	Options   apptransfers.UploadOptions `json:"options,omitempty"`
	DryRun    bool                       `json:"dry_run,omitempty"`
	BasePath  *string                    `json:"base_path,omitempty"`
	ContextID *string                    `json:"context_id,omitempty"`
}
type downloadInput struct {
	Selector *struct {
		Kind   string   `json:"kind"`
		Values []string `json:"values"`
	} `json:"selector"`
	Destination *struct {
		RootID    *string `json:"root_id"`
		Directory *string `json:"directory"`
	} `json:"destination"`
	Options   apptransfers.DownloadOptions `json:"options,omitempty"`
	DryRun    bool                         `json:"dry_run,omitempty"`
	BasePath  *string                      `json:"base_path,omitempty"`
	ContextID *string                      `json:"context_id,omitempty"`
}

func absoluteRemote(s string) bool {
	return utf8.ValidString(s) && len(s) <= 4096 && strings.HasPrefix(s, "/") && path.Clean(s) == s && !strings.ContainsAny(s, "\x00\r\n")
}
func unsupportedP2() error {
	return fault.New(fault.Unsupported, "UNSUPPORTED_OPTION", "P2仅支持显式账号、绝对网盘路径及单个本地文件；参见P2能力限制")
}
func (a *API) respond(w http.ResponseWriter, r *http.Request, status int, data any) {
	a.json(w, r, status, struct {
		Data any               `json:"data"`
		Meta map[string]string `json:"meta"`
	}{data, map[string]string{"request_id": RequestID(r.Context())}})
}
func publicAccount(v account.Entry) any {
	present := map[string]bool{}
	for _, name := range []string{"BDUSS", "PTOKEN", "STOKEN", "SBOXTKN", "BAIDUID"} {
		present[strings.ToLower(name)] = v.Credentials.Cookies[name] != "" || v.Present[name]
	}
	present["cookies"] = len(v.Credentials.Cookies) > 0 || v.Present["cookies"]
	present["access_token"] = v.Credentials.AccessToken != "" || v.Present["access_token"]
	return map[string]any{"account_id": v.ID, "label": v.Label, "baidu_uid": v.Identity.UID, "baidu_uk": v.Identity.UK, "username": v.Identity.Username, "sex": v.Identity.Sex, "age": v.Identity.Age, "identity_verified": v.Identity.Verified, "login_state": v.LoginState, "revision": uintString(v.Revision), "credential_revision": uintString(max(v.CredentialVersion, v.Credentials.Revision, 1)), "credentials_present": present, "created_at": v.CreatedAt, "updated_at": v.UpdatedAt}
}
func (a *API) serveP2(id string, w http.ResponseWriter, r *http.Request) error {
	ctx, p := r.Context(), principal(r.Context())
	switch id {
	case "ACC01":
		var input struct {
			Label         string           `json:"label"`
			Credentials   *credentialInput `json:"credentials"`
			ForceUsername *string          `json:"force_login_username,omitempty"`
		}
		if err := decodeRequest(w, r, &input); err != nil {
			return err
		}
		if input.Credentials == nil {
			return invalid("缺少credentials")
		}
		if input.ForceUsername != nil {
			if *input.ForceUsername == "" || utf8.RuneCountInString(*input.ForceUsername) > 256 {
				return invalid("force_login_username格式无效")
			}
		}
		credentials, err := input.Credentials.domain()
		if err != nil {
			return err
		}
		key, err := idempotencyKey(r)
		if err != nil {
			return err
		}
		entry, err := a.deps.Accounts.ImportNamed(ctx, p, input.Label, credentials, key, deref(input.ForceUsername, ""))
		if err != nil {
			return err
		}
		w.Header().Set("Location", "/api/v1/accounts/"+entry.ID)
		w.Header().Set("ETag", etag(entry.Revision))
		a.respond(w, r, 201, publicAccount(entry))
		return nil
	case "ACC02":
		entries, err := a.deps.Accounts.List(ctx, p)
		if err != nil {
			return err
		}
		result := make([]any, 0, len(entries))
		for _, entry := range entries {
			result = append(result, publicAccount(entry))
		}
		a.success(w, r, result)
		return nil
	case "ACC03":
		entry, err := a.deps.Accounts.Get(ctx, p, r.PathValue("account_id"))
		if err != nil {
			return err
		}
		w.Header().Set("ETag", etag(entry.Revision))
		a.success(w, r, publicAccount(entry))
		return nil
	case "ACC07":
		if r.ContentLength > 0 {
			return invalid("账号验证不接受请求体")
		}
		result, err := a.deps.Accounts.Validate(ctx, p, r.PathValue("account_id"))
		if err != nil {
			return err
		}
		a.success(w, r, map[string]any{"account": publicAccount(result.Account), "valid": result.Valid, "checked_at": result.CheckedAt, "problem": publicProblem(result.Problem, RequestID(ctx))})
		return nil
	case "UPL01", "DWN01":
		return a.submitTransfer(id, w, r)
	case "JOB02", "JOB04":
		job, err := a.deps.Transfers.Get(ctx, p, r.PathValue("job_id"))
		if err != nil {
			return err
		}
		if id == "JOB04" {
			if !job.Terminal() {
				return fault.New(fault.Conflict, "JOB_NOT_FINISHED", "任务尚未完成")
			}
			a.success(w, r, publicResult(job, RequestID(ctx)))
			return nil
		}
		a.success(w, r, publicJob(job, RequestID(ctx)))
		return nil
	case "JOB06", "JOB07", "JOB08":
		var input struct {
			Reason string `json:"reason,omitempty"`
		}
		if err := decodeObject(w, r, &input, true); err != nil {
			return err
		}
		if utf8.RuneCountInString(input.Reason) > 1024 {
			return invalid("reason过长")
		}
		key, err := idempotencyKey(r)
		if err != nil {
			return err
		}
		control := apptransfers.Control{Key: key, Reason: input.Reason}
		var job transfer.Record
		if id == "JOB06" {
			job, err = a.deps.Transfers.Cancel(ctx, p, r.PathValue("job_id"), control)
		} else if id == "JOB07" {
			job, err = a.deps.Transfers.Pause(ctx, p, r.PathValue("job_id"), control)
		} else {
			job, err = a.deps.Transfers.Resume(ctx, p, r.PathValue("job_id"), control)
		}
		if err != nil {
			return err
		}
		w.Header().Set("Location", "/api/v1/jobs/"+job.ID)
		a.respond(w, r, 202, publicJob(job, RequestID(ctx)))
		return nil
	}
	return &httpError{status: 501, code: "CAPABILITY_NOT_IMPLEMENTED", message: "此操作尚未实现"}
}

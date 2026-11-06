package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	appaccounts "github.com/qjfoidnh/BaiduPCS-Go/internal/application/accounts"
	appartifacts "github.com/qjfoidnh/BaiduPCS-Go/internal/application/artifacts"
	appauth "github.com/qjfoidnh/BaiduPCS-Go/internal/application/auth"
	appconfig "github.com/qjfoidnh/BaiduPCS-Go/internal/application/configuration"
	appcontent "github.com/qjfoidnh/BaiduPCS-Go/internal/application/content"
	appcontexts "github.com/qjfoidnh/BaiduPCS-Go/internal/application/contexts"
	appfiles "github.com/qjfoidnh/BaiduPCS-Go/internal/application/files"
	applocal "github.com/qjfoidnh/BaiduPCS-Go/internal/application/local"
	applogin "github.com/qjfoidnh/BaiduPCS-Go/internal/application/login"
	appquery "github.com/qjfoidnh/BaiduPCS-Go/internal/application/query"
	appstaging "github.com/qjfoidnh/BaiduPCS-Go/internal/application/staging"
	appsystem "github.com/qjfoidnh/BaiduPCS-Go/internal/application/system"
	apptools "github.com/qjfoidnh/BaiduPCS-Go/internal/application/tools"
	apptransfers "github.com/qjfoidnh/BaiduPCS-Go/internal/application/transfers"
	appwrites "github.com/qjfoidnh/BaiduPCS-Go/internal/application/writes"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/auth"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/metrics"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/system"
)

const MaxJSONBodyBytes int64 = 1 << 20

type Dependencies struct {
	Metrics       *metrics.Registry
	Auth          *appauth.Service
	Configuration *appconfig.Service
	System        *appsystem.Service
	Accounts      *appaccounts.Service
	Transfers     *apptransfers.Service
	Files         *appfiles.Service
	Queries       *appquery.Service
	Artifacts     *appartifacts.Service
	Staging       *appstaging.Service
	Content       *appcontent.Service
	Writes        *appwrites.Service
	Tools         *apptools.Service
	Contexts      *appcontexts.Service
	Login         *applogin.Service
	CLI           *appconfig.CLI
	Diagnostics   *appsystem.Diagnostics
	Local         *applocal.Service
	Operations    []system.Operation
	Contract      []byte
	Ready         func(context.Context) error
	Draining      func() bool
	Logger        *slog.Logger
}
type API struct {
	deps     Dependencies
	mux      *http.ServeMux
	schema   configSchema
	sseSlots chan struct{}
}
type contextKey uint8

const (
	requestKey contextKey = iota
	principalKey
)

func RequestID(ctx context.Context) string { value, _ := ctx.Value(requestKey).(string); return value }
func principal(ctx context.Context) auth.Principal {
	p, _ := ctx.Value(principalKey).(auth.Principal)
	return p
}

func Implemented(id string) bool {
	if p5Operation(id) || p6ToolOperation(id) || strings.HasPrefix(id, "CTX") || (id >= "UPL05" && id <= "UPL09") || strings.HasPrefix(id, "LOG") || id == "CFG06" || id == "CFG08" || id == "CFG09" || id == "SYS05" || id == "SYS06" {
		return true
	}
	switch id {
	case "SYS07", "UPL01", "UPL02", "UPL03", "UPL04", "DWN01", "DWN02", "DWN03", "DWN04", "DWN05", "JOB07", "JOB08", "SYS01", "SYS02", "SYS03", "SYS04", "SYS08", "SYS09", "CFG01", "CFG02", "CFG03", "CFG04", "CFG05", "CFG07", "ACC01", "ACC02", "ACC03", "ACC04", "ACC05", "ACC06", "ACC07", "ACC08", "ACC09", "FIL01", "FIL02", "FIL03", "FIL04", "FIL05", "FIL06", "JOB01", "JOB02", "JOB03", "JOB04", "JOB05", "JOB06", "JOB09", "JOB11", "ART01", "ART02", "ART03", "ART04", "LOC01":
		return true
	}
	return false
}
func Implementation(id string) string {
	if Implemented(id) {
		return "implemented"
	}
	switch id {

	}
	return "planned"
}
func New(deps Dependencies) (http.Handler, error) {
	if deps.Auth == nil || deps.Configuration == nil || deps.System == nil {
		return nil, errors.New("missing HTTP application services")
	}
	if deps.Logger == nil {
		deps.Logger = slog.New(slog.NewJSONHandler(nilWriter{}, nil))
	}
	defaults, err := deps.Configuration.Defaults(auth.Principal{Scopes: []string{"admin"}})
	if err != nil {
		return nil, err
	}
	schema, err := configurationSchema(deps.Contract, publicClient(defaults))
	if err != nil {
		return nil, err
	}
	api := &API{deps: deps, mux: http.NewServeMux(), schema: schema, sseSlots: make(chan struct{}, 64)}
	grouped := map[string]map[string]system.Operation{}
	for _, op := range deps.Operations {
		if op.Implementation != Implementation(op.ID) || op.Implemented != Implemented(op.ID) {
			return nil, fmt.Errorf("contract implementation status does not match handler: %s", op.ID)
		}
		if op.Implementation == "partial" && (deps.Accounts == nil || deps.Transfers == nil) {
			return nil, errors.New("missing P2 application services")
		}
		if grouped[op.Path] == nil {
			grouped[op.Path] = map[string]system.Operation{}
		}
		grouped[op.Path][op.Method] = op
	}
	for path, methods := range grouped {
		methods := methods
		api.mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			op, ok := methods[r.Method]
			if !ok {
				allowed := []string{}
				for method := range methods {
					allowed = append(allowed, method)
				}
				sort.Strings(allowed)
				w.Header().Set("Allow", strings.Join(allowed, ", "))
				api.problem(w, r, &httpError{status: 405, code: "METHOD_NOT_ALLOWED", message: "此路径不支持该方法"})
				return
			}
			if recorder, ok := w.(*recordWriter); ok {
				recorder.operation = op.ID
				recorder.route = op.Path
			}
			if !op.Anonymous {
				values := r.Header.Values("Authorization")
				var token string
				if len(values) == 1 {
					parts := strings.Fields(values[0])
					if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
						token = parts[1]
					}
				}
				var p auth.Principal
				var err error
				if (op.ID == "DWN03" || op.ID == "DWN04") && r.URL.Query().Has("ticket") {
					if len(values) > 0 {
						api.problem(w, r, invalid("票据与Authorization不能同时提供"))
						return
					}
					p, err = api.authenticateTicket(r)
				} else {
					p, err = api.deps.Auth.Authenticate(r.Context(), token)
				}
				if err != nil {
					api.problem(w, r, err)
					return
				}
				if err = appauth.Authorize(p, op.Scopes, r.PathValue("account_id")); err != nil {
					api.problem(w, r, err)
					return
				}
				r = r.WithContext(context.WithValue(r.Context(), principalKey, p))
			}
			if op.Implementation == "planned" {
				api.problem(w, r, &httpError{status: 501, code: "CAPABILITY_NOT_IMPLEMENTED", message: "此操作尚未实现"})
				return
			}
			if api.deps.Draining != nil && api.deps.Draining() && op.ID != "SYS01" && op.ID != "SYS02" {
				api.problem(w, r, fault.New(fault.Unavailable, "SERVICE_DRAINING", "服务正在排空"))
				return
			}
			if r.URL.RawQuery != "" && !acceptsQuery(op.ID) {
				api.problem(w, r, invalid("此接口不接受查询参数"))
				return
			}
			scoped, err := api.freezePathScope(r, op.ID)
			if err != nil {
				api.problem(w, r, err)
				return
			}
			r = scoped
			if err := api.serve(op.ID, w, r); err != nil {
				api.problem(w, r, err)
			}
		})
	}
	api.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		values := r.Header.Values("Authorization")
		token := ""
		if len(values) == 1 {
			parts := strings.Fields(values[0])
			if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
				token = parts[1]
			}
		}
		if _, err := api.deps.Auth.Authenticate(r.Context(), token); err != nil {
			api.problem(w, r, err)
			return
		}
		api.problem(w, r, fault.New(fault.NotFound, "RESOURCE_NOT_FOUND", "未找到此服务资源"))
	})
	return api.middleware(api.mux), nil
}

type nilWriter struct{}

func (nilWriter) Write(p []byte) (int, error) { return len(p), nil }

type recordWriter struct {
	http.ResponseWriter
	status           int
	operation, route string
}

func (w *recordWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *recordWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(p)
}
func (w *recordWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (a *API) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		record := &recordWriter{ResponseWriter: w, operation: "unmatched", route: "unmatched"}
		random := make([]byte, 16)
		if _, err := rand.Read(random); err != nil {
			a.problem(record, r, &httpError{status: 500, code: "INTERNAL_ERROR", message: "无法创建请求标识"})
			return
		}
		id := "req_" + hex.EncodeToString(random)
		r = r.WithContext(context.WithValue(r.Context(), requestKey, id))
		limit := int64(MaxJSONBodyBytes)
		if r.Method == "PATCH" && strings.HasPrefix(r.URL.Path, "/api/v1/staging-upload-sessions/") && strings.HasSuffix(r.URL.Path, "/content") {
			limit = 32 << 20
		}
		if r.URL.Path == "/api/v1/staging-files" {
			limit = (256 << 20) + (64 << 10)
		}
		r.Body = http.MaxBytesReader(record, r.Body, limit)
		record.Header().Set("X-Request-ID", id)
		record.Header().Set("Cache-Control", "no-store")
		record.Header().Set("X-Content-Type-Options", "nosniff")
		defer func() {
			if recovered := recover(); recovered != nil {
				if record.status == 0 {
					a.problem(record, r, fault.New(fault.Internal, "INTERNAL_ERROR", "服务内部错误"))
				}
				a.deps.Logger.Error("request panic", "request_id", id, "operation", record.operation)
			}
			status := record.status
			if status == 0 {
				status = 200
			}
			a.deps.Metrics.HTTP(record.operation, status, time.Since(start))
			// Templates and fixed operation IDs only: no URL query, headers, body or raw error.
			a.deps.Logger.Info("http request", "request_id", id, "method", r.Method, "route", record.route, "operation", record.operation, "status", status, "duration_ms", time.Since(start).Milliseconds())
		}()
		// Stream operations acquire their own idle policy in P3/P4. No global
		// http.Server.WriteTimeout is imposed on SSE or content streams.
		streaming := strings.HasSuffix(r.URL.Path, "/events") || strings.HasSuffix(r.URL.Path, "/content") || r.URL.Path == "/api/v1/staging-files"
		if !streaming {
			ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
			defer cancel()
			r = r.WithContext(ctx)
			controller := http.NewResponseController(record)
			_ = controller.SetWriteDeadline(time.Now().Add(30 * time.Second))
			_ = controller.SetReadDeadline(time.Now().Add(30 * time.Second))
			defer controller.SetReadDeadline(time.Time{})
			defer controller.SetWriteDeadline(time.Time{})
		}
		next.ServeHTTP(record, r)
	})
}
func (a *API) serve(id string, w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p := principal(ctx)
	switch id {
	case "SYS01":
		a.success(w, r, map[string]string{"status": "ok"})
		return nil
	case "SYS02":
		if a.deps.Draining != nil && a.deps.Draining() {
			return fault.New(fault.Unavailable, "SERVICE_NOT_READY", "服务尚未就绪")
		}
		if a.deps.Ready != nil {
			if err := a.deps.Ready(ctx); err != nil {
				return fault.New(fault.Unavailable, "SERVICE_NOT_READY", "服务尚未就绪")
			}
		}
		a.success(w, r, map[string]string{"status": "ok"})
		return nil
	case "SYS03":
		a.success(w, r, a.deps.System.Info())
		return nil
	case "SYS04":
		a.success(w, r, a.deps.System.Capabilities())
		return nil
	case "SYS07":
		if a.deps.Metrics == nil {
			return fault.New(fault.Unavailable, "METRICS_UNAVAILABLE", "指标尚未就绪")
		}
		var queued, executing int64
		if a.deps.Transfers != nil {
			queued, executing = a.deps.Transfers.MetricsSnapshot()
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		return a.deps.Metrics.Write(w, queued, executing)
	case "SYS08":
		w.Header().Set("Content-Type", "application/json")
		w.Write(a.deps.Contract)
		return nil
	case "SYS09":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(docsHTML))
		return nil
	case "CFG01":
		snapshot, err := a.deps.Configuration.Read(ctx, p)
		if err != nil {
			return err
		}
		w.Header().Set("ETag", etag(snapshot.Revision()))
		a.success(w, r, publicSnapshot(snapshot))
		return nil
	case "CFG02":
		revision, err := ifMatch(r)
		if err != nil {
			return err
		}
		key, err := idempotencyKey(r)
		if err != nil {
			return err
		}
		var input patchInput
		if err = decodeRequest(w, r, &input); err != nil {
			return err
		}
		patch, err := input.domain()
		if err != nil {
			return err
		}
		change, err := a.deps.Configuration.Patch(ctx, p, revision, key, patch)
		if err != nil {
			return err
		}
		w.Header().Set("ETag", etag(change.Snapshot.Revision()))
		a.success(w, r, publicChange(change))
		return nil
	case "CFG03":
		if _, err := a.deps.Configuration.Defaults(p); err != nil {
			return err
		}
		a.success(w, r, a.schema)
		return nil
	case "CFG04":
		var input patchInput
		if err := decodeRequest(w, r, &input); err != nil {
			return err
		}
		patch, err := input.domain()
		if err != nil {
			return err
		}
		candidate, issues, err := a.deps.Configuration.Validate(ctx, p, patch)
		if err != nil {
			return err
		}
		var normalized *clientDTO
		if len(issues) == 0 {
			v := publicClient(candidate)
			normalized = &v
		}
		a.success(w, r, struct {
			Valid      bool          `json:"valid"`
			Normalized *clientDTO    `json:"normalized"`
			Errors     []fault.Issue `json:"errors"`
		}{len(issues) == 0, normalized, issues})
		return nil
	case "CFG05":
		revision, err := ifMatch(r)
		if err != nil {
			return err
		}
		key, err := idempotencyKey(r)
		if err != nil {
			return err
		}
		var input struct {
			Fields []string `json:"fields"`
		}
		if err = decodeRequest(w, r, &input); err != nil {
			return err
		}
		change, err := a.deps.Configuration.Reset(ctx, p, revision, key, input.Fields)
		if err != nil {
			return err
		}
		w.Header().Set("ETag", etag(change.Snapshot.Revision()))
		a.success(w, r, publicChange(change))
		return nil
	case "CFG07":
		defaults, err := a.deps.Configuration.Defaults(p)
		if err != nil {
			return err
		}
		origin, template := "home_downloads", "<HOME>/Downloads"
		if runtime.GOOS == "windows" {
			origin, template = "executable_downloads", "<executable>/Downloads"
		}
		a.success(w, r, struct {
			Client     clientDTO `json:"client"`
			Platform   string    `json:"platform"`
			Origin     string    `json:"savedir_origin"`
			Template   string    `json:"source_savedir_template"`
			Configured bool      `json:"effective_root_configured"`
		}{publicClient(defaults), runtime.GOOS + "/" + runtime.GOARCH, origin, template, defaults.SaveDir != nil})
		return nil
	}
	if id == "CFG06" || id == "CFG08" || id == "CFG09" || id == "SYS05" || id == "SYS06" {
		return a.serveManagement(id, w, r)
	}
	if strings.HasPrefix(id, "LOG") {
		return a.serveLogin(id, w, r)
	}
	if id >= "UPL05" && id <= "UPL09" {
		return a.serveReceptions(id, w, r)
	}
	if strings.HasPrefix(id, "CTX") {
		return a.serveContexts(id, w, r)
	}
	if p6ToolOperation(id) {
		return a.serveP6Tools(id, w, r)
	}
	if p5Operation(id) {
		return a.serveP5(id, w, r)
	}
	if id == "DWN02" || id == "DWN03" || id == "DWN04" || id == "DWN05" {
		return a.serveContent(id, w, r)
	}
	if id == "UPL02" || id == "UPL03" || id == "UPL04" {
		return a.serveStaging(id, w, r)
	}
	if p3Operation(id) {
		return a.serveP3(id, w, r)
	}
	return a.serveP2(id, w, r)
}
func etag(revision uint64) string { return `"` + strconv.FormatUint(revision, 10) + `"` }
func ifMatch(r *http.Request) (uint64, error) {
	values := r.Header.Values("If-Match")
	if len(values) == 0 {
		return 0, &httpError{status: 428, code: "IF_MATCH_REQUIRED", message: "更新需要If-Match"}
	}
	value := strings.TrimSpace(values[0])
	if len(values) != 1 || len(value) < 3 || value[0] != '"' || value[len(value)-1] != '"' || value[1] == '0' {
		return 0, invalid("需要一个有效强ETag")
	}
	for _, digit := range value[1 : len(value)-1] {
		if digit < '0' || digit > '9' {
			return 0, invalid("需要一个有效强ETag")
		}
	}
	revision, err := strconv.ParseUint(value[1:len(value)-1], 10, 64)
	if err != nil || revision == 0 {
		return 0, invalid("需要一个有效强ETag")
	}
	return revision, nil
}
func idempotencyKey(r *http.Request) (string, error) {
	values := r.Header.Values("Idempotency-Key")
	if len(values) == 0 {
		return "", nil
	}
	value := values[0]
	if len(values) != 1 || len(value) < 1 || len(value) > 128 {
		return "", invalid("幂等Key格式无效")
	}
	for _, b := range []byte(value) {
		if b < 0x21 || b > 0x7e {
			return "", invalid("幂等Key格式无效")
		}
	}
	return value, nil
}
func (a *API) success(w http.ResponseWriter, r *http.Request, data any) {
	a.json(w, r, 200, struct {
		Data any `json:"data"`
		Meta struct {
			RequestID string `json:"request_id"`
		} `json:"meta"`
	}{Data: data, Meta: struct {
		RequestID string `json:"request_id"`
	}{RequestID(r.Context())}})
}
func (a *API) json(w http.ResponseWriter, r *http.Request, status int, value any) {
	encoded, err := json.Marshal(value)
	if err != nil {
		a.problem(w, r, fault.New(fault.Internal, "INTERNAL_ERROR", "服务内部错误"))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if r.Method != "HEAD" {
		w.Write(append(encoded, '\n'))
	}
}

type httpError struct {
	status        int
	code, message string
}

func (e *httpError) Error() string { return e.code }
func (a *API) problem(w http.ResponseWriter, r *http.Request, err error) {
	status := 500
	code, message, retryable, issues := "INTERNAL_ERROR", "服务内部错误", false, []fault.Issue(nil)
	var transport *httpError
	if errors.As(err, &transport) {
		status, code, message = transport.status, transport.code, transport.message
	} else {
		f := fault.As(err)
		code, message, retryable, issues = f.Code, f.Message, f.Retryable, f.Issues
		switch f.Kind {
		case fault.Invalid:
			status = 400
		case fault.Unauthorized:
			status = 401
		case fault.Forbidden:
			status = 403
		case fault.NotFound:
			status = 404
		case fault.Gone:
			status = 410
		case fault.TooLarge:
			status = 413
		case fault.Conflict:
			status = 409
		case fault.Precondition:
			status = 412
		case fault.Unsupported:
			status = 422
		case fault.Unavailable:
			status = 503
		case fault.RateLimited:
			status = 429
		case fault.BadGateway:
			status = 502
		case fault.Timeout:
			status = 504
		}
	}
	if status == 401 {
		w.Header().Set("WWW-Authenticate", "Bearer")
	}
	if (status == 503 || status == 429) && retryable {
		w.Header().Set("Retry-After", "1")
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("X-Error-Code", code)
	w.WriteHeader(status)
	if r.Method == "HEAD" {
		return
	}
	value := struct {
		Type      string        `json:"type"`
		Title     string        `json:"title"`
		Status    int           `json:"status"`
		Code      string        `json:"code"`
		RequestID string        `json:"request_id"`
		Retryable bool          `json:"retryable"`
		Errors    []fault.Issue `json:"errors,omitempty"`
	}{"urn:baidupcs:error:" + strings.ToLower(strings.ReplaceAll(code, "_", "-")), message, status, code, RequestID(r.Context()), retryable, issues}
	_ = json.NewEncoder(w).Encode(value)
}

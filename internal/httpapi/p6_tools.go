package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/file"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/tool"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/transfer"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"
)

func p6ToolOperation(id string) bool {
	switch id {
	case "INT01", "INT02", "INT03", "INT04", "INT05", "INT06", "EXP01", "FIL12", "LOC02", "LOC03":
		return true
	}
	return false
}
func validLocal(f tool.LocalFile) bool {
	return configuration.ValidID(f.RootID) && configuration.ValidRelative(f.Path) && f.Path != "" && f.Path != "."
}
func validAlgorithms(a []string, allowed string) bool {
	if len(a) == 0 || len(a) > 4 {
		return false
	}
	seen := map[string]bool{}
	for _, v := range a {
		if !strings.Contains(allowed, "|"+v+"|") || seen[v] {
			return false
		}
		seen[v] = true
	}
	return true
}
func validDigest(s string, n int) bool { b, e := hex.DecodeString(s); return e == nil && len(b) == n }
func (a *API) serveP6Tools(id string, w http.ResponseWriter, r *http.Request) error {
	ctx, p, accountID := r.Context(), principal(r.Context()), r.PathValue("account_id")
	if id == "FIL12" || id == "SYS06" {
		cursor := ""
		if id == "FIL12" {
			q, e := queryValues(r, "cursor")
			if e != nil {
				return e
			}
			cursor = q.Get("cursor")
			if len(cursor) > 8192 {
				return invalid("cursor过长")
			}
		}
		g, e := a.deps.Tools.Session(ctx, p, accountID)
		if e != nil {
			return e
		}
		defer g.Close()
		var result map[string]any
		if id == "FIL12" {
			result, e = g.Changes(cursor)
		} else {
			result, e = g.Servers()
		}
		if e != nil {
			return e
		}
		a.success(w, r, result)
		return nil
	}
	if id == "INT05" {
		var in struct {
			Ref      string `json:"link_ref"`
			Expected *struct {
				Algorithm string `json:"algorithm"`
				Value     string `json:"value"`
			} `json:"expected_checksum,omitempty"`
		}
		if e := decodeRequest(w, r, &in); e != nil {
			return e
		}
		if !configuration.ValidID(in.Ref) {
			return invalid("link_ref无效")
		}
		if in.Expected != nil {
			n := 16
			if in.Expected.Algorithm == "sha256" {
				n = 32
			} else if in.Expected.Algorithm != "md5" {
				return invalid("摘要算法无效")
			}
			if !validDigest(in.Expected.Value, n) {
				return invalid("期望摘要无效")
			}
		}
		address, e := a.deps.Content.Reference(ctx, p, accountID, in.Ref)
		if e != nil {
			return e
		}
		g, e := a.deps.Tools.Session(ctx, p, accountID)
		if e != nil {
			return e
		}
		defer g.Close()
		var v tool.Checksum
		if in.Expected != nil {
			v, e = g.VerifyLink(address.URL, in.Expected.Algorithm, in.Expected.Value, address.UserAgent)
		} else {
			v, e = g.InspectLink(address.URL, address.UserAgent)
		}
		if e != nil {
			return e
		}
		a.success(w, r, v)
		return nil
	}
	if id == "INT06" {
		var in struct {
			Path      *string `json:"path"`
			Size      *string `json:"size_bytes"`
			MD5       string  `json:"md5"`
			Slice     string  `json:"slice_md5"`
			CRC       string  `json:"crc32"`
			Overwrite bool    `json:"overwrite,omitempty"`
			Base      string  `json:"base_path,omitempty"`
			Context   string  `json:"context_id,omitempty"`
		}
		if e := decodeRequest(w, r, &in); e != nil {
			return e
		}
		if in.Path == nil || *in.Path == "" || !validDigest(in.MD5, 16) || !validDigest(in.Slice, 16) {
			return invalid("旧式秒传输入无效")
		}
		if _, e := decimal(in.Size, false); e != nil || in.Size == nil {
			return invalid("文件长度无效")
		}
		if n, e := strconv.ParseUint(in.CRC, 10, 32); e != nil || strconv.FormatUint(n, 10) != in.CRC {
			return invalid("crc32无效")
		}
		if _, e := a.resolve(r, *in.Path, file.Scope{BasePath: in.Base, ContextID: in.Context}); e != nil {
			return e
		}
		return fault.New(fault.Unavailable, "UPSTREAM_CAPABILITY_UNAVAILABLE", "百度已长期停用旧式摘要秒传")
	}
	plan := tool.Plan{}
	kind := ""
	var secret string
	switch id {
	case "INT01", "INT02", "EXP01":
		var in struct {
			Paths   []string `json:"paths"`
			Base    string   `json:"base_path,omitempty"`
			Context string   `json:"context_id,omitempty"`
			Options *struct {
				Recursive   bool                     `json:"recursive,omitempty"`
				Root        string                   `json:"root_path,omitempty"`
				Format      *string                  `json:"format,omitempty"`
				Retries     *int                     `json:"max_retries,omitempty"`
				Destination *configuration.Directory `json:"destination,omitempty"`
			} `json:"options,omitempty"`
		}
		if e := decodeRequest(w, r, &in); e != nil {
			return e
		}
		if len(in.Paths) == 0 || len(in.Paths) > 100 || id != "EXP01" && in.Options != nil {
			return invalid("路径或选项无效")
		}
		for _, v := range in.Paths {
			if v == "" {
				return invalid("路径不能为空")
			}
			remote, e := a.resolve(r, v, file.Scope{BasePath: in.Base, ContextID: in.Context})
			if e != nil {
				return e
			}
			plan.Units = append(plan.Units, tool.Unit{Path: remote})
		}
		if id == "INT01" {
			g, e := a.deps.Tools.Session(ctx, p, accountID)
			if e != nil {
				return e
			}
			defer g.Close()
			items := []any{}
			counts := transfer.Counts{Total: uint64(len(plan.Units)), Processed: uint64(len(plan.Units))}
			for i, u := range plan.Units {
				v, e := g.Inspect(u.Path)
				if e != nil {
					counts.Failed++
					items = append(items, map[string]any{"status": "failed", "input": in.Paths[i], "problem": publicProblem(fault.As(e), RequestID(ctx))})
				} else {
					counts.Succeeded++
					items = append(items, map[string]any{"status": "succeeded", "input": in.Paths[i], "checksum": v})
				}
			}
			a.success(w, r, map[string]any{"items": items, "summary": publicCounts(counts), "complete": true})
			return nil
		}
		kind = "checksum_fix"
		plan.Options = json.RawMessage(`{}`)
		if id == "EXP01" {
			kind = "export"
			o := in.Options
			if o == nil {
				o = &struct {
					Recursive   bool                     `json:"recursive,omitempty"`
					Root        string                   `json:"root_path,omitempty"`
					Format      *string                  `json:"format,omitempty"`
					Retries     *int                     `json:"max_retries,omitempty"`
					Destination *configuration.Directory `json:"destination,omitempty"`
				}{}
			}
			format, retries := deref(o.Format, "jsonl"), deref(o.Retries, 3)
			if format != "jsonl" && format != "legacy_command" && format != "legacy_link" || retries < 0 || retries > 100 || utf8.RuneCountInString(o.Root) > 4096 || strings.ContainsRune(o.Root, 0) {
				return invalid("导出选项无效")
			}
			plan.Destination = o.Destination
			plan.Options, _ = json.Marshal(map[string]any{"recursive": o.Recursive, "root_path": o.Root, "format": format, "max_retries": retries})
			if o.Destination != nil {
				var m map[string]any
				_ = json.Unmarshal(plan.Options, &m)
				m["destination"] = o.Destination
				plan.Options, _ = json.Marshal(m)
			}
		}
	case "INT03":
		var in struct {
			Root    string   `json:"root_id"`
			Paths   []string `json:"paths"`
			Options *struct {
				Algorithms []string `json:"algorithms,omitempty"`
			} `json:"options,omitempty"`
		}
		if e := decodeRequest(w, r, &in); e != nil {
			return e
		}
		if len(in.Paths) == 0 || len(in.Paths) > 100 {
			return invalid("本地选择数量无效")
		}
		algs := []string{"md5", "slice_md5", "crc32"}
		if in.Options != nil && in.Options.Algorithms != nil {
			algs = in.Options.Algorithms
		}
		if !validAlgorithms(algs, "|md5|slice_md5|crc32|block_md5|") {
			return invalid("摘要算法无效")
		}
		for _, v := range in.Paths {
			f := tool.LocalFile{RootID: in.Root, Path: v}
			if !validLocal(f) {
				return invalid("本地引用无效")
			}
			plan.Units = append(plan.Units, tool.Unit{Local: f})
		}
		kind = "local_checksum"
		plan.Options, _ = json.Marshal(map[string]any{"algorithms": algs})
	case "INT04":
		var in struct {
			Items []struct {
				Path  string         `json:"path"`
				Local tool.LocalFile `json:"local_file"`
			} `json:"items"`
			Options *struct {
				Algorithms []string `json:"algorithms,omitempty"`
			} `json:"options,omitempty"`
			Base    string `json:"base_path,omitempty"`
			Context string `json:"context_id,omitempty"`
		}
		if e := decodeRequest(w, r, &in); e != nil {
			return e
		}
		if len(in.Items) == 0 || len(in.Items) > 100 {
			return invalid("校验选择数量无效")
		}
		algs := []string{"size", "md5"}
		if in.Options != nil && in.Options.Algorithms != nil {
			algs = in.Options.Algorithms
		}
		if !validAlgorithms(algs, "|size|md5|slice_md5|crc32|") {
			return invalid("校验算法无效")
		}
		for _, v := range in.Items {
			if v.Path == "" || !validLocal(v.Local) {
				return invalid("校验文件引用无效")
			}
			remote, e := a.resolve(r, v.Path, file.Scope{BasePath: in.Base, ContextID: in.Context})
			if e != nil {
				return e
			}
			plan.Units = append(plan.Units, tool.Unit{Path: remote, Local: v.Local})
		}
		kind = "file_verification"
		plan.Options, _ = json.Marshal(map[string]any{"algorithms": algs})
	case "LOC02", "LOC03":
		var in struct {
			Sources     []tool.LocalFile         `json:"sources"`
			Destination *configuration.Directory `json:"destination"`
			Options     *struct {
				Method string `json:"method"`
				Gzip   *bool  `json:"gzip,omitempty"`
				Remove bool   `json:"remove_source,omitempty"`
			} `json:"options"`
			Key string `json:"key"`
		}
		if e := decodeRequest(w, r, &in); e != nil {
			return e
		}
		if in.Destination == nil || in.Options == nil || len(in.Sources) == 0 || len(in.Sources) > 100 || len(in.Key) == 0 || utf8.RuneCountInString(in.Key) > 8192 || !validCryptoMethod(in.Options.Method) {
			return invalid("加解密输入无效")
		}
		for _, f := range in.Sources {
			if !validLocal(f) {
				return invalid("本地来源无效")
			}
			if in.Options.Remove && (!p.AllowsRoot(f.RootID) || !p.Has("local.write")) {
				return fault.New(fault.Forbidden, "FORBIDDEN", "删除源文件需要写权限")
			}
			plan.Units = append(plan.Units, tool.Unit{Local: f})
		}
		kind = "local_encryption"
		if id == "LOC03" {
			kind = "local_decryption"
		}
		plan.Destination = in.Destination
		plan.Options, _ = json.Marshal(map[string]any{"method": in.Options.Method, "gzip": deref(in.Options.Gzip, true), "remove_source": in.Options.Remove})
		secret = in.Key
	}
	if plan.Destination != nil && (!configuration.ValidID(plan.Destination.RootID) || !configuration.ValidRelative(plan.Destination.Directory)) {
		return invalid("本地目标无效")
	}
	key, e := idempotencyKey(r)
	if e != nil {
		return e
	}
	if secret != "" {
		binding, _ := json.Marshal([]any{p.ID, id, key, plan})
		if key == "" {
			nonce, e := randomID()
			if e != nil {
				return e
			}
			binding = append(binding, []byte(nonce)...)
		}
		plan.KeyRef, e = a.deps.Tools.RememberSecret(string(binding), secret)
		if e != nil {
			return e
		}
	}
	raw, _ := json.Marshal(plan)
	remote := ""
	if len(plan.Units) > 0 {
		remote = plan.Units[0].Path
	}
	job, e := a.deps.Transfers.Submit(ctx, p, transfer.Request{Kind: kind, AccountID: accountID, RemotePath: remote, ToolPlan: string(raw), Public: true, SemanticInput: contextSemantic(r)}, key)
	if e != nil {
		a.deps.Tools.ForgetSecret(plan.KeyRef)
		return e
	}
	if plan.KeyRef != "" {
		if current, err := a.deps.Transfers.Get(ctx, p, job.ID); err == nil && (current.Terminal() || current.State == "interrupted" || current.State == "paused") {
			a.deps.Tools.ForgetSecret(plan.KeyRef)
		}
	}
	w.Header().Set("Location", "/api/v1/jobs/"+job.ID)
	a.respond(w, r, 202, publicJob(job, RequestID(ctx)))
	return nil
}

func validCryptoMethod(s string) bool {
	for _, bits := range []string{"128", "192", "256"} {
		for _, mode := range []string{"ctr", "cfb", "ofb"} {
			if s == "aes-"+bits+"-"+mode {
				return true
			}
		}
	}
	return false
}

func randomID() (string, error) {
	var b [16]byte
	_, e := rand.Read(b[:])
	return hex.EncodeToString(b[:]), e
}

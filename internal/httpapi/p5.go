package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	apptransfers "github.com/qjfoidnh/BaiduPCS-Go/internal/application/transfers"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/file"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/transfer"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/write"
)

func p5Operation(id string) bool {
	switch id {
	case "FIL07", "FIL08", "FIL09", "FIL10", "FIL11", "SHR01", "SHR02", "SHR03", "SHR04", "TRF01", "TRF02", "OFF01", "OFF02", "OFF03", "OFF04", "OFF05", "OFF06", "OFF07", "REC01", "REC02", "REC03", "REC04", "JOB10":
		return true
	}
	return false
}
func canonicalIDs(ids []string, max int) error {
	if len(ids) == 0 {
		return invalid("ID集合不能为空")
	}
	if len(ids) > max {
		return fault.New(fault.TooLarge, "LIMIT_EXCEEDED", "ID数量超过限制")
	}
	seen := map[string]bool{}
	for _, id := range ids {
		v, err := strconv.ParseInt(id, 10, 64)
		if err != nil || v <= 0 || strconv.FormatInt(v, 10) != id || seen[id] {
			return invalid("ID必须是唯一规范正十进制整数")
		}
		seen[id] = true
	}
	return nil
}
func (a *API) resolveWritePaths(r *http.Request, paths []string, base, context string) ([]string, error) {
	if len(paths) == 0 || len(paths) > 100 {
		return nil, invalid("路径数量必须为1至100")
	}
	out := []string{}
	seen := map[string]bool{}
	for _, p := range paths {
		if p == "" {
			return nil, invalid("网盘路径不能为空")
		}
		v, err := a.resolve(r, p, file.Scope{BasePath: base, ContextID: context})
		if err != nil {
			return nil, err
		}
		if v == "/" {
			return nil, invalid("写操作不能使用根目录")
		}
		if !seen[v] {
			out = append(out, v)
			seen[v] = true
		}
	}
	return out, nil
}
func publicShare(s write.Share) any {
	var password, combined any
	if s.Password != "" {
		password = s.Password
	}
	if s.Combined && s.Password != "" {
		u, err := url.Parse(s.Link)
		if err == nil {
			q := u.Query()
			q.Set("pwd", s.Password)
			u.RawQuery = q.Encode()
			combined = u.String()
		}
	}
	out := map[string]any{"share_id": s.ID, "link": s.Link, "password": password, "combined_link": combined}
	for k, v := range s.Fields {
		out[k] = v
	}
	return out
}
func publicOffline(v write.Offline) any {
	files := []any{}
	for _, f := range v.Files {
		files = append(files, map[string]string{"name": f.Name, "size_bytes": strconv.FormatInt(f.Size, 10)})
	}
	return map[string]any{"offline_task_id": v.ID, "status": v.Status, "status_text": v.StatusText, "size_bytes": strconv.FormatInt(v.Size, 10), "finished_bytes": strconv.FormatInt(v.Finished, 10), "created_at": businessTime(v.Created), "started_at": businessTime(v.Started), "finished_at": businessTime(v.Ended), "save_path": v.Path, "source_url": v.URL, "name": v.Name, "od_type": v.Type, "files": files}
}
func businessTime(seconds int64) any {
	if seconds <= 0 || seconds > 253402300799 {
		return nil
	}
	return time.Unix(seconds, 0).UTC().Format(time.RFC3339)
}
func pageMeta(page int, size any, more any, request string) any {
	return map[string]any{"request_id": request, "pagination": map[string]any{"page": page, "page_size": size, "total": nil, "has_more": more, "consistency": "upstream_live"}}
}
func (a *API) serveP5(id string, w http.ResponseWriter, r *http.Request) error {
	ctx, p := r.Context(), principal(r.Context())
	account := r.PathValue("account_id")
	requestID := RequestID(ctx)
	switch id {
	case "JOB10":
		var in struct {
			Dry bool `json:"dry_run"`
		}
		if err := decodeObject(w, r, &in, true); err != nil {
			return err
		}
		key, err := idempotencyKey(r)
		if err != nil {
			return err
		}
		job, err := a.deps.Transfers.Reconcile(ctx, p, r.PathValue("job_id"), in.Dry, apptransfers.Control{Key: key})
		if err != nil {
			return err
		}
		a.respond(w, r, 202, publicJob(job, requestID))
		return nil
	case "SHR01":
		var in struct {
			Paths    []string `json:"paths"`
			Password *string  `json:"password"`
			Period   int      `json:"period_days"`
			Combined bool     `json:"combined_link"`
			Base     string   `json:"base_path"`
			Context  string   `json:"context_id"`
		}
		if err := decodeRequest(w, r, &in); err != nil {
			return err
		}
		paths, err := a.resolveWritePaths(r, in.Paths, in.Base, in.Context)
		if err != nil {
			return err
		}
		key, err := idempotencyKey(r)
		if err != nil {
			return err
		}
		v, err := a.deps.Writes.CreateShare(ctx, p, account, paths, in.Password, in.Period, in.Combined, key)
		if err != nil {
			return err
		}
		w.Header().Set("Location", "/api/v1/accounts/"+account+"/shares/"+v.ID)
		a.respond(w, r, 201, publicShare(v))
		return nil
	case "SHR02":
		q, err := queryValues(r, "page", "include_passwords")
		if err != nil {
			return err
		}
		page, err := queryInt(q, "page", 1, 1, 2147483647)
		if err != nil {
			return err
		}
		passwords := false
		if raw := q.Get("include_passwords"); raw != "" {
			if raw != "true" && raw != "false" {
				return invalid("include_passwords必须为true/false")
			}
			passwords = raw == "true"
		}
		values, err := a.deps.Writes.Shares(ctx, p, account, page, passwords)
		if err != nil {
			return err
		}
		out := []any{}
		for _, v := range values {
			out = append(out, publicShare(v))
		}
		var more any
		if len(values) == 0 {
			more = false
		}
		a.paged(w, r, out, pageMeta(page, nil, more, requestID))
		return nil
	case "SHR03":
		if err := canonicalIDs([]string{r.PathValue("share_id")}, 1); err != nil {
			return err
		}
		v, err := a.deps.Writes.Share(ctx, p, account, r.PathValue("share_id"))
		if err != nil {
			return err
		}
		a.success(w, r, publicShare(v))
		return nil
	case "TRF01":
		var in struct {
			Input     string  `json:"input"`
			Password  *string `json:"password"`
			ID        string  `json:"inspection_id"`
			Directory string  `json:"directory_fs_id"`
			Cursor    string  `json:"cursor"`
			Size      *int    `json:"page_size"`
		}
		if err := decodeRequest(w, r, &in); err != nil {
			return err
		}
		size := deref(in.Size, 100)
		if size < 1 || size > 1000 {
			return invalid("page_size无效")
		}
		if in.Password != nil && len(*in.Password) != 4 {
			return invalid("提取码无效")
		}
		if in.Directory != "" {
			if err := canonicalIDs([]string{in.Directory}, 1); err != nil {
				return err
			}
		}
		inspection, page, err := a.deps.Writes.Inspect(ctx, p, account, in.Input, in.Password, in.ID, in.Directory, in.Cursor, size)
		if err != nil {
			return err
		}
		entries := []any{}
		for _, raw := range page.Items {
			var entry file.Entry
			if json.Unmarshal(raw, &entry) != nil {
				return fault.New(fault.Internal, "STORAGE_INVALID", "分享检查条目无效")
			}
			entries = append(entries, publicFile(entry, []string{"all"}))
		}
		a.paged(w, r, map[string]any{"inspection_id": inspection.ID, "account_id": account, "share_id": inspection.Access.ShareID, "entries": entries, "expires_at": inspection.Access.ExpiresAt, "directory_fs_id": nullable(in.Directory), "selection_complete": !page.HasMore}, cursorMeta(page, requestID))
		return nil
	case "OFF02":
		q, err := queryValues(r, "cursor", "page_size")
		if err != nil {
			return err
		}
		size, err := queryInt(q, "page_size", 100, 1, 1000)
		if err != nil {
			return err
		}
		page, err := a.deps.Writes.OfflineList(ctx, p, account, q.Get("cursor"), size)
		if err != nil {
			return err
		}
		out := []any{}
		for _, raw := range page.Items {
			var task write.Offline
			if err = json.Unmarshal(raw, &task); err != nil {
				return err
			}
			out = append(out, publicOffline(task))
		}
		meta := cursorMeta(page, requestID).(map[string]any)
		meta["upstream_window"] = map[string]any{"start": 0, "limit": 1000, "complete_history": false}
		a.paged(w, r, out, meta)
		return nil
	case "OFF03", "OFF07":
		ids := []string{r.PathValue("offline_task_id")}
		if id == "OFF03" {
			var in struct {
				IDs []string `json:"offline_task_ids"`
			}
			if err := decodeRequest(w, r, &in); err != nil {
				return err
			}
			ids = in.IDs
		}
		if err := canonicalIDs(ids, 100); err != nil {
			return err
		}
		values, err := a.deps.Writes.OfflineQuery(ctx, p, account, ids)
		if err != nil {
			return err
		}
		byID := map[string]write.Offline{}
		for _, v := range values {
			byID[v.ID] = v
		}
		if id == "OFF07" {
			v, ok := byID[ids[0]]
			if !ok || v.Missing {
				return fault.New(fault.NotFound, "OFFLINE_TASK_NOT_FOUND", "百度离线任务不存在")
			}
			a.success(w, r, publicOffline(v))
			return nil
		}
		items := []any{}
		counts := transfer.Counts{Total: uint64(len(ids)), Processed: uint64(len(ids))}
		for _, id := range ids {
			v, ok := byID[id]
			if ok && !v.Missing {
				counts.Succeeded++
				items = append(items, map[string]any{"status": "succeeded", "input": id, "task": publicOffline(v)})
			} else {
				counts.Failed++
				items = append(items, map[string]any{"status": "failed", "input": id, "problem": publicProblem(fault.New(fault.NotFound, "OFFLINE_TASK_NOT_FOUND", "百度离线任务不存在"), requestID)})
			}
		}
		a.success(w, r, map[string]any{"items": items, "summary": publicCounts(counts), "complete": true})
		return nil
	case "REC01":
		q, err := queryValues(r, "page")
		if err != nil {
			return err
		}
		page, err := queryInt(q, "page", 1, 1, 2147483647)
		if err != nil {
			return err
		}
		values, err := a.deps.Writes.Recycle(ctx, p, account, page)
		if err != nil {
			return err
		}
		out := []any{}
		for _, v := range values {
			e := v.Entry
			out = append(out, map[string]any{"fs_id": e.ID, "path": e.Path, "name": e.Name, "is_dir": e.IsDir, "size_bytes": strconv.FormatInt(e.Size, 10), "created_at": e.CreatedAt, "modified_at": e.ModifiedAt, "deleted_at": nil, "expires_at": nil, "upstream_left_time": v.LeftTime, "md5": e.MD5})
		}
		var more any
		if len(values) < 100 {
			more = false
		}
		a.paged(w, r, out, pageMeta(page, 100, more, requestID))
		return nil
	}
	originalBody, readErr := io.ReadAll(r.Body)
	if readErr != nil {
		var limit *http.MaxBytesError
		if errors.As(readErr, &limit) {
			return fault.New(fault.TooLarge, "LIMIT_EXCEEDED", "请求体超过限制")
		}
		return invalid("请求体读取失败")
	}
	canonical := ""
	if len(bytes.TrimSpace(originalBody)) > 0 {
		var body any
		decoder := json.NewDecoder(bytes.NewReader(originalBody))
		decoder.UseNumber()
		if err := decoder.Decode(&body); err != nil {
			return invalid("JSON格式无效")
		}
		normalized, _ := json.Marshal(body)
		canonical = string(normalized)
	}
	r.Body = io.NopCloser(bytes.NewReader(originalBody))
	plan := write.Plan{Units: []write.Unit{}}
	kind := ""
	switch id {
	case "FIL07":
		var in struct {
			Paths   []string `json:"paths"`
			Base    string   `json:"base_path"`
			Context string   `json:"context_id"`
		}
		if err := decodeRequest(w, r, &in); err != nil {
			return err
		}
		paths, err := a.resolveWritePaths(r, in.Paths, in.Base, in.Context)
		if err != nil {
			return err
		}
		for _, v := range paths {
			plan.Units = append(plan.Units, write.Unit{To: v})
		}
		kind = "mkdir"
	case "FIL08":
		var in struct {
			Selector *struct {
				Kind   string   `json:"kind"`
				Values []string `json:"values"`
			} `json:"selector"`
			Ignore  bool   `json:"ignore_missing"`
			Base    string `json:"base_path"`
			Context string `json:"context_id"`
		}
		if err := decodeRequest(w, r, &in); err != nil {
			return err
		}
		if in.Selector == nil || len(in.Selector.Values) == 0 || len(in.Selector.Values) > 100 {
			return invalid("缺少有效selector")
		}
		key, err := idempotencyKey(r)
		if err != nil {
			return err
		}
		if replay, err := a.deps.Transfers.ReplaySubmission(ctx, p, "file_delete", account, key, canonical); err != nil {
			return err
		} else if replay != nil {
			a.respond(w, r, 202, publicJob(*replay, requestID))
			return nil
		}
		units, err := a.deps.Writes.Selection(ctx, p, account, in.Selector.Kind, in.Selector.Values, a.frozenScope(r, file.Scope{BasePath: in.Base, ContextID: in.Context}))
		if err != nil {
			return err
		}
		for _, unit := range units {
			if unit.From == "/" {
				return invalid("禁止删除根目录")
			}
			plan.Units = append(plan.Units, unit)
		}
		plan.Options.IgnoreMissing = in.Ignore
		kind = "file_delete"
	case "FIL09", "FIL10":
		var in struct {
			Items []struct {
				From *string `json:"from"`
				To   *string `json:"to"`
			} `json:"items"`
			Policy  string `json:"conflict_policy"`
			Base    string `json:"base_path"`
			Context string `json:"context_id"`
		}
		if err := decodeRequest(w, r, &in); err != nil {
			return err
		}
		if len(in.Items) == 0 || len(in.Items) > 100 {
			return invalid("items数量无效")
		}
		for _, v := range in.Items {
			if v.From == nil || v.To == nil {
				return invalid("缺少from/to")
			}
			paths, err := a.resolveWritePaths(r, []string{*v.From, *v.To}, in.Base, in.Context)
			if err != nil {
				return err
			}
			if len(paths) != 2 || paths[0] == paths[1] || strings.HasPrefix(paths[1], paths[0]+"/") {
				return invalid("源和目标路径冲突")
			}
			plan.Units = append(plan.Units, write.Unit{From: paths[0], To: paths[1]})
		}
		plan.Options.Policy = in.Policy
		kind = "file_copy"
		if id == "FIL10" {
			kind = "file_move"
		}
	case "FIL11":
		var in struct {
			Path    *string `json:"path"`
			Target  *string `json:"target_path"`
			Policy  string  `json:"conflict_policy"`
			Base    string  `json:"base_path"`
			Context string  `json:"context_id"`
		}
		if err := decodeRequest(w, r, &in); err != nil {
			return err
		}
		if in.Path == nil || in.Target == nil {
			return invalid("缺少path/target_path")
		}
		paths, err := a.resolveWritePaths(r, []string{*in.Path, *in.Target}, in.Base, in.Context)
		if err != nil {
			return err
		}
		if len(paths) != 2 || path.Dir(paths[0]) != path.Dir(paths[1]) {
			return invalid("重命名必须使用同一父目录")
		}
		plan.Units = []write.Unit{{From: paths[0], To: paths[1]}}
		plan.Options.Policy = in.Policy
		kind = "file_rename"
	case "SHR04":
		var in struct {
			IDs []string `json:"share_ids"`
		}
		if err := decodeRequest(w, r, &in); err != nil {
			return err
		}
		if err := canonicalIDs(in.IDs, 100); err != nil {
			return err
		}
		for _, id := range in.IDs {
			plan.Units = append(plan.Units, write.Unit{ID: id})
		}
		kind = "share_cancel"
	case "OFF01":
		var in struct {
			URLs    []string `json:"source_urls"`
			Path    *string  `json:"save_path"`
			Base    string   `json:"base_path"`
			Context string   `json:"context_id"`
		}
		if err := decodeRequest(w, r, &in); err != nil {
			return err
		}
		if len(in.URLs) == 0 || len(in.URLs) > 100 || in.Path == nil || in.Path != nil && *in.Path == "" {
			return invalid("离线创建输入无效")
		}
		target, err := a.resolve(r, *in.Path, file.Scope{BasePath: in.Base, ContextID: in.Context})
		if err != nil {
			return err
		}
		for _, v := range in.URLs {
			if v == "" || len(v) > 16384 || strings.ContainsAny(v, "\x00\r\n") {
				return invalid("离线source URL格式无效")
			}
			u, err := url.Parse(v)
			if err != nil || u.Scheme == "" {
				return invalid("离线source URL缺少协议")
			}
			plan.Units = append(plan.Units, write.Unit{SourceURL: v, To: target})
		}
		kind = "offline_add"
	case "OFF04", "OFF05":
		var in struct {
			IDs []string `json:"offline_task_ids"`
		}
		if err := decodeRequest(w, r, &in); err != nil {
			return err
		}
		if err := canonicalIDs(in.IDs, 100); err != nil {
			return err
		}
		for _, v := range in.IDs {
			plan.Units = append(plan.Units, write.Unit{ID: v})
		}
		kind = "offline_cancel"
		if id == "OFF05" {
			kind = "offline_delete"
		}
	case "REC02", "REC03":
		var in struct {
			IDs []string `json:"fs_ids"`
		}
		if err := decodeRequest(w, r, &in); err != nil {
			return err
		}
		if err := canonicalIDs(in.IDs, 100); err != nil {
			return err
		}
		for _, v := range in.IDs {
			plan.Units = append(plan.Units, write.Unit{ID: v})
		}
		kind = "recycle_restore"
		if id == "REC03" {
			kind = "recycle_delete"
		}
	case "OFF06", "REC04":
		if len(bytes.TrimSpace(originalBody)) > 0 {
			return invalid("清空操作不接受请求体")
		}
		plan.Units = []write.Unit{{}}
		kind = "offline_clear"
		if id == "REC04" {
			kind = "recycle_clear"
		}
	case "TRF02":
		var in struct {
			Share *struct {
				Input    string  `json:"input"`
				Password *string `json:"password"`
				ID       string  `json:"inspection_id"`
			} `json:"share"`
			Target  *string                   `json:"target_dir"`
			IDs     []string                  `json:"fs_ids"`
			Options shareTransferInputOptions `json:"options"`
			Base    string                    `json:"base_path"`
			Context string                    `json:"context_id"`
		}
		if err := decodeRequest(w, r, &in); err != nil {
			return err
		}
		if in.Share == nil || in.Target == nil || in.Target != nil && *in.Target == "" {
			return invalid("缺少share/target_dir")
		}
		if in.Share != nil && (in.Share.ID != "" && (in.Share.Input != "" || in.Share.Password != nil) || in.Share.ID == "" && in.Share.Input == "") {
			return invalid("share必须选择input或inspection_id")
		}
		if in.Share.Password != nil && !validSharePassword(*in.Share.Password) {
			return invalid("提取码无效")
		}
		if in.Options.Randomize {
			return fault.New(fault.Unsupported, "UNSUPPORTED_OPTION", "randomize_name/Rname/SuperTransfer尚无有效实现")
		}
		if in.Options.Download && (in.Options.Destination == nil || !configuration.ValidID(in.Options.Destination.RootID) || !configuration.ValidRelative(in.Options.Destination.Directory)) {
			return invalid("下载子任务目标无效")
		}
		target, err := a.resolve(r, *in.Target, file.Scope{BasePath: in.Base, ContextID: in.Context})
		if err != nil {
			return err
		}
		key, err := idempotencyKey(r)
		if err != nil {
			return err
		}
		if replay, err := a.deps.Transfers.ReplaySubmission(ctx, p, "share_transfer", account, key, canonical); err != nil {
			return err
		} else if replay != nil {
			w.Header().Set("Location", "/api/v1/jobs/"+replay.ID)
			a.respond(w, r, 202, publicJob(*replay, requestID))
			return nil
		}
		access, err := a.deps.Writes.Access(ctx, p, account, in.Share.ID, in.Share.Input, in.Share.Password)
		if err != nil {
			return err
		}
		selected := in.IDs
		if selected == nil {
			selected = access.RootIDs
		} else if err = canonicalIDs(selected, 100); err != nil {
			return err
		}
		for _, id := range selected {
			var entry *file.Entry
			for i := range access.Entries {
				if access.Entries[i].ID == id {
					entry = &access.Entries[i]
					break
				}
			}
			if entry == nil {
				return fault.New(fault.Forbidden, "SHARE_SELECTION_FORBIDDEN", "fs_id不属于本次分享检查")
			}
			plan.Units = append(plan.Units, write.Unit{ID: id, From: entry.Path, To: path.Join(target, entry.Name), Before: entry})
		}
		access.Entries = nil
		access.RootIDs = nil
		plan.Share = &access
		plan.Options = in.Options.domain()
		kind = "share_transfer"
	default:
		return fault.New(fault.Unsupported, "UNSUPPORTED_JOB_ACTION", "未知写操作")
	}
	if len(plan.Units) == 0 {
		return fault.New(fault.NotFound, "NO_MATCHING_ITEMS", "没有匹配的写项目")
	}
	raw, _ := json.Marshal(plan)
	remote := "/"
	for _, u := range plan.Units {
		if u.To != "" {
			remote = u.To
			break
		}
		if u.From != "" {
			remote = u.From
			break
		}
	}
	if kind == "share_transfer" {
		remote = path.Dir(plan.Units[0].To)
	}
	key, err := idempotencyKey(r)
	if err != nil {
		return err
	}
	job, err := a.deps.Transfers.Submit(ctx, p, transfer.Request{Kind: kind, AccountID: account, WritePlan: string(raw), SemanticInput: canonical, RemotePath: remote, Public: true}, key)
	if err != nil {
		return err
	}
	w.Header().Set("Location", "/api/v1/jobs/"+job.ID)
	a.respond(w, r, 202, publicJob(job, requestID))
	return nil
}

func validSharePassword(value string) bool {
	if len(value) != 4 {
		return false
	}
	for _, c := range value {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

type shareTransferInputOptions struct {
	Collect     bool `json:"collect"`
	Randomize   bool `json:"randomize_name"`
	Download    bool `json:"download_after"`
	Destination *struct {
		RootID    string `json:"root_id"`
		Directory string `json:"directory"`
	} `json:"download_destination"`
	DownloadOptions json.RawMessage `json:"download_options"`
}

func (o shareTransferInputOptions) domain() write.Options {
	return write.Options{Collect: o.Collect, Randomize: o.Randomize, Download: o.Download, Destination: o.Destination, DownloadOptions: o.DownloadOptions}
}

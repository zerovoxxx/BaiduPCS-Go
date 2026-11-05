package httpapi

import (
	"encoding/json"
	appfiles "github.com/qjfoidnh/BaiduPCS-Go/internal/application/files"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/application/query"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/account"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/artifact"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/file"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/transfer"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

func p3Operation(id string) bool {
	switch id {
	case "ACC04", "ACC05", "ACC06", "ACC08", "ACC09", "FIL01", "FIL02", "FIL03", "FIL04", "FIL05", "FIL06", "JOB01", "JOB03", "JOB05", "JOB09", "JOB11", "LOC01", "ART01", "ART02", "ART03", "ART04":
		return true
	}
	return false
}
func acceptsQuery(id string) bool {
	switch id {
	case "FIL01", "FIL02", "FIL04", "JOB01", "JOB03":
		return true
	}
	return false
}
func queryValues(r *http.Request, allowed ...string) (url.Values, error) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, invalid("查询参数格式无效")
	}
	set := map[string]bool{}
	for _, k := range allowed {
		set[k] = true
	}
	for k, v := range q {
		if !set[k] || len(v) != 1 || v[0] == "" {
			return nil, invalid("未知、空白或重复的查询参数")
		}
	}
	return q, nil
}
func queryInt(q url.Values, k string, def, lo, hi int) (int, error) {
	s := q.Get(k)
	if s == "" {
		return def, nil
	}
	n, e := strconv.Atoi(s)
	if e != nil || strconv.Itoa(n) != s || n < lo || n > hi {
		return 0, invalid("分页或数值参数无效")
	}
	return n, nil
}
func fields(q url.Values) ([]string, error) {
	value := q.Get("fields")
	if value == "" {
		return []string{"all"}, nil
	}
	result := strings.Split(value, ",")
	seen := map[string]bool{}
	for _, v := range result {
		switch v {
		case "all", "fs_id", "app_id", "path", "name", "is_dir", "size_bytes", "created_at", "modified_at", "md5", "md5_status", "block_list", "has_subdirectories":
		default:
			return nil, invalid("fields字段无效")
		}
		if seen[v] {
			return nil, invalid("fields不能重复")
		}
		seen[v] = true
	}
	if len(result) > 13 {
		return nil, invalid("fields过多")
	}
	return result, nil
}
func publicFile(e file.Entry, projection []string) any {
	result := map[string]any{"fs_id": e.ID, "path": e.Path, "name": e.Name, "is_dir": e.IsDir, "size_bytes": strconv.FormatInt(e.Size, 10), "md5_status": e.MD5Status}
	optional := map[string]any{"app_id": e.AppID, "created_at": e.CreatedAt, "modified_at": e.ModifiedAt, "md5": e.MD5, "block_list": append([]string{}, e.Blocks...), "has_subdirectories": e.HasSubdirectories}
	all := false
	for _, k := range projection {
		if k == "all" {
			all = true
		}
	}
	for k, v := range optional {
		if all {
			result[k] = v
			continue
		}
		for _, field := range projection {
			if field == k {
				result[k] = v
			}
		}
	}
	return result
}
func filePage(items []file.Entry, projection []string) any {
	entries := []any{}
	var files, dirs, bytes uint64
	for _, e := range items {
		entries = append(entries, publicFile(e, projection))
		if e.IsDir {
			dirs++
		} else {
			files++
			bytes += uint64(e.Size)
		}
	}
	return map[string]any{"entries": entries, "summary": map[string]string{"returned_items": uintString(uint64(len(items))), "returned_files": uintString(files), "returned_directories": uintString(dirs), "returned_bytes": uintString(bytes)}}
}
func cursorMeta(p query.Page, request string) any {
	return map[string]any{"request_id": request, "pagination": map[string]any{"next_cursor": nullable(p.NextCursor), "has_more": p.HasMore, "consistency": "snapshot", "expires_at": nullable(p.ExpiresAt)}}
}
func (a *API) paged(w http.ResponseWriter, r *http.Request, data, meta any) {
	a.json(w, r, 200, map[string]any{"data": data, "meta": meta})
}
func publicArtifact(v artifact.Metadata) any {
	return map[string]any{"artifact_id": v.ID, "job_id": v.JobID, "filename": v.Filename, "media_type": v.MediaType, "size_bytes": strconv.FormatInt(v.Size, 10), "created_at": v.CreatedAt, "expires_at": v.ExpiresAt, "content_url": "/api/v1/artifacts/" + v.ID + "/content", "complete": v.Complete}
}
func publicItem(v transfer.Item, requestID string) any {
	var local any
	if v.RootID != "" {
		local = map[string]string{"root_id": v.RootID, "relative_path": v.RelativePath}
	}
	var size, offset any
	if v.Size != nil {
		size = strconv.FormatInt(*v.Size, 10)
	}
	if v.Offset != nil {
		offset = strconv.FormatInt(*v.Offset, 10)
	}
	var problem any
	if v.Failure != nil {
		problem = publicProblem(jobFault(v.Failure.Code), requestID)
	}
	return map[string]any{"item_id": v.ID, "index": uintString(v.Index), "path": nullable(v.Path), "local_file": local, "state": v.State, "attempts": v.Attempts, "size_bytes": size, "transferred_bytes": strconv.FormatInt(v.Transferred, 10), "written_bytes": strconv.FormatInt(v.Written, 10), "problem": problem, "checkpoint": map[string]any{"available": v.CheckpointAvailable, "verified": false, "offset_bytes": offset, "reason": nil}, "workers": []any{}}
}
func (a *API) serveP3(id string, w http.ResponseWriter, r *http.Request) error {
	ctx, p := r.Context(), principal(r.Context())
	accountID := r.PathValue("account_id")
	requestID := RequestID(ctx)
	switch id {
	case "ACC04", "ACC06":
		revision, err := ifMatch(r)
		if err != nil {
			return err
		}
		key, err := idempotencyKey(r)
		if err != nil {
			return err
		}
		var label *string
		var patch account.Patch
		if id == "ACC04" {
			var input struct {
				Label *string `json:"label"`
			}
			if err = decodeRequest(w, r, &input); err != nil {
				return err
			}
			if input.Label == nil {
				return invalid("缺少label")
			}
			label = input.Label
		} else {
			if err = decodeObjectMode(w, r, &patch, false, true); err != nil {
				return err
			}
		}
		v, err := a.deps.Accounts.Patch(ctx, p, accountID, revision, label, patch, key)
		if err != nil {
			return err
		}
		w.Header().Set("ETag", etag(v.Revision))
		a.success(w, r, publicAccount(v))
		return nil
	case "ACC05":
		if r.ContentLength > 0 {
			return invalid("账号删除不接受请求体")
		}
		key, err := idempotencyKey(r)
		if err != nil {
			return err
		}
		if err = a.deps.Accounts.Delete(ctx, p, accountID, key); err != nil {
			return err
		}
		w.WriteHeader(204)
		return nil
	case "ACC08":
		q, err := a.deps.Accounts.Quota(ctx, p, accountID)
		if err != nil {
			return err
		}
		a.success(w, r, map[string]any{"total_bytes": uintString(q.Total), "used_bytes": uintString(q.Used), "free_bytes": uintString(q.Free), "collected_at": q.CollectedAt})
		return nil
	case "ACC09":
		caps, err := a.deps.Accounts.Capabilities(ctx, p, accountID, a.deps.System.Capabilities())
		if err != nil {
			return err
		}
		a.success(w, r, caps)
		return nil
	case "FIL01", "FIL02", "FIL04":
		q, err := queryValues(r, "path", "base_path", "context_id", "fields", "page", "page_size", "order_by", "direction", "keyword", "recursive", "cursor")
		if err != nil {
			return err
		}
		valid := map[string]bool{"path": true, "base_path": true, "context_id": true, "fields": true}
		if id == "FIL01" {
			for _, k := range []string{"page", "page_size", "order_by", "direction"} {
				valid[k] = true
			}
		}
		if id == "FIL04" {
			for _, k := range []string{"keyword", "recursive", "cursor", "page_size"} {
				valid[k] = true
			}
		}
		for k := range q {
			if !valid[k] {
				return invalid("此操作不接受该查询参数")
			}
		}
		if id == "FIL02" && q.Get("path") == "" {
			return invalid("缺少path")
		}
		remote, err := appfiles.Resolve(q.Get("path"), file.Scope{BasePath: q.Get("base_path"), ContextID: q.Get("context_id")})
		if err != nil {
			return err
		}
		projection, err := fields(q)
		if err != nil {
			return err
		}
		if id == "FIL02" {
			e, err := a.deps.Files.Meta(ctx, p, accountID, remote)
			if err != nil {
				return err
			}
			a.success(w, r, publicFile(e, projection))
			return nil
		}
		size, err := queryInt(q, "page_size", 100, 1, 1000)
		if err != nil {
			return err
		}
		if id == "FIL01" {
			page, err := queryInt(q, "page", 1, 1, 2147483647)
			if err != nil {
				return err
			}
			order, direction := q.Get("order_by"), q.Get("direction")
			if order == "" {
				order = "name"
			}
			if direction == "" {
				direction = "asc"
			}
			if order != "name" && order != "time" && order != "size" || direction != "asc" && direction != "desc" {
				return invalid("排序参数无效")
			}
			entries, err := a.deps.Files.List(ctx, p, accountID, remote, file.ListOptions{Page: page, PageSize: size, OrderBy: order, Direction: direction})
			if err != nil {
				return err
			}
			var more any
			if len(entries) < size {
				more = false
			}
			a.paged(w, r, filePage(entries, projection), map[string]any{"request_id": requestID, "pagination": map[string]any{"page": page, "page_size": size, "total": nil, "has_more": more, "consistency": "upstream_live"}})
			return nil
		}
		keyword := q.Get("keyword")
		if keyword == "" || utf8.RuneCountInString(keyword) > 1024 {
			return invalid("keyword无效")
		}
		recursive := q.Get("recursive") == "true"
		if q.Get("recursive") != "" && q.Get("recursive") != "true" && q.Get("recursive") != "false" {
			return invalid("recursive无效")
		}
		page, err := a.deps.Files.Search(ctx, p, accountID, remote, keyword, recursive, q.Get("cursor"), size, strings.Join(projection, ","))
		if err != nil {
			return err
		}
		entries := []file.Entry{}
		for _, raw := range page.Items {
			var e file.Entry
			if err = json.Unmarshal(raw, &e); err != nil {
				return err
			}
			entries = append(entries, e)
		}
		a.paged(w, r, filePage(entries, projection), cursorMeta(page, requestID))
		return nil
	case "FIL03", "FIL05", "FIL06":
		var input struct {
			Paths     []string   `json:"paths,omitempty"`
			Patterns  []string   `json:"patterns,omitempty"`
			BasePath  *string    `json:"base_path,omitempty"`
			ContextID *string    `json:"context_id,omitempty"`
			Options   *scanInput `json:"options,omitempty"`
		}
		if err := decodeRequest(w, r, &input); err != nil {
			return err
		}
		if input.BasePath != nil && input.ContextID != nil {
			return &httpError{400, "AMBIGUOUS_PATH_SCOPE", "base_path与context_id不能同时提供"}
		}
		if input.BasePath != nil && *input.BasePath == "" || input.ContextID != nil && *input.ContextID == "" {
			return invalid("路径作用域不能为空")
		}
		scope := file.Scope{BasePath: deref(input.BasePath, ""), ContextID: deref(input.ContextID, "")}
		values := input.Paths
		if id == "FIL06" {
			values = input.Patterns
		}
		if len(values) > 100 {
			return fault.New(fault.TooLarge, "LIMIT_EXCEEDED", "每次最多100项")
		}
		if len(values) == 0 {
			return invalid("缺少路径或通配符")
		}
		if id == "FIL06" && (input.Paths != nil || input.Options != nil) || id != "FIL06" && input.Patterns != nil || id == "FIL03" && input.Options != nil {
			return invalid("请求包含此操作不支持的字段")
		}
		paths := []string{}
		for _, v := range values {
			if v == "" {
				return invalid("路径不能为空")
			}
			resolved, err := appfiles.Resolve(v, scope)
			if err != nil {
				return err
			}
			paths = append(paths, resolved)
		}
		if id == "FIL05" {
			options, err := effectiveScan(input.Options)
			if err != nil {
				return err
			}
			key, err := idempotencyKey(r)
			if err != nil {
				return err
			}
			job, err := a.deps.Transfers.SubmitScan(ctx, p, accountID, file.ScanPlan{Paths: paths, Options: options}, key)
			if err != nil {
				return err
			}
			w.Header().Set("Location", "/api/v1/jobs/"+job.ID)
			a.respond(w, r, 202, publicJob(job, requestID))
			return nil
		}
		if id == "FIL03" {
			items := []any{}
			var succeeded, failed uint64
			for i, remote := range paths {
				e, err := a.deps.Files.Meta(ctx, p, accountID, remote)
				if err != nil {
					failed++
					items = append(items, map[string]any{"status": "failed", "input": values[i], "problem": publicProblem(fault.As(err), requestID)})
				} else {
					succeeded++
					items = append(items, map[string]any{"status": "succeeded", "input": values[i], "file": publicFile(e, []string{"all"})})
				}
			}
			a.success(w, r, map[string]any{"items": items, "complete": true, "summary": publicCounts(transfer.Counts{Total: uint64(len(items)), Processed: uint64(len(items)), Succeeded: succeeded, Failed: failed})})
			return nil
		}
		items := []any{}
		complete := true
		for i, pattern := range paths {
			matches, err := a.deps.Files.Match(ctx, p, accountID, pattern)
			var problem any
			if err != nil {
				complete = false
				problem = publicProblem(fault.As(err), requestID)
			}
			if matches == nil {
				matches = []string{}
			}
			items = append(items, map[string]any{"pattern": values[i], "paths": matches, "problem": problem})
		}
		a.success(w, r, map[string]any{"items": items, "complete": complete})
		return nil
	case "JOB01", "JOB03":
		allowed := []string{"cursor", "page_size"}
		if id == "JOB01" {
			allowed = append(allowed, "account_id", "kind", "state", "created_after", "created_before")
		}
		q, err := queryValues(r, allowed...)
		if err != nil {
			return err
		}
		size, err := queryInt(q, "page_size", 100, 1, 1000)
		if err != nil {
			return err
		}
		if id == "JOB03" {
			page, err := a.deps.Transfers.ItemPage(ctx, p, r.PathValue("job_id"), q.Get("cursor"), size, a.deps.Queries)
			if err != nil {
				return err
			}
			items := []any{}
			for _, b := range page.Items {
				var item transfer.Item
				if err = json.Unmarshal(b, &item); err != nil {
					return err
				}
				items = append(items, publicItem(item, requestID))
			}
			a.paged(w, r, items, cursorMeta(page, requestID))
			return nil
		}
		filter := transfer.Filter{AccountID: q.Get("account_id"), Kind: q.Get("kind"), State: q.Get("state"), After: q.Get("created_after"), Before: q.Get("created_before")}
		if filter.AccountID != "" && !configuration.ValidID(filter.AccountID) {
			return invalid("account_id格式无效")
		}
		if !jobFilterValid(filter) {
			return invalid("任务过滤参数无效")
		}
		page, err := a.deps.Queries.Page(ctx, []any{"jobs", p.ID, p.Scopes, p.Accounts, p.Roots, filter, size}, q.Get("cursor"), size, func() ([]json.RawMessage, error) {
			jobs, err := a.deps.Transfers.List(ctx, p, filter)
			if err != nil {
				return nil, err
			}
			return query.Encode(jobs)
		})
		if err != nil {
			return err
		}
		jobs := []any{}
		for _, b := range page.Items {
			var job transfer.Record
			if err = json.Unmarshal(b, &job); err != nil {
				return err
			}
			jobs = append(jobs, publicJob(job, requestID))
		}
		a.paged(w, r, jobs, cursorMeta(page, requestID))
		return nil
	case "JOB05":
		return a.serveEvents(w, r)
	case "JOB09":
		var input struct {
			Scope string `json:"scope,omitempty"`
		}
		if err := decodeObject(w, r, &input, true); err != nil {
			return err
		}
		if input.Scope == "" {
			input.Scope = "failed"
		}
		if input.Scope != "failed" && input.Scope != "failed_and_cancelled" {
			return invalid("重试scope无效")
		}
		key, err := idempotencyKey(r)
		if err != nil {
			return err
		}
		job, err := a.deps.Transfers.Retry(ctx, p, r.PathValue("job_id"), input.Scope, key)
		if err != nil {
			return err
		}
		w.Header().Set("Location", "/api/v1/jobs/"+job.ID)
		a.respond(w, r, 202, publicJob(job, requestID))
		return nil
	case "JOB11":
		all, err := a.deps.Artifacts.List(ctx, p, r.PathValue("job_id"))
		if err != nil {
			return err
		}
		items := []any{}
		for _, v := range all {
			items = append(items, publicArtifact(v))
		}
		a.success(w, r, items)
		return nil
	case "LOC01":
		roots, err := a.deps.Local.Roots(ctx, p)
		if err != nil {
			return err
		}
		items := []any{}
		for _, v := range roots {
			var space any
			if v.Available != nil {
				space = uintString(*v.Available)
			}
			items = append(items, map[string]any{"root_id": v.ID, "label": v.Label, "readable": v.Readable, "writable": v.Writable, "available_bytes": space, "state": v.State})
		}
		a.success(w, r, items)
		return nil
	case "ART01":
		v, err := a.deps.Artifacts.Get(ctx, p, r.PathValue("artifact_id"))
		if err != nil {
			return err
		}
		a.success(w, r, publicArtifact(v))
		return nil
	case "ART02", "ART03":
		return a.serveArtifactContent(w, r)
	case "ART04":
		if r.ContentLength > 0 {
			return invalid("结果文件删除不接受请求体")
		}
		key, err := idempotencyKey(r)
		if err != nil {
			return err
		}
		if err = a.deps.Artifacts.Delete(ctx, p, r.PathValue("artifact_id"), key); err != nil {
			return err
		}
		w.WriteHeader(204)
		return nil
	}
	return invalid("未知P3操作")
}

type scanInput struct {
	Mode     *string `json:"mode,omitempty"`
	Depth    *int    `json:"depth,omitempty"`
	IDs      *bool   `json:"include_ids,omitempty"`
	Nodes    *string `json:"max_nodes,omitempty"`
	Duration *int    `json:"max_duration_seconds,omitempty"`
}

func effectiveScan(input *scanInput) (file.ScanOptions, error) {
	if input == nil {
		input = &scanInput{}
	}
	o := file.ScanOptions{Mode: deref(input.Mode, "recursive"), Depth: deref(input.Depth, -1), IncludeIDs: deref(input.IDs, true), MaxNodes: deref(input.Nodes, "100000"), MaxDuration: deref(input.Duration, 3600)}
	n, err := decimal(&o.MaxNodes, true)
	if err != nil || n == nil {
		return o, invalid("max_nodes无效")
	}
	if *n > 1000000 {
		return o, fault.New(fault.TooLarge, "LIMIT_EXCEEDED", "扫描最多1000000个节点")
	}
	if o.Mode != "tree" && o.Mode != "recursive" && o.Mode != "statistics" || o.Depth < -1 || o.Depth > 1000 || o.MaxDuration < 1 || o.MaxDuration > 86400 {
		return o, invalid("扫描选项无效")
	}
	return o, nil
}
func jobFilterValid(f transfer.Filter) bool {
	kinds := "|upload|download|file_scan|mkdir|file_delete|file_copy|file_move|file_rename|share_cancel|share_transfer|offline_add|offline_cancel|offline_delete|offline_clear|recycle_restore|recycle_delete|recycle_clear|checksum_fix|local_checksum|file_verification|export|local_encryption|local_decryption|"
	states := "|queued|running|pausing|paused|cancelling|cancelled|succeeded|partially_succeeded|failed|interrupted|"
	if f.Kind != "" && !strings.Contains(kinds, "|"+f.Kind+"|") || f.State != "" && !strings.Contains(states, "|"+f.State+"|") {
		return false
	}
	for _, v := range []string{f.After, f.Before} {
		if v != "" {
			if _, err := time.Parse(time.RFC3339Nano, v); err != nil {
				return false
			}
		}
	}
	return true
}
func publicCounts(c transfer.Counts) map[string]string {
	return map[string]string{"total": uintString(c.Total), "processed": uintString(c.Processed), "succeeded": uintString(c.Succeeded), "failed": uintString(c.Failed), "skipped": uintString(c.Skipped), "cancelled": uintString(c.Cancelled)}
}

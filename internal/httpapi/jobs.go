package httpapi

import (
	"encoding/json"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/file"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/tool"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/transfer"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/write"
)

func uintString(n uint64) string { return strconv.FormatUint(n, 10) }
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
func publicProblem(f *fault.Error, requestID string) any {
	if f == nil {
		return nil
	}
	status := 502
	switch f.Kind {
	case fault.Invalid:
		status = 400
	case fault.Forbidden:
		status = 403
	case fault.NotFound:
		status = 404
	case fault.TooLarge:
		status = 413
	case fault.Gone:
		status = 410
	case fault.Precondition:
		status = 412
	case fault.RateLimited:
		status = 429
	case fault.Conflict:
		status = 409
	case fault.Unsupported:
		status = 422
	case fault.Unavailable:
		status = 503
	case fault.Timeout:
		status = 504
	case fault.Internal:
		status = 500
	}
	return map[string]any{"type": "urn:baidupcs:error:" + strings.ToLower(strings.ReplaceAll(f.Code, "_", "-")), "title": f.Message, "status": status, "code": f.Code, "request_id": requestID, "retryable": f.Retryable}
}

type publicCheckpoint struct {
	Phase        string
	Size, Offset int64
	Skipped      bool
	Upload       *struct {
		BlockList []struct {
			Range    struct{ Begin, End int64 }
			CheckSum string `json:"checksum"`
		} `json:"block_list"`
	}
}

func checkpoint(r transfer.Record) publicCheckpoint {
	var cp publicCheckpoint
	_ = json.Unmarshal(r.Checkpoint, &cp)
	return cp
}
func counters(r transfer.Record) map[string]string {
	if r.Request.WritePlan != "" {
		if r.ScanResult != nil {
			return publicCounts(r.ScanResult.Counts)
		}
		var plan write.Plan
		_ = json.Unmarshal([]byte(r.Request.WritePlan), &plan)
		return publicCounts(transfer.Counts{Total: uint64(len(plan.Units))})
	}

	if r.Request.ToolPlan != "" || r.Request.Kind == "file_scan" || len(r.Plan) > 0 {
		if r.ScanResult != nil {
			return publicCounts(r.ScanResult.Counts)
		}
		return publicCounts(transfer.Counts{})
	}
	values := map[string]string{"total": "1", "processed": "0", "succeeded": "0", "failed": "0", "skipped": "0", "cancelled": "0"}
	if r.Terminal() {
		values["processed"] = "1"
		switch r.State {
		case "succeeded":
			if checkpoint(r).Skipped {
				values["skipped"] = "1"
			} else {
				values["succeeded"] = "1"
			}
		case "failed":
			values["failed"] = "1"
		case "cancelled":
			values["cancelled"] = "1"
		}
	}
	return values
}
func bytesConfirmed(r transfer.Record) (string, string) {
	if len(r.Plan) > 0 && r.ScanResult != nil {
		return strconv.FormatInt(r.ScanResult.Transferred, 10), strconv.FormatInt(r.ScanResult.Written, 10)
	}

	cp := checkpoint(r)
	transferred := cp.Offset
	if r.Request.Kind == "upload" {
		transferred = 0
		if cp.Upload != nil {
			for _, b := range cp.Upload.BlockList {
				if b.CheckSum != "" {
					transferred += b.Range.End - b.Range.Begin
				}
			}
		}
		if cp.Phase == "done" && !cp.Skipped {
			transferred = cp.Size
		}
	}
	written := int64(0)
	if r.Request.Kind == "download" {
		written = transferred
	}
	return strconv.FormatInt(max(transferred, 0), 10), strconv.FormatInt(max(written, 0), 10)
}
func publicJob(r transfer.Record, requestID string) any {
	cp, counts := checkpoint(r), counters(r)
	phase := cp.Phase
	if phase == "" {
		phase = "pending"
	}
	var totalBytes any
	if cp.Size > 0 {
		totalBytes = strconv.FormatInt(cp.Size, 10)
	}
	transferred, written := bytesConfirmed(r)
	rate := "0"
	var eta any
	if r.State == "running" || r.State == "cancelling" {
		if started, err := time.Parse(time.RFC3339Nano, r.StartedAt); err == nil {
			elapsed := time.Since(started).Seconds()
			if elapsed > 0 {
				confirmed, _ := strconv.ParseInt(transferred, 10, 64)
				perSecond := int64(float64(confirmed) / elapsed)
				rate = strconv.FormatInt(perSecond, 10)
				if perSecond > 0 && cp.Size >= confirmed {
					eta = strconv.FormatInt((cp.Size-confirmed+perSecond-1)/perSecond, 10)
				}
			}
		}
	}
	actions := []string{}
	if !r.Terminal() {
		switch r.State {
		case "queued":
			if cp.Phase != "committing" && cp.Phase != "done" {
				actions = append(actions, "cancel")
			}
		case "running":
			actions = append(actions, "cancel")
			if r.Request.Kind == "upload" || r.Request.Kind == "download" {
				actions = append(actions, "pause")
			}
		case "paused":
			actions = append(actions, "resume", "cancel")
		case "interrupted":
			if cp.Phase != "committing" && cp.Phase != "done" {
				actions = append(actions, "cancel")
			}
			actions = append(actions, "resume")

		}
	}
	if r.FailureCode == "CREDENTIAL_REVOKED" {
		filtered := []string{}
		for _, action := range actions {
			if action != "resume" && action != "reconcile" {
				filtered = append(filtered, action)
			}
		}
		actions = filtered
	}
	var problem any
	if r.FailureCode != "" {
		problem = publicProblem(jobFault(r.FailureCode), requestID)
	}
	result := map[string]any{
		"job_id": r.ID, "owner": r.Owner, "account_id": r.Request.AccountID, "kind": r.Request.Kind, "state": r.State, "phase": phase, "created_at": r.CreatedAt, "dry_run": false, "started_at": nullable(r.StartedAt), "finished_at": nullable(r.FinishedAt), "config_revision": uintString(r.ConfigRevision), "credential_revision": uintString(r.CredentialRevision), "parent_job_id": nullable(r.Request.ParentJobID), "child_job_ids": []string{}, "retry_of_job_id": nullable(r.Request.RetryOf),
		"resolved_paths": []string{r.Request.RemotePath}, "resolved_sources": []any{}, "local_files": []any{}, "destination": nil, "target_path": nil,
		"progress":          map[string]any{"total_items": counts["total"], "processed_items": counts["processed"], "succeeded_items": counts["succeeded"], "failed_items": counts["failed"], "skipped_items": counts["skipped"], "cancelled_items": counts["cancelled"], "total_bytes": totalBytes, "transferred_bytes": transferred, "written_bytes": written, "bytes_per_second": rate, "eta_seconds": eta},
		"available_actions": actions, "problem": problem, "links": map[string]string{"self": "/api/v1/jobs/" + r.ID, "events": "/api/v1/jobs/" + r.ID + "/events", "result": "/api/v1/jobs/" + r.ID + "/result"}, "effective_options": r.EffectiveOptions,
	}
	if r.Terminal() && (counts["failed"] != "0" || counts["cancelled"] != "0" || r.State == "failed" && r.FailureCode != "") {
		result["available_actions"] = []string{"retry"}
	}
	if r.Request.ToolPlan != "" {
		var plan tool.Plan
		_ = json.Unmarshal([]byte(r.Request.ToolPlan), &plan)
		paths, locals := []string{}, []any{}
		for _, u := range plan.Units {
			if u.Path != "" {
				paths = append(paths, u.Path)
			}
			if u.Local.RootID != "" {
				locals = append(locals, u.Local)
			}
		}
		result["resolved_paths"] = paths
		result["local_files"] = locals
		result["destination"] = plan.Destination
		if r.Request.AccountID == "" {
			result["account_id"] = nil
			result["credential_revision"] = nil
		}
		actions := []string{}
		if !r.Terminal() && r.State != "cancelling" && cp.Phase != "committing" && cp.Phase != "done" {
			actions = append(actions, "cancel")
		}
		if r.Terminal() && (counts["failed"] != "0" || counts["cancelled"] != "0") && r.Request.Kind != "local_encryption" && r.Request.Kind != "local_decryption" {
			actions = append(actions, "retry")
		}
		result["available_actions"] = actions
		return result
	}

	if r.Request.WritePlan != "" {
		var plan write.Plan
		raw := r.Plan
		if len(raw) == 0 {
			raw = []byte(r.Request.WritePlan)
		}
		_ = json.Unmarshal(raw, &plan)
		paths := []string{}
		for _, u := range plan.Units {
			target := u.To
			if target == "" {
				target = u.From
			}
			if strings.HasPrefix(target, "/") {
				paths = append(paths, target)
			}
		}
		result["resolved_paths"] = paths
		result["target_path"] = nullable(r.Request.RemotePath)
		result["progress"].(map[string]any)["total_items"] = uintString(uint64(len(plan.Units)))
		if r.ScanResult != nil {
			result["child_job_ids"] = append([]string{}, r.ScanResult.ChildJobIDs...)
		}
		controls := []string{}
		if r.State == "interrupted" {
			controls = append(controls, "reconcile")
			if cp.Phase != "committing" && cp.Phase != "done" {
				controls = append(controls, "cancel")
			}
		} else if r.State == "queued" || r.State == "running" {
			controls = append(controls, "cancel")
		} else if r.Terminal() && (counts["failed"] != "0" || counts["cancelled"] != "0") {
			controls = append(controls, "retry")
		}
		result["available_actions"] = controls
		return result
	}
	if len(r.Plan) > 0 {
		var plan transfer.Plan
		_ = json.Unmarshal(r.Plan, &plan)
		result["dry_run"] = plan.DryRun
		paths := []string{}
		sources := []any{}
		locals := []any{}
		var size int64
		for _, u := range plan.Units {
			paths = append(paths, u.RemotePath)
			if !u.Directory {
				size += u.Size
			}
			if r.Request.Kind == "download" {
				locals = append(locals, map[string]string{"root_id": u.RootID, "relative_path": u.RelativePath})
			}
		}
		if r.Request.Batch != "" {
			var batch transfer.Batch
			_ = json.Unmarshal([]byte(r.Request.Batch), &batch)
			for _, s := range batch.Sources {
				sources = append(sources, s)
			}
		}
		result["resolved_paths"] = paths
		result["resolved_sources"] = sources
		result["local_files"] = locals
		progress := result["progress"].(map[string]any)
		progress["total_items"] = uintString(uint64(len(plan.Units)))
		progress["total_bytes"] = strconv.FormatInt(size, 10)
		if r.Request.Kind == "download" {
			result["destination"] = map[string]string{"root_id": r.Request.RootID, "directory": path.Dir(r.Request.RelativePath)}
		} else {
			result["target_path"] = r.Request.RemotePath
		}
		return result
	}
	if r.Request.Kind == "file_scan" {
		var plan file.ScanPlan
		_ = json.Unmarshal([]byte(r.Request.ScanPlan), &plan)
		result["resolved_paths"] = plan.Paths
		progress := result["progress"].(map[string]any)
		progress["total_items"] = nil
		progress["total_bytes"] = nil
		if r.Terminal() && r.ScanResult != nil {
			progress["total_items"] = counts["total"]
		}
		result["available_actions"] = []string{}
		if !r.Terminal() && r.State != "cancelling" {
			result["available_actions"] = []string{"cancel"}
		}
		if r.Terminal() && (counts["failed"] != "0" || counts["cancelled"] != "0" || r.State == "failed" && r.FailureCode != "") {
			result["available_actions"] = []string{"retry"}
		}
		return result
	}
	if r.Request.Kind == "upload" {
		result["target_path"] = r.Request.RemotePath
		result["resolved_sources"] = []any{map[string]string{"type": "local", "root_id": r.Request.RootID, "relative_path": r.Request.RelativePath}}
	} else {
		result["destination"] = map[string]string{"root_id": r.Request.RootID, "directory": path.Dir(r.Request.RelativePath)}
		result["local_files"] = []any{map[string]string{"root_id": r.Request.RootID, "relative_path": r.Request.RelativePath}}
	}
	return result
}
func jobFault(code string) *fault.Error {
	kind := fault.Internal
	switch code {
	case "REMOTE_RESULT_UNKNOWN", "LOCAL_RESULT_UNKNOWN", "TARGET_EXISTS", "CHECKSUM_MISMATCH", "CREDENTIAL_REVISION_MISMATCH":
		kind = fault.Conflict
	case "TEMPORARY_SECRET_LOST", "CHECKPOINT_SOURCE_CHANGED", "CHECKPOINT_BINDING_MISMATCH", "CHECKPOINT_VERSION_UNSUPPORTED", "CHECKSUM_UNAVAILABLE", "UNSUPPORTED_OPTION", "BAIDU_CREDENTIAL_EXPIRED", "HTTPS_REQUIRED", "CREDENTIAL_REVOKED", "BAIDU_COOKIE_REQUIRED", "BAIDU_CREDENTIAL_MISSING":
		kind = fault.Unsupported
	case "LOCAL_PATH_FORBIDDEN":
		kind = fault.Forbidden
	case "REMOTE_FILE_NOT_FOUND", "LOCAL_FILE_NOT_FOUND":
		kind = fault.NotFound
	case "UPSTREAM_TIMEOUT":
		kind = fault.Timeout
	case "LOCAL_STORAGE_FULL":
		kind = fault.Unavailable
	case "UPSTREAM_ERROR", "UPSTREAM_PROTOCOL_ERROR":
		kind = fault.BadGateway
	}
	return fault.New(kind, code, "任务执行未完成；根据业务码核对或恢复")
}
func publicResult(r transfer.Record, requestIDs ...string) any {
	requestID := "req_result"
	if len(requestIDs) > 0 {
		requestID = requestIDs[0]
	}
	if r.Request.ToolPlan != "" {
		v := r.ScanResult
		if v == nil {
			v = &transfer.ScanResult{}
		}
		var result any
		switch r.Request.Kind {
		case "export":
			var p tool.Plan
			_ = json.Unmarshal([]byte(r.Request.ToolPlan), &p)
			var o struct {
				Format string `json:"format"`
			}
			_ = json.Unmarshal(p.Options, &o)
			result = map[string]any{"summary": publicCounts(v.Counts), "format": o.Format, "artifact_ids": append([]string{}, v.ArtifactIDs...), "complete": v.Complete, "missing_fields": append([]string{}, v.MissingFields...)}
		case "local_checksum", "file_verification":
			items := []any{}
			for _, i := range v.IntegrityItems {
				items = append(items, map[string]any{"input": i.Input, "checksum": i.Checksum, "matches": i.Matches, "problem": publicProblem(i.Failure, requestID)})
			}
			result = map[string]any{"summary": publicCounts(v.Counts), "items": items, "complete": v.Complete, "artifact_ids": append([]string{}, v.ArtifactIDs...)}
		case "checksum_fix":
			items := []any{}
			for _, i := range v.MutationItems {
				items = append(items, map[string]any{"input": i.Input, "state": i.State, "path": nullable(i.Path), "fs_id": nil, "offline_task_id": nil, "problem": publicProblem(i.Failure, requestID), "remote_effect": i.RemoteEffect})
			}
			result = map[string]any{"summary": publicCounts(v.Counts), "items": items, "complete": v.Complete, "affected_count": uintString(v.Affected)}
		default:
			result = map[string]any{"summary": publicCounts(v.Counts), "complete": v.Complete, "artifact_ids": []string{}, "transferred_bytes": "0", "written_bytes": strconv.FormatInt(v.Written, 10), "child_job_ids": []string{}}
		}
		return map[string]any{"job_id": r.ID, "kind": r.Request.Kind, "result": result}
	}

	if r.Request.WritePlan != "" {
		v := r.ScanResult
		if v == nil {
			v = &transfer.ScanResult{}
		}
		if r.Request.Kind == "share_transfer" {
			return map[string]any{"job_id": r.ID, "kind": r.Request.Kind, "result": map[string]any{"summary": publicCounts(v.Counts), "complete": r.Terminal() && v.Counts.Total > 0 && v.Counts.Processed == v.Counts.Total, "artifact_ids": []string{}, "transferred_bytes": "0", "written_bytes": "0", "child_job_ids": append([]string{}, v.ChildJobIDs...)}}
		}
		items := []any{}
		for _, item := range v.MutationItems {
			var problem any
			if item.Failure != nil {
				problem = publicProblem(item.Failure, requestID)
			}
			items = append(items, map[string]any{"input": item.Input, "state": item.State, "path": nullable(item.Path), "fs_id": nullable(item.ResultID), "offline_task_id": nullable(item.OfflineID), "problem": problem, "remote_effect": item.RemoteEffect})
		}
		return map[string]any{"job_id": r.ID, "kind": r.Request.Kind, "result": map[string]any{"summary": publicCounts(v.Counts), "items": items, "complete": r.Terminal() && v.Counts.Total > 0 && v.Counts.Processed == v.Counts.Total, "affected_count": uintString(v.Affected)}}
	}

	if r.Request.Kind == "file_scan" {
		v := r.ScanResult
		if v == nil {
			v = &transfer.ScanResult{ArtifactIDs: []string{}}
		}
		return map[string]any{"job_id": r.ID, "kind": r.Request.Kind, "result": map[string]any{"summary": publicCounts(v.Counts), "complete": v.Complete, "truncation_reason": nullable(v.Truncation), "files": uintString(v.Files), "directories": uintString(v.Directories), "size_bytes": uintString(v.Bytes), "artifact_ids": append([]string{}, v.ArtifactIDs...)}}
	}
	transferred, written := bytesConfirmed(r)
	return map[string]any{"job_id": r.ID, "kind": r.Request.Kind, "result": map[string]any{"summary": counters(r), "complete": r.Terminal() && (len(r.Plan) == 0 || r.ScanResult != nil && r.ScanResult.Counts.Processed == r.ScanResult.Counts.Total), "artifact_ids": []string{}, "transferred_bytes": transferred, "written_bytes": written, "child_job_ids": []string{}}}
}

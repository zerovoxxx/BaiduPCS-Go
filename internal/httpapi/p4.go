package httpapi

import (
	"encoding/json"
	"net/http"
	"path"
	"strconv"

	appfiles "github.com/qjfoidnh/BaiduPCS-Go/internal/application/files"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/file"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/transfer"
)

func (a *API) submitTransfer(id string, w http.ResponseWriter, r *http.Request) error {
	req := transfer.Request{AccountID: r.PathValue("account_id"), Public: true}
	b := transfer.Batch{}
	if id == "UPL01" {
		var in uploadInput
		if err := decodeRequest(w, r, &in); err != nil {
			return err
		}
		if len(in.Sources) == 0 || len(in.Sources) > 100 || in.Target == nil {
			return invalid("缺少上传源或target_dir")
		}
		remote, err := appfiles.Resolve(*in.Target, file.Scope{BasePath: deref(in.BasePath, ""), ContextID: deref(in.ContextID, "")})
		if err != nil {
			return err
		}
		b.DryRun = in.DryRun
		for _, s := range in.Sources {
			source := transfer.Source{Type: s.Type}
			switch s.Type {
			case "local":
				if s.RootID == nil || s.RelativePath == nil || s.StagedFileID != nil || !configuration.ValidID(*s.RootID) || !configuration.ValidRelative(*s.RelativePath) {
					return invalid("本地上传源无效")
				}
				source.RootID, source.RelativePath = *s.RootID, *s.RelativePath
			case "staged":
				if s.StagedFileID == nil || s.RootID != nil || s.RelativePath != nil || !configuration.ValidID(*s.StagedFileID) {
					return invalid("暂存上传源无效")
				}
				source.StagedFileID = *s.StagedFileID
			default:
				return invalid("上传源类型无效")
			}
			b.Sources = append(b.Sources, source)
		}
		raw, _ := json.Marshal(in.Options)
		req.Options = string(raw)
		req.Kind = "upload"
		req.RemotePath = remote
		req.RootID = b.Sources[0].RootID
		req.RelativePath = b.Sources[0].RelativePath
	} else {
		var in downloadInput
		if err := decodeRequest(w, r, &in); err != nil {
			return err
		}
		if in.Selector == nil || in.Destination == nil || in.Destination.RootID == nil || in.Destination.Directory == nil || !configuration.ValidID(*in.Destination.RootID) || !configuration.ValidRelative(*in.Destination.Directory) {
			return invalid("下载选择或目标目录无效")
		}
		if len(in.Selector.Values) == 0 || len(in.Selector.Values) > 100 {
			return invalid("下载选择数量无效")
		}
		for _, v := range in.Selector.Values {
			if in.Selector.Kind == "fs_ids" {
				id, err := strconv.ParseInt(v, 10, 64)
				if err != nil || id <= 0 {
					return invalid("fs_id无效")
				}
				paths, err := a.deps.Content.ResolveIDs(r.Context(), principal(r.Context()), req.AccountID, []int64{id})
				if err != nil {
					return err
				}
				b.Paths = append(b.Paths, paths...)
				continue
			}
			remote, err := appfiles.Resolve(v, file.Scope{BasePath: deref(in.BasePath, ""), ContextID: deref(in.ContextID, "")})
			if err != nil {
				return err
			}
			if in.Selector.Kind == "patterns" {
				paths, err := a.deps.Files.Match(r.Context(), principal(r.Context()), req.AccountID, remote)
				if err != nil {
					return err
				}
				b.Paths = append(b.Paths, paths...)
			} else if in.Selector.Kind == "paths" {
				b.Paths = append(b.Paths, remote)
			} else {
				return invalid("选择器类型无效")
			}
		}
		if len(b.Paths) == 0 {
			return fault.New(fault.NotFound, "REMOTE_FILE_NOT_FOUND", "没有匹配文件")
		}
		req.Kind = "download"
		req.RootID = *in.Destination.RootID
		req.RelativePath = path.Join(*in.Destination.Directory, path.Base(b.Paths[0]))
		req.RemotePath = b.Paths[0]
		b.Directory = *in.Destination.Directory
		b.DryRun = in.DryRun
		raw, _ := json.Marshal(in.Options)
		req.Options = string(raw)
	}
	raw, _ := json.Marshal(b)
	req.Batch = string(raw)
	// Preserve the P2 canonical request and idempotency hash for its one-file
	// representation. The planner still expands a directory at that same path.
	if !b.DryRun && len(b.Sources) == 1 && b.Sources[0].Type == "local" && req.RelativePath != "" && req.RelativePath != "." {
		req.Batch = ""
		req.RemotePath = path.Join(req.RemotePath, path.Base(req.RelativePath))
	}
	if !b.DryRun && len(b.Paths) == 1 && derefDownloadPathMode(req.Options) == "relative" {
		req.Batch = ""
	}

	key, err := idempotencyKey(r)
	if err != nil {
		return err
	}
	job, err := a.deps.Transfers.Submit(r.Context(), principal(r.Context()), req, key)
	if err != nil {
		return err
	}
	w.Header().Set("Location", "/api/v1/jobs/"+job.ID)
	a.respond(w, r, 202, publicJob(job, RequestID(r.Context())))
	return nil
}

func derefDownloadPathMode(raw string) string {
	var options struct {
		PathMode *string `json:"path_mode"`
	}
	_ = json.Unmarshal([]byte(raw), &options)
	return deref(options.PathMode, "relative")
}

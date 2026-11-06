package transfers

import (
	"encoding/json"
	"strconv"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/transfer"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/write"
)

type UploadOptions struct {
	FileConcurrency *int    `json:"file_concurrency,omitempty"`
	Parallel        *int    `json:"parallel_per_file,omitempty"`
	Retries         *int    `json:"max_retries,omitempty"`
	Policy          *string `json:"policy,omitempty"`
	SkipRapid       *bool   `json:"skip_rapid_check,omitempty"`
	CheckFilename   *bool   `json:"check_filename,omitempty"`
	NoSplit         *bool   `json:"no_split_file,omitempty"`
	Rate            *string `json:"max_rate_bytes_per_second,omitempty"`
}
type DownloadOptions struct {
	Mode            *string `json:"mode,omitempty"`
	PathMode        *string `json:"path_mode,omitempty"`
	Parallel        *int    `json:"parallel,omitempty"`
	FileConcurrency *int    `json:"file_concurrency,omitempty"`
	Retries         *int    `json:"max_retries,omitempty"`
	Overwrite       *bool   `json:"overwrite,omitempty"`
	Verify          *bool   `json:"verify_checksum,omitempty"`
	Mtime           *bool   `json:"preserve_mtime,omitempty"`
	Executable      *bool   `json:"executable,omitempty"`
	LinkIndex       *int    `json:"link_index,omitempty"`
	Test            *bool   `json:"test,omitempty"`
	Report          *bool   `json:"report_workers,omitempty"`
	Rate            *string `json:"max_rate_bytes_per_second,omitempty"`
}

func value[T any](p *T, fallback T) T {
	if p != nil {
		return *p
	}
	return fallback
}
func optionError() error {
	return fault.New(fault.Unsupported, "UNSUPPORTED_OPTION", "所选历史选项尚无有效实现；请查询capabilities")
}
func resolveOptions(r *transfer.Record) error {
	if write.Operation(r.Request.Kind) != "" {
		var p write.Plan
		if json.Unmarshal([]byte(r.Request.WritePlan), &p) != nil {
			return fault.New(fault.Invalid, "INVALID_REQUEST", "写计划无效")
		}
		o := p.Options
		effective := map[string]any{}
		switch r.Request.Kind {
		case "file_copy", "file_move", "file_rename":
			if o.Policy == "" {
				o.Policy = "fail"
			}
			if o.Policy != "fail" && o.Policy != "skip" && o.Policy != "overwrite" {
				return fault.New(fault.Invalid, "INVALID_REQUEST", "冲突策略无效")
			}
			effective["conflict_policy"] = o.Policy
		case "file_delete":
			effective["ignore_missing"] = o.IgnoreMissing
		case "share_transfer":
			if o.Randomize {
				return optionError()
			}
			effective = map[string]any{"collect": o.Collect, "randomize_name": false, "download_after": o.Download}
			if o.Download {
				if o.Destination == nil {
					return fault.New(fault.Invalid, "INVALID_REQUEST", "下载子任务缺少目标")
				}
				child := *r
				child.Request.Kind = "download"
				child.Request.Options = string(o.DownloadOptions)
				child.Request.Public = true
				if err := resolveOptions(&child); err != nil {
					return err
				}
				effective["download_destination"] = o.Destination
				effective["download_options"] = child.EffectiveOptions
			} else if o.Destination != nil || len(o.DownloadOptions) > 0 {
				return fault.New(fault.Invalid, "INVALID_REQUEST", "未请求下载，不能提供下载选项")
			}
		}
		r.EffectiveOptions, _ = json.Marshal(effective)
		return nil
	}

	if !r.Request.Public {
		if r.Request.Kind == "upload" {
			r.EffectiveOptions = json.RawMessage(`{"file_concurrency":1,"parallel_per_file":1,"max_retries":0,"policy":"skip","skip_rapid_check":true,"check_filename":false,"no_split_file":false,"max_rate_bytes_per_second":"0"}`)
		} else {
			r.EffectiveOptions = json.RawMessage(`{"mode":"pcs","path_mode":"relative","parallel":1,"file_concurrency":1,"max_retries":0,"overwrite":false,"verify_checksum":true,"preserve_mtime":false,"executable":false,"link_index":0,"test":false,"report_workers":false,"max_rate_bytes_per_second":"0"}`)
		}
		return nil
	}
	raw := r.Request.Options
	if raw == "" {
		raw = "{}"
	}
	bad := func() error { return fault.New(fault.Invalid, "INVALID_REQUEST", "传输选项格式或范围无效") }
	var effective map[string]any
	if r.Request.Kind == "upload" {
		var o UploadOptions
		if json.Unmarshal([]byte(raw), &o) != nil {
			return bad()
		}
		concurrency, parallel, retries := value(o.FileConcurrency, r.Config.MaxUploadLoad), value(o.Parallel, r.Config.MaxUploadParallel), value(o.Retries, 3)
		policy, rapid, check, split, rate := value(o.Policy, r.Config.UploadPolicy), value(o.SkipRapid, false), value(o.CheckFilename, !r.Config.IgnoreIllegal), value(o.NoSplit, false), value(o.Rate, strconv.FormatInt(r.Config.MaxUploadRate, 10))
		if concurrency < 1 || concurrency > 256 || parallel < 1 || parallel > 256 || retries < 0 || retries > 100 || policy != "skip" && policy != "overwrite" && policy != "rsync" || !decimal(rate) {
			return bad()
		}
		if split {
			return optionError()
		}
		if concurrency > 16 || parallel > 32 || concurrency*parallel > 256 {
			return fault.New(fault.Unsupported, "TRANSFER_BUDGET_EXCEEDED", "文件并发最多16、单文件线程最多32、总线程最多256")
		}
		r.Config.MaxUploadRate, _ = strconv.ParseInt(rate, 10, 64)
		effective = map[string]any{"file_concurrency": concurrency, "parallel_per_file": parallel, "max_retries": retries, "policy": policy, "skip_rapid_check": rapid, "check_filename": check, "no_split_file": split, "max_rate_bytes_per_second": rate}
	} else {
		var o DownloadOptions
		if json.Unmarshal([]byte(raw), &o) != nil {
			return bad()
		}
		mode, pathMode := value(o.Mode, "locate"), value(o.PathMode, "relative")
		parallel, concurrency, retries, index := value(o.Parallel, r.Config.MaxParallel), value(o.FileConcurrency, r.Config.MaxDownloadLoad), value(o.Retries, 3), value(o.LinkIndex, 0)
		overwrite, verify, mtime, executable, test, report, rate := value(o.Overwrite, false), value(o.Verify, !r.Config.NoCheck), value(o.Mtime, false), value(o.Executable, false), value(o.Test, false), value(o.Report, false), value(o.Rate, strconv.FormatInt(r.Config.MaxDownloadRate, 10))
		if parallel < 1 || parallel > 256 || concurrency < 1 || concurrency > 256 || retries < 0 || retries > 100 || index < 0 || index > 99 || mode != "locate" && mode != "pcs" && mode != "stream" || pathMode != "relative" && pathMode != "full" || !decimal(rate) {
			return bad()
		}
		if concurrency > 16 || parallel > 32 || concurrency*parallel > 256 {
			return fault.New(fault.Unsupported, "TRANSFER_BUDGET_EXCEEDED", "文件并发最多16、单文件线程最多32、总线程最多256")
		}
		r.Config.MaxDownloadRate, _ = strconv.ParseInt(rate, 10, 64)
		effective = map[string]any{"mode": mode, "path_mode": pathMode, "parallel": parallel, "file_concurrency": concurrency, "max_retries": retries, "overwrite": overwrite, "verify_checksum": verify, "preserve_mtime": mtime, "executable": executable, "link_index": index, "test": test, "report_workers": report, "max_rate_bytes_per_second": rate}
	}
	r.EffectiveOptions, _ = json.Marshal(effective)
	return nil
}
func decimal(s string) bool {
	if s == "" || len(s) > 20 || len(s) > 1 && s[0] == '0' {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	_, err := strconv.ParseInt(s, 10, 64)
	return err == nil
}

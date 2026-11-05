package bootstrap

import (
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/system"
	"strings"
)

func configureP4Capabilities(operations []system.Operation) {
	for i := range operations {
		o := &operations[i]
		if o.ID != "UPL01" && o.ID != "DWN01" && o.ID != "UPL02" && o.ID != "UPL03" && o.ID != "UPL04" && o.ID != "DWN02" && o.ID != "DWN03" && o.ID != "DWN04" && o.ID != "JOB07" && o.ID != "JOB08" {
			continue
		}
		if strings.HasPrefix(o.ID, "DWN") || o.ID == "UPL01" {
			o.RequiredCredentials = []string{"bduss"}
		}
		o.SupportedValues = map[string][]string{}
		o.Limits = nil
		add := func(n, v, u string) {
			x := v
			o.Limits = append(o.Limits, system.Limit{Name: n, Value: &x, Unit: u, Source: "service"})
		}
		if o.ID == "UPL01" || o.ID == "DWN01" {
			add("max_files_per_request", "100", "count")
			add("max_plan_items", "10000", "count")
			add("min_file_size", "0", "bytes")
			add("max_file_size", "137438953472", "bytes")
			add("file_concurrency_max", "16", "count")
			add("parallel_per_file_max", "32", "count")
			add("thread_budget_max", "256", "count")
			add("running_jobs", "2", "count")
			add("waiting_jobs", "16", "count")
		}
		if o.ID == "UPL01" {
			o.SupportedOptions = []string{"file_concurrency", "parallel_per_file", "max_retries", "policy", "skip_rapid_check", "check_filename", "max_rate_bytes_per_second", "dry_run"}
			o.SupportedValues = map[string][]string{"policy": {"skip", "overwrite", "rsync"}, "no_split_file": {"false"}, "source_type": {"local", "staged"}}
		}
		if o.ID == "DWN01" {
			o.SupportedOptions = []string{"mode", "path_mode", "parallel", "file_concurrency", "max_retries", "overwrite", "verify_checksum", "preserve_mtime", "executable", "link_index", "test", "report_workers", "max_rate_bytes_per_second", "dry_run"}
			o.SupportedValues = map[string][]string{"mode": {"pcs", "locate", "stream"}, "path_mode": {"relative", "full"}, "selector_kind": {"paths", "fs_ids", "patterns"}}
		}
		if o.ID == "UPL02" || o.ID == "UPL03" || o.ID == "UPL04" {
			add("max_file_size", "268435456", "bytes")
			add("total_quota", "1073741824", "bytes")
			add("max_files", "128", "count")
			add("unreferenced_ttl", "86400", "seconds")
		}
	}
}

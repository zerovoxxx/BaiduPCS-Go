package bootstrap

import "github.com/qjfoidnh/BaiduPCS-Go/internal/domain/system"

func configureP5Capabilities(operations []system.Operation) {
	for i := range operations {
		o := &operations[i]
		switch o.ID {
		case "SHR01", "SHR02", "SHR03", "SHR04", "TRF01", "TRF02", "OFF01", "OFF02", "OFF03", "OFF04", "OFF05", "OFF06", "OFF07", "REC01", "REC02", "REC03", "REC04":
			o.RequiredCredentials = []string{"bduss"}
		}
		add := func(name, value, unit string) {
			v := value
			o.Limits = append(o.Limits, system.Limit{Name: name, Value: &v, Unit: unit, Source: "service"})
		}
		switch o.ID {
		case "FIL09", "FIL10", "FIL11":
			o.SupportedOptions = []string{"conflict_policy", "base_path"}
			o.SupportedValues = map[string][]string{"conflict_policy": {"fail", "skip", "overwrite"}}
		case "SHR01":
			o.SupportedOptions = []string{"password", "period_days", "combined_link", "base_path"}
			add("period_days_max", "2147483647", "count")
		case "TRF01":
			o.SupportedOptions = []string{"input", "password", "inspection_id", "directory_fs_id", "cursor", "page_size"}
			add("inspection_ttl", "600", "seconds")
			add("inspection_max_entries", "10000", "count")
		case "TRF02":
			o.SupportedOptions = []string{"collect", "fs_ids", "download_after", "download_destination", "download_options"}
			o.SupportedValues = map[string][]string{"randomize_name": {"false"}, "super_transfer": {"unsupported"}}
		case "OFF02":
			add("upstream_window_max", "1000", "count")
		case "OFF03":
			add("query_ids_max", "100", "count")
		case "REC01":
			add("upstream_page_size", "100", "count")
		case "JOB10":
			o.SupportedOptions = []string{"dry_run"}
		}
	}
}

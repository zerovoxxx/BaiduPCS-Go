package bootstrap

import (
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/system"
	"strings"
)

func configureP6Capabilities(operations []system.Operation, legacyConfigured, serviceConfigured bool) {
	for i := range operations {
		op := &operations[i]
		if op.ID == "CFG09" && !legacyConfigured {
			op.Availability = "disabled"
			op.Reason = "本实例未配置固定legacy_accounts_file；不写用户日常CLI配置"
		}
		if op.ID == "CFG06" && !legacyConfigured && !serviceConfigured {
			op.Availability = "disabled"
			op.Reason = "本实例未配置固定重载来源"
		}
		if strings.HasPrefix(op.ID, "LOG") {
			op.Availability = "unverified"
			op.Reason = "HTTP密码和二维码流程已受控验收，百度交互登录须单独授权与验证"
		}
		add := func(name, value, unit string) {
			v := value
			op.Limits = append(op.Limits, system.Limit{Name: name, Value: &v, Unit: unit, Source: "service"})
		}
		if strings.HasPrefix(op.ID, "INT") || op.ID == "EXP01" || op.ID == "SYS06" {
			op.RequiredCredentials = []string{"bduss"}
		}
		switch op.ID {
		case "FIL01", "FIL02", "FIL03", "FIL04", "FIL05", "FIL06", "UPL01", "DWN01", "DWN02", "DWN03", "DWN04", "DWN05", "FIL07", "FIL08", "FIL09", "FIL10", "FIL11", "SHR01", "TRF02", "OFF01", "INT01", "INT02", "INT04", "INT06", "EXP01":
			op.SupportedOptions = append(op.SupportedOptions, "context_id", "base_path")
		}
		switch op.ID {
		case "INT03", "INT04":
			op.SupportedOptions = append(op.SupportedOptions, "algorithms")
		case "LOC02", "LOC03":
			op.SupportedOptions = []string{"method", "gzip", "remove_source"}
			add("temporary_key_jobs_max", "36", "count")
		case "EXP01":
			op.SupportedOptions = append(op.SupportedOptions, "recursive", "root_path", "format", "max_retries", "destination")
			add("nodes_max", "100000", "count")
			add("duration_seconds_max", "3600", "seconds")
		case "CFG06":
			op.SupportedOptions = []string{"source", "dry_run"}
		case "CFG09":
			op.SupportedOptions = []string{"default_account_id", "accounts"}
		case "CTX01", "CTX02", "CTX03", "CTX04":
			add("context_ttl", "86400", "seconds")
		case "LOG01", "LOG02", "LOG03", "LOG04", "LOG05", "LOG06":
			add("session_ttl", "300", "seconds")
			add("session_max", "128", "count")
		case "SYS05":
			op.SupportedOptions = []string{"active_probe"}
			add("probe_timeout", "5", "seconds")
		case "UPL05", "UPL06", "UPL07", "UPL08", "UPL09":
			add("session_ttl", "86400", "seconds")
			add("chunk_max_bytes", "33554432", "bytes")
			add("file_max_bytes", "268435456", "bytes")
			add("staging_total_max_bytes", "1073741824", "bytes")
			add("staging_files_max", "128", "count")
		}
	}
}

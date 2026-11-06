package transfers

import (
	"encoding/json"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/transfer"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/infrastructure/baidu"
	"testing"
)

func TestExplicitZeroAndInverseOptionsOverrideConfiguration(t *testing.T) {
	cfg := baidu.DefaultConfiguration()
	cfg.NoCheck = true
	cfg.IgnoreIllegal = true
	cfg.MaxDownloadRate = 4096
	cfg.MaxUploadRate = 2048
	for _, tc := range []struct{ kind, options, field string }{{"download", `{"verify_checksum":true,"max_retries":0,"max_rate_bytes_per_second":"0"}`, "verify_checksum"}, {"upload", `{"check_filename":true,"max_retries":0,"max_rate_bytes_per_second":"0"}`, "check_filename"}} {
		r := transfer.Record{Config: cfg, Request: transfer.Request{Kind: tc.kind, Public: true, Options: tc.options}}
		if e := resolveOptions(&r); e != nil {
			t.Fatal(e)
		}
		var values map[string]any
		json.Unmarshal(r.EffectiveOptions, &values)
		if values[tc.field] != true || values["max_retries"] != float64(0) || values["max_rate_bytes_per_second"] != "0" {
			t.Fatal("explicit values lost", values)
		}
		if cfg.MaxDownloadRate != 4096 || cfg.MaxUploadRate != 2048 {
			t.Fatal("caller config mutated")
		}
		r.Request.Options = "{}"
		r.Config = cfg
		if e := resolveOptions(&r); e != nil {
			t.Fatal(e)
		}
		json.Unmarshal(r.EffectiveOptions, &values)
		if values[tc.field] != false || values["max_retries"] != float64(3) {
			t.Fatal("omission did not inherit", values)
		}
	}
}

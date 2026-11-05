package bootstrap

import (
	"context"
	"encoding/json"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/application/files"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/account"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/system"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/transfer"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/infrastructure/baidu"
	"strings"
)

type p3Engine struct {
	transfer *baidu.SingleFileEngine
	scanner  *files.Scanner
}

func (e p3Engine) Prepare(ctx context.Context, r transfer.Record, c account.Credentials) (string, error) {
	if r.Request.Kind == "file_scan" {
		if c.Cookies["BDUSS"] == "" {
			return "", fault.New(fault.Unsupported, "BAIDU_COOKIE_REQUIRED", "文件扫描需要BDUSS")
		}
		return "scan/" + r.ID, nil
	}
	return e.transfer.Prepare(ctx, r, c)
}
func (e p3Engine) Execute(ctx context.Context, r transfer.Record, c account.Credentials, save func(json.RawMessage) error) error {
	if r.Request.Kind == "file_scan" {
		return e.scanner.Execute(ctx, r, c, save)
	}
	return e.transfer.Execute(ctx, r, c, save)
}
func (e p3Engine) CleanupTransfer(ctx context.Context, r transfer.Record) error {
	if r.Request.Kind == "file_scan" {
		return nil
	}
	return e.transfer.CleanupTransfer(ctx, r)
}
func configureP3Capabilities(operations []system.Operation) {
	for i := range operations {
		op := &operations[i]
		if strings.HasPrefix(op.ID, "FIL") || op.ID == "UPL01" || op.ID == "DWN01" {
			op.RequiredCredentials = []string{"bduss"}
		}
		if op.ID == "ACC07" || op.ID == "ACC08" {
			op.CredentialAlternatives = [][]string{{"bduss"}, {"access_token"}}
		}
		add := func(name, value, unit string) {
			v := value
			op.Limits = append(op.Limits, system.Limit{Name: name, Value: &v, Unit: unit, Source: "service"})
		}
		switch op.ID {
		case "FIL01":
			op.SupportedOptions = []string{"page", "page_size", "order_by", "direction", "fields", "base_path"}
			add("page_size_max", "1000", "count")
		case "FIL04", "JOB01":
			add("snapshot_ttl", "600", "seconds")
			add("snapshot_max_items", "10000", "count")
		case "JOB03":
			add("cursor_ttl", "600", "seconds")
			add("page_size_max", "1000", "count")
		case "JOB09":
			add("retry_items_max", "10000", "count")
		case "FIL05":
			op.SupportedOptions = []string{"mode", "depth", "include_ids", "max_nodes", "max_duration_seconds"}
			add("max_nodes", "1000000", "count")
			add("max_duration_seconds", "86400", "seconds")
		case "JOB05":
			add("event_retention_max", "10000", "count")
			add("event_retention_seconds", "86400", "seconds")
			add("connection_queue_max", "128", "count")
			add("connections_max", "64", "count")
			add("heartbeat_seconds", "15", "seconds")
		case "ART01", "ART02", "ART03", "ART04":
			add("artifact_ttl", "86400", "seconds")
			add("artifact_max_bytes", "268435456", "bytes")
			add("artifact_total_max_bytes", "1073741824", "bytes")
		case "ACC01":
			op.SupportedOptions = []string{"force_login_username"}
		}
	}
}

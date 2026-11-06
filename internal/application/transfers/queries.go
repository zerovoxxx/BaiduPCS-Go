package transfers

import (
	"context"
	"encoding/json"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/application/query"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/auth"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/file"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/tool"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/transfer"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/write"
)

func (s *Service) SubmitScan(ctx context.Context, p auth.Principal, id string, plan file.ScanPlan, key string) (transfer.Record, error) {
	if len(plan.Paths) == 0 {
		return transfer.Record{}, fault.New(fault.Invalid, "INVALID_REQUEST", "扫描路径不能为空")
	}
	unique := make([]string, 0, len(plan.Paths))
	seen := map[string]bool{}
	for _, p := range plan.Paths {
		if !seen[p] {
			unique = append(unique, p)
			seen[p] = true
		}
	}
	plan.Paths = unique
	encoded, err := json.Marshal(plan)
	if err != nil {
		return transfer.Record{}, err
	}
	return s.Submit(ctx, p, transfer.Request{Kind: "file_scan", AccountID: id, RemotePath: plan.Paths[0], ScanPlan: string(encoded), Public: true}, key)
}
func (s *Service) List(ctx context.Context, p auth.Principal, f transfer.Filter) ([]transfer.Record, error) {
	if f.AccountID != "" && !p.AllowsAccount(f.AccountID) {
		return nil, fault.New(fault.Forbidden, "FORBIDDEN", "无权访问此账号")
	}
	if !p.Has("admin") {
		f.Owner = p.ID
	}
	all, err := s.repo.ListTransfers(ctx, f, 10001, func(r transfer.Record) bool { return permission(p, r) == nil })
	if err != nil {
		return nil, err
	}
	out := []transfer.Record{}
	for _, r := range all {
		if permission(p, r) == nil {
			out = append(out, r)
		}
	}
	if len(all) > 10000 {
		return nil, fault.New(fault.TooLarge, "QUERY_SNAPSHOT_LIMIT", "任务查询超过10000项，请收紧过滤条件")
	}
	return out, nil
}
func (s *Service) Items(ctx context.Context, p auth.Principal, id string) ([]transfer.Item, error) {
	r, err := s.Get(ctx, p, id)
	if err != nil {
		return nil, err
	}
	if r.Request.ToolPlan != "" || r.Request.Kind == "file_scan" || len(r.Plan) > 0 || r.Request.WritePlan != "" {
		return s.repo.TransferItems(ctx, id, 10001)
	}
	// Transfers currently execute one unit. Its public summary is projected from
	// the durable checkpoint without exposing uploadid, filenames or URLs.
	return []transfer.Item{TransferItem(r)}, nil
}
func (s *Service) ItemPage(ctx context.Context, p auth.Principal, id, cursor string, size int, q *query.Service) (query.Page, error) {
	r, err := s.Get(ctx, p, id)
	if err != nil {
		return query.Page{}, err
	}
	if r.Request.ToolPlan != "" || r.Request.Kind == "file_scan" || len(r.Plan) > 0 || r.Request.WritePlan != "" {
		return s.repo.TransferItemPage(ctx, id, p.ID, cursor, size)
	}
	return q.Page(ctx, []any{"items", p.ID, id, size}, cursor, size, func() ([]json.RawMessage, error) {
		items, err := s.Items(ctx, p, id)
		if err != nil {
			return nil, err
		}
		return query.Encode(items)
	})
}
func (s *Service) Retry(ctx context.Context, p auth.Principal, id, scope, key string) (transfer.Record, error) {
	r, err := s.Get(ctx, p, id)
	if err != nil {
		return r, err
	}
	if !r.Terminal() {
		return r, fault.New(fault.Conflict, "JOB_NOT_FINISHED", "重试需要原任务处于终态")
	}
	req := r.Request
	if req.ToolPlan != "" {
		if req.Kind == "local_encryption" || req.Kind == "local_decryption" {
			return r, fault.New(fault.Unsupported, "SECRET_RESUBMISSION_REQUIRED", "加解密重试需要重新提交密钥")
		}
		if r.State != "failed" && r.State != "partially_succeeded" && !(scope == "failed_and_cancelled" && r.State == "cancelled") {
			return r, fault.New(fault.Conflict, "NO_RETRYABLE_ITEMS", "没有可重试项")
		}
		var plan tool.Plan
		if err := json.Unmarshal([]byte(req.ToolPlan), &plan); err != nil {
			return r, err
		}
		items, err := s.repo.RetryTransferItems(ctx, id, scope == "failed_and_cancelled")
		if err != nil {
			return r, err
		}
		units := []tool.Unit{}
		for _, item := range items {
			if item.Index < uint64(len(plan.Units)) {
				units = append(units, plan.Units[item.Index])
			}
		}
		if len(units) > 0 {
			plan.Units = units
		} else if r.ScanResult != nil && r.ScanResult.Counts.Processed > 0 {
			return r, fault.New(fault.Conflict, "NO_RETRYABLE_ITEMS", "没有符合范围的重试项")
		}
		raw, _ := json.Marshal(plan)
		req.ToolPlan = string(raw)
		req.RetryOf = id
		req.RetryScope = scope
		return s.Submit(ctx, p, req, key)
	}
	req.RetryOf = id
	req.RetryScope = scope
	if write.Operation(r.Request.Kind) != "" {
		items, err := s.repo.RetryTransferItems(ctx, id, scope == "failed_and_cancelled")
		if err != nil {
			return r, err
		}
		selected := map[uint64]bool{}
		for _, item := range items {
			selected[item.Index] = true
		}
		var plan write.Plan
		if err = json.Unmarshal(r.Plan, &plan); err != nil {
			return r, err
		}
		units := []write.Unit{}
		for i, u := range plan.Units {
			if selected[uint64(i)] {
				u.Before = nil
				u.TargetBefore = nil
				u.PreparationError = nil
				u.PriorIDs = nil
				units = append(units, u)
			}
		}
		if len(units) == 0 {
			return r, fault.New(fault.Conflict, "NO_RETRYABLE_ITEMS", "没有符合范围的重试项")
		}
		plan.Units = units
		raw, _ := json.Marshal(plan)
		req.WritePlan = string(raw)
		req.Reconcile = false
		req.ReconcileDry = false
		return s.Submit(ctx, p, req, key)
	}
	if len(r.Plan) > 0 {
		items, err := s.repo.RetryTransferItems(ctx, id, scope == "failed_and_cancelled")
		if err != nil {
			return r, err
		}
		selected := map[uint64]bool{}
		for _, item := range items {
			selected[item.Index] = true
		}
		var plan transfer.Plan
		_ = json.Unmarshal(r.Plan, &plan)
		b := transfer.Batch{}
		for i, u := range plan.Units {
			if !selected[uint64(i)] {
				continue
			}
			u.SHA256 = ""
			u.Mtime = 0
			u.Size = 0
			b.RetryUnits = append(b.RetryUnits, u)
		}
		if len(b.RetryUnits) == 0 {
			return r, fault.New(fault.Conflict, "NO_RETRYABLE_ITEMS", "没有符合范围的重试项")
		}
		var original transfer.Batch
		_ = json.Unmarshal([]byte(req.Batch), &original)
		b.Directory = original.Directory
		raw, _ := json.Marshal(b)
		req.Batch = string(raw)
		return s.Submit(ctx, p, req, key)
	}
	if r.Request.Kind == "file_scan" {
		items, err := s.repo.RetryTransferItems(ctx, id, scope == "failed_and_cancelled")
		if err != nil {
			return r, err
		}
		paths := []string{}
		seen := map[string]bool{}
		for _, v := range items {
			if v.State == "failed" || scope == "failed_and_cancelled" && v.State == "cancelled" {
				if !seen[v.Path] {
					paths = append(paths, v.Path)
					seen[v.Path] = true
				}
			}
		}
		if len(paths) == 0 && r.State == "failed" && r.FailureCode != "" {
			// A preparation/output failure can fail a read-only scan before it creates
			// item rows. The failed selection is then the original request itself.
			var original file.ScanPlan
			if err = json.Unmarshal([]byte(req.ScanPlan), &original); err != nil {
				return r, err
			}
			paths = original.Paths
		}
		if len(paths) == 0 {
			return r, fault.New(fault.Conflict, "NO_RETRYABLE_ITEMS", "没有符合重试范围的项目")
		}
		var plan file.ScanPlan
		if err = json.Unmarshal([]byte(req.ScanPlan), &plan); err != nil {
			return r, err
		}
		plan.Paths = paths
		b, _ := json.Marshal(plan)
		req.ScanPlan = string(b)
		req.RemotePath = paths[0]
	} else if r.State != "failed" && !(scope == "failed_and_cancelled" && r.State == "cancelled") {
		return r, fault.New(fault.Conflict, "NO_RETRYABLE_ITEMS", "没有符合重试范围的项目")
	}
	return s.Submit(ctx, p, req, key)
}
func (s *Service) RevokeAccount(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for job, cancel := range s.running {
		r, err := s.repo.Transfer(ctx, job)
		if err != nil {
			return err
		}
		if r.Request.AccountID != id || r.Terminal() {
			continue
		}
		if _, err = s.accounts.CredentialVersion(ctx, id, r.CredentialRevision); err == nil {
			continue
		} else if fault.As(err).Code != "CREDENTIAL_REVOKED" {
			return err
		}
		cancel()
	}
	s.notify()
	return nil
}
func (s *Service) EventSnapshot(ctx context.Context, p auth.Principal, id string) (transfer.Record, uint64, error) {
	if _, err := s.Get(ctx, p, id); err != nil {
		return transfer.Record{}, 0, err
	}
	return s.repo.EventSnapshot(ctx, id)
}
func (s *Service) Events(ctx context.Context, p auth.Principal, id string, after uint64, limit int) (transfer.EventBatch, error) {
	if _, err := s.Get(ctx, p, id); err != nil {
		return transfer.EventBatch{}, err
	}
	return s.repo.TransferEvents(ctx, id, after, limit)
}
func TransferItem(r transfer.Record) transfer.Item {
	var cp struct {
		Workers      []transfer.Worker
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
	_ = json.Unmarshal(r.Checkpoint, &cp)
	state := r.State
	if state == "queued" {
		state = "pending"
	}
	if state == "cancelling" {
		state = "running"
	}
	if state == "paused" || state == "pausing" {
		state = "interrupted"
		for i := range cp.Workers {
			cp.Workers[i].State = "paused"
		}
	}
	if state == "succeeded" && cp.Skipped {
		state = "skipped"
	}
	item := transfer.Item{Workers: cp.Workers, ID: "item_0", Path: r.Request.RemotePath, RootID: r.Request.RootID, RelativePath: r.Request.RelativePath, State: state, CheckpointAvailable: len(r.Checkpoint) > 0}
	if r.StartedAt != "" {
		item.Attempts = 1
	}
	if cp.Size > 0 {
		item.Size = &cp.Size
	}
	offset := cp.Offset
	if r.Request.Kind == "upload" {
		offset = 0
		if cp.Upload != nil {
			for _, b := range cp.Upload.BlockList {
				if b.CheckSum != "" {
					offset += b.Range.End - b.Range.Begin
				}
			}
		}
		if cp.Phase == "done" && !cp.Skipped {
			offset = cp.Size
		}
	}
	item.Transferred = max(offset, 0)
	if r.Request.Kind == "download" {
		item.Written = item.Transferred
	}
	if len(r.Checkpoint) > 0 {
		item.Offset = &item.Transferred
	}
	if r.FailureCode != "" {
		item.Failure = fault.New(fault.Internal, r.FailureCode, "任务执行失败")
	}
	return item
}

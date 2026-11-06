package baidu

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/account"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/transfer"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/infrastructure/localfs"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/ports"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/uploader"
)

func (e *SingleFileEngine) SetStaging(s *localfs.Staging)               { e.staging = s }
func (e *SingleFileEngine) SetRepository(repo ports.TransferRepository) { e.repo = repo }
func (e *SingleFileEngine) Plan(ctx context.Context, r transfer.Record, c account.Credentials) (json.RawMessage, error) {
	var b transfer.Batch
	if r.Request.Batch == "" {
		if r.Request.Kind == "upload" {
			isDirectory := false
			stop := fmt.Errorf("inspection done")
			err := e.roots.Walk(ctx, r.Request.RootID, r.Request.RelativePath, func(_ string, info os.FileInfo) error { isDirectory = info.IsDir(); return stop })
			if err != stop {
				return nil, upstreamFault(err)
			}
			if !isDirectory {
				return nil, nil
			}
			b.Sources = []transfer.Source{{Type: "local", RootID: r.Request.RootID, RelativePath: r.Request.RelativePath}}
			r.Request.RemotePath = path.Dir(r.Request.RemotePath)
		} else {
			s, err := e.clients.New(ctx, configuration.NewSnapshot(r.ConfigRevision, r.Config), c)
			if err != nil {
				return nil, err
			}
			meta, metaErr := s.PCS.FilesDirectoriesMeta(r.Request.RemotePath)
			s.Close()
			if metaErr != nil {
				return nil, upstreamFault(metaErr)
			}
			if !meta.Isdir {
				if _, err = e.roots.File(ctx, r.Request.RootID, r.Request.RelativePath, true); err != nil {
					if validation := e.roots.ValidateDestination(ctx, configuration.Directory{RootID: r.Request.RootID, Directory: path.Dir(r.Request.RelativePath)}); validation != nil {
						return nil, fault.New(fault.Forbidden, "LOCAL_PATH_FORBIDDEN", "下载目录不可访问")
					}
					return json.Marshal(transfer.Plan{Units: []transfer.Unit{{RootID: r.Request.RootID, RelativePath: r.Request.RelativePath, RemotePath: r.Request.RemotePath, Size: meta.Size, Mtime: meta.Mtime}}})
				}
				return nil, nil
			}
			b.Paths = []string{r.Request.RemotePath}
			b.Directory = path.Dir(r.Request.RelativePath)
		}
	} else if json.Unmarshal([]byte(r.Request.Batch), &b) != nil {
		return nil, unsupported("INVALID_REQUEST", "传输选择无效")
	}
	if len(b.RetryUnits) > 0 {
		if len(b.RetryUnits) > 10000 {
			return nil, fault.New(fault.TooLarge, "TRANSFER_PLAN_LIMIT", "重试项目过多")
		}
		plan := transfer.Plan{Units: []transfer.Unit{}}
		for _, u := range b.RetryUnits {
			if r.Request.Kind == "upload" && !u.Directory {
				roots := e.roots
				if u.StagedFileID != "" {
					meta, err := e.staging.Metadata(ctx, u.StagedFileID)
					if err != nil {
						return nil, err
					}
					if meta.Owner != r.Owner {
						return nil, fault.New(fault.Forbidden, "FORBIDDEN", "暂存源所有权不符")
					}
					roots = e.staging.Registry()
				}
				f, err := roots.OpenFile(ctx, u.RootID, u.RelativePath, os.O_RDONLY, 0)
				if err != nil {
					return nil, upstreamFault(err)
				}
				u.Size, u.Mtime, u.SHA256, _, err = sourceFingerprint(f, ctx)
				f.Close()
				if err != nil {
					return nil, err
				}
			}
			plan.Units = append(plan.Units, u)
		}
		return json.Marshal(plan)
	}
	p := transfer.Plan{DryRun: b.DryRun, Units: []transfer.Unit{}}
	appendUnit := func(u transfer.Unit) error {
		if len(p.Units) >= 10000 {
			return fault.New(fault.TooLarge, "TRANSFER_PLAN_LIMIT", "传输计划最多10000项")
		}
		p.Units = append(p.Units, u)
		return nil
	}
	if r.Request.Kind == "upload" {
		if len(b.Sources) == 0 || len(b.Sources) > 100 {
			return nil, unsupported("INVALID_REQUEST", "上传源数量无效")
		}
		for _, source := range b.Sources {
			if source.Type == "staged" {
				if e.staging == nil {
					return nil, fault.New(fault.Unavailable, "TRANSFER_NOT_READY", "暂存服务不可用")
				}
				meta, err := e.staging.Metadata(ctx, source.StagedFileID)
				if err != nil {
					return nil, err
				}
				if meta.Owner != r.Owner {
					return nil, fault.New(fault.Forbidden, "FORBIDDEN", "暂存源属于其他调用方")
				}
				f, err := e.staging.Registry().OpenFile(ctx, "staging", meta.ID, os.O_RDONLY, 0)
				if err != nil {
					return nil, err
				}
				size, mtime, sha, _, err := sourceFingerprint(f, ctx)
				f.Close()
				if err != nil {
					return nil, err
				}
				if err = appendUnit(transfer.Unit{RootID: "staging", RelativePath: meta.ID, RemotePath: path.Join(r.Request.RemotePath, meta.Filename), StagedFileID: meta.ID, Size: size, Mtime: mtime, SHA256: sha}); err != nil {
					return nil, err
				}
				continue
			}
			if source.Type != "local" {
				return nil, unsupported("INVALID_REQUEST", "上传源类型无效")
			}
			if err := e.roots.Walk(ctx, source.RootID, source.RelativePath, func(rel string, info os.FileInfo) error {
				suffix := strings.TrimPrefix(strings.TrimPrefix(rel, source.RelativePath), "/")
				target := path.Join(r.Request.RemotePath, path.Base(source.RelativePath), suffix)
				if source.RelativePath == "." || source.RelativePath == "" {
					target = path.Join(r.Request.RemotePath, suffix)
				}
				u := transfer.Unit{RootID: source.RootID, RelativePath: rel, RemotePath: target, Directory: info.IsDir(), Size: info.Size(), Mtime: info.ModTime().UnixNano()}
				if !info.IsDir() {
					f, err := e.roots.OpenFile(ctx, source.RootID, rel, os.O_RDONLY, 0)
					if err != nil {
						return err
					}
					_, _, u.SHA256, _, err = sourceFingerprint(f, ctx)
					f.Close()
					if err != nil {
						return err
					}
				}
				return appendUnit(u)
			}); err != nil {
				return nil, upstreamFault(err)
			}
		}
	} else {
		if err := e.roots.ValidateDestination(ctx, configuration.Directory{RootID: r.Request.RootID, Directory: b.Directory}); err != nil {
			return nil, fault.New(fault.Forbidden, "LOCAL_PATH_FORBIDDEN", "下载目录不可访问")
		}
		s, err := e.clients.New(ctx, configuration.NewSnapshot(r.ConfigRevision, r.Config), c)
		if err != nil {
			return nil, err
		}
		defer s.Close()
		paths := append([]string{}, b.Paths...)
		sort.Strings(paths)
		roots := []string{}
		for _, v := range paths {
			overlap := false
			for _, parent := range roots {
				if under(parent, v) {
					overlap = true
					break
				}
			}
			if !overlap {
				roots = append(roots, v)
			}
		}
		if len(roots) == 0 {
			return nil, fault.New(fault.Invalid, "INVALID_REQUEST", "下载选择不能为空")
		}
		ancestor := path.Dir(roots[0])
		for _, v := range roots[1:] {
			for !under(ancestor, path.Dir(v)) {
				ancestor = path.Dir(ancestor)
			}
		}
		opts := downloadSettings(r)
		var walk func(string) error
		walk = func(remote string) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			meta, metaErr := s.PCS.FilesDirectoriesMeta(remote)
			if metaErr != nil {
				return upstreamFault(metaErr)
			}
			var err error
			suffix := strings.TrimPrefix(strings.TrimPrefix(remote, ancestor), "/")
			if opts.PathMode == "full" {
				suffix = strings.TrimPrefix(remote, "/")
			}
			rel := path.Join(b.Directory, suffix)
			if err = appendUnit(transfer.Unit{RootID: r.Request.RootID, RelativePath: rel, RemotePath: remote, Directory: meta.Isdir, Size: meta.Size, Mtime: meta.Mtime}); err != nil {
				return err
			}
			if !meta.Isdir {
				return nil
			}
			seen := map[string]bool{}
			for page := 1; ; page++ {
				children, listErr := s.PCS.FilesDirectoriesListPage(remote, baidupcs.DefaultOrderOptions, 1000, page)
				if listErr != nil {
					return upstreamFault(listErr)
				}
				for _, child := range children {
					if path.Dir(child.Path) != remote || seen[child.Path] {
						return fault.New(fault.BadGateway, "UPSTREAM_PROTOCOL_ERROR", "目录分页重复或越界")
					}
					seen[child.Path] = true
					if err = walk(child.Path); err != nil {
						return err
					}
				}
				if len(children) < 1000 {
					break
				}
			}
			return nil
		}
		for _, v := range roots {
			if err = walk(v); err != nil {
				return nil, err
			}
		}
	}
	seen := map[string]bool{}
	unique := p.Units[:0]
	for _, u := range p.Units {
		key := u.RemotePath
		if r.Request.Kind == "download" {
			key = u.RootID + ":" + u.RelativePath
		}
		if seen[key] {
			return nil, fault.New(fault.Conflict, "TARGET_EXISTS", "多个源映射到相同目标")
		}
		seen[key] = true
		unique = append(unique, u)
	}
	p.Units = unique
	return json.Marshal(p)
}

type planCheckpoint struct {
	Version        int
	Binding, Phase string
	Units          map[string]json.RawMessage
}

func (e *SingleFileEngine) executePlan(ctx context.Context, r transfer.Record, c account.Credentials, save func(json.RawMessage) error) error {
	if e.repo == nil {
		return fault.New(fault.Unavailable, "TRANSFER_NOT_READY", "缺少任务结果存储")
	}
	var plan transfer.Plan
	if json.Unmarshal(r.Plan, &plan) != nil || len(plan.Units) > 10000 {
		return unsupported("CHECKPOINT_BINDING_MISMATCH", "传输计划无效")
	}
	cp := planCheckpoint{Version: 2, Binding: binding(r), Phase: "executing", Units: map[string]json.RawMessage{}}
	if len(r.Checkpoint) > 0 {
		if json.Unmarshal(r.Checkpoint, &cp) != nil || cp.Version != 2 || cp.Binding != binding(r) {
			return unsupported("CHECKPOINT_BINDING_MISMATCH", "目录检查点不属于本任务")
		}
	}
	var mu sync.Mutex
	persist := func() error {
		encoded, err := json.Marshal(cp)
		if err != nil {
			return err
		}
		return save(encoded)
	}
	old, err := e.repo.TransferItems(ctx, r.ID, 10001)
	if err != nil {
		return err
	}
	done := map[uint64]bool{}
	result := transfer.ScanResult{Complete: true, ArtifactIDs: []string{}}
	result.Counts.Total = uint64(len(plan.Units))
	count := func(item transfer.Item) {
		result.Counts.Processed++
		result.Transferred += item.Transferred
		result.Written += item.Written
		switch item.State {
		case "succeeded":
			result.Counts.Succeeded++
		case "skipped":
			result.Counts.Skipped++
		case "failed":
			result.Counts.Failed++
		case "cancelled":
			result.Counts.Cancelled++
		}
	}
	for _, item := range old {
		done[item.Index] = true
		count(item)
	}
	flushContext := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(context.Background(), 5*time.Second)
	}
	complete := func(i int, item transfer.Item) error {
		mu.Lock()
		defer mu.Unlock()
		count(item)
		f, stop := flushContext()
		defer stop()
		if err := e.repo.SaveScanItem(f, r.ID, item, result); err != nil {
			return err
		}
		delete(cp.Units, fmt.Sprint(i))
		cp.Phase = "executing"
		for _, raw := range cp.Units {
			var unitCP fileCheckpoint
			_ = json.Unmarshal(raw, &unitCP)
			if unitCP.Phase == "committing" || unitCP.Phase == "done" {
				cp.Phase = "committing"
			}
		}
		return persist()
	}
	// A completed unit is an immutable result. Remaining units retain their exact
	// local/remote fingerprints; neither resume nor retry reuses another job's id.
	run := func(i int) error {
		u := plan.Units[i]
		unitEngine := e
		if u.StagedFileID != "" {
			copyEngine := *e
			copyEngine.roots = e.staging.Registry()
			unitEngine = &copyEngine
		}
		item := transfer.Item{ID: fmt.Sprintf("item_%d", i), Index: uint64(i), RootID: u.RootID, RelativePath: u.RelativePath, Path: u.RemotePath, State: "succeeded", Attempts: 1, Size: &u.Size}
		if u.StagedFileID != "" {
			item.RootID = ""
			item.RelativePath = ""
		}
		child := r.Clone()
		child.ID = fmt.Sprintf("%s_u%d", r.ID, i)
		child.Plan = nil
		child.Request.Batch = ""
		child.Request.RootID = u.RootID
		child.Request.RelativePath = u.RelativePath
		child.Request.RemotePath = u.RemotePath
		mu.Lock()
		child.Checkpoint = append(json.RawMessage(nil), cp.Units[fmt.Sprint(i)]...)
		mu.Unlock()
		var runErr error
		if plan.DryRun {
			item.State = "skipped"
		} else if u.Directory {
			if r.Request.Kind == "download" {
				if !downloadSettings(r).Test {
					runErr = e.roots.MkdirAll(ctx, u.RootID, u.RelativePath)
				}
			} else {
				s, err := e.clients.New(ctx, configuration.NewSnapshot(r.ConfigRevision, r.Config), c)
				if err != nil {
					runErr = err
				} else {
					defer s.Close()
					meta, err := s.PCS.FilesDirectoriesMeta(u.RemotePath)
					if err == nil && !meta.Isdir {
						runErr = fault.New(fault.Conflict, "TARGET_EXISTS", "目录目标是普通文件")
					} else if err != nil {
						if fault.As(upstreamFault(err)).Kind != fault.NotFound {
							runErr = err
						} else {
							runErr = s.PCS.Mkdir(u.RemotePath)
						}
					}
				}
			}
		} else {
			if r.Request.Kind == "download" {
				runErr = e.roots.MkdirAll(ctx, u.RootID, path.Dir(u.RelativePath))
			} else {
				f, err := unitEngine.roots.OpenFile(ctx, u.RootID, u.RelativePath, os.O_RDONLY, 0)
				if err != nil {
					runErr = err
				} else {
					size, mtime, sha, _, err := sourceFingerprint(f, ctx)
					f.Close()
					if err != nil {
						runErr = err
					} else if size != u.Size || mtime != u.Mtime || sha != u.SHA256 {
						runErr = unsupported("CHECKPOINT_SOURCE_CHANGED", "上传源与固定计划不一致")
					}
				}
			}
			if runErr == nil {
				runErr = unitEngine.Execute(ctx, child, c, func(raw json.RawMessage) error {
					mu.Lock()
					defer mu.Unlock()
					cp.Units[fmt.Sprint(i)] = append(json.RawMessage(nil), raw...)
					cp.Phase = "executing"
					for _, b := range cp.Units {
						var p struct{ Phase string }
						_ = json.Unmarshal(b, &p)
						if p.Phase == "committing" || p.Phase == "done" {
							cp.Phase = "committing"
						}
					}
					return persist()
				})
			}
			mu.Lock()
			child.Checkpoint = append(json.RawMessage(nil), cp.Units[fmt.Sprint(i)]...)
			mu.Unlock()
			var unitProgress fileCheckpoint
			_ = json.Unmarshal(child.Checkpoint, &unitProgress)
			item.Workers = unitProgress.Workers
			item.Transferred = unitProgress.Offset
			if r.Request.Kind == "upload" {
				for _, block := range unitProgress.UploadBlocks() {
					if block.CheckSum != "" {
						item.Transferred += block.Range.End - block.Range.Begin
					}
				}
			}
			if r.Request.Kind == "download" && !downloadSettings(r).Test {
				item.Written = item.Transferred
			}
			var progress fileCheckpoint
			_ = json.Unmarshal(child.Checkpoint, &progress)
			if progress.Skipped {
				item.State = "skipped"
			}
		}
		if runErr != nil {
			var unitCP fileCheckpoint
			_ = json.Unmarshal(child.Checkpoint, &unitCP)
			if ctx.Err() != nil || unitCP.Phase == "committing" || unitCP.Phase == "done" || fault.As(runErr).Code == "REMOTE_RESULT_UNKNOWN" || fault.As(runErr).Code == "LOCAL_RESULT_UNKNOWN" {
				return runErr
			}
			item.State = "failed"
			item.Failure = fault.As(upstreamFault(runErr))
		}
		return complete(i, item)
	}
	// Create directories in plan order before file workers; empty directories are
	// units in their own right. File concurrency and per-file parallel are bounded.
	for i, u := range plan.Units {
		if u.Directory && !done[uint64(i)] {
			if err = run(i); err != nil {
				return err
			}
			done[uint64(i)] = true
		}
	}
	concurrency := downloadSettings(r).Concurrency
	if r.Request.Kind == "upload" {
		concurrency = uploadSettings(r).Concurrency
	}
	if concurrency < 1 {
		concurrency = 1
	}
	queue := make(chan int)
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	var first error
	var errMu sync.Mutex
	for n := 0; n < concurrency; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range queue {
				if workCtx.Err() != nil {
					return
				}
				if err := run(i); err != nil {
					errMu.Lock()
					if first == nil {
						first = err
					}
					errMu.Unlock()
					cancel()
					return
				}
			}
		}()
	}
dispatch:
	for i := range plan.Units {
		if done[uint64(i)] {
			continue
		}
		select {
		case <-workCtx.Done():
			break dispatch
		case queue <- i:
		}
	}
	close(queue)
	wg.Wait()
	if first != nil {
		return first
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	mu.Lock()
	defer mu.Unlock()
	cp.Phase = "done"
	if err = persist(); err != nil {
		return err
	}
	f, stop := flushContext()
	defer stop()
	return e.repo.SaveScanResult(f, r.ID, result)
}

func (cp fileCheckpoint) UploadBlocks() []*uploader.BlockState {
	if cp.Upload == nil {
		return nil
	}
	return cp.Upload.BlockList
}

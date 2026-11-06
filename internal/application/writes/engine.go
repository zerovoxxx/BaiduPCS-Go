package writes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/account"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/file"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/transfer"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/write"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/ports"
)

type Engine struct {
	files    ports.FileWriter
	shares   ports.ShareGateway
	offline  ports.OfflineGateway
	recycle  ports.RecycleGateway
	resolver ports.ContentGateway
	repo     ports.TransferRepository
	children func(context.Context, transfer.Record, []string) (string, error)
}

func NewEngine(f ports.FileWriter, s ports.ShareGateway, o ports.OfflineGateway, b ports.RecycleGateway, resolver ports.ContentGateway, r ports.TransferRepository) *Engine {
	return &Engine{files: f, shares: s, offline: o, recycle: b, resolver: resolver, repo: r}
}
func (e *Engine) OnDownload(f func(context.Context, transfer.Record, []string) (string, error)) {
	e.children = f
}
func isFile(kind string) bool { return kind == "mkdir" || strings.HasPrefix(kind, "file_") }
func (e *Engine) Prepare(ctx context.Context, r transfer.Record, c account.Credentials) (string, error) {
	if c.Cookies["BDUSS"] == "" {
		return "", fault.New(fault.Unsupported, "BAIDU_COOKIE_REQUIRED", "写业务需要BDUSS")
	}
	return "write:" + r.Request.AccountID, nil
}
func (e *Engine) Plan(ctx context.Context, r transfer.Record, c account.Credentials) (json.RawMessage, error) {
	var p write.Plan
	if json.Unmarshal([]byte(r.Request.WritePlan), &p) != nil || len(p.Units) == 0 || len(p.Units) > 10000 {
		return nil, fault.New(fault.Invalid, "INVALID_REQUEST", "写任务计划无效")
	}
	cfg := configuration.NewSnapshot(r.ConfigRevision, r.Config)
	if g, err := e.files.OpenWriter(ctx, cfg, c); err == nil {
		if identity, ok := g.(interface{ Identity() (string, error) }); ok {
			if uk, err := identity.Identity(); err == nil {
				p.AccountUK = uk
			}
		}
		g.Close()
	}

	if isFile(r.Request.Kind) {
		for i := range p.Units {
			p.Units[i].PreparationError = nil
			p.Units[i].Before = nil
			p.Units[i].TargetBefore = nil
		}
		g, err := e.files.OpenWriter(ctx, cfg, c)
		if err != nil {
			return nil, err
		}
		defer g.Close()
		for i := range p.Units {
			u := &p.Units[i]
			if r.Request.Kind == "file_delete" && u.ID != "" {
				u.From = ""
				resolver, err := e.resolver.OpenContent(ctx, cfg, c)
				if err != nil {
					u.PreparationError = fault.As(err)
					continue
				}
				number, _ := strconv.ParseInt(u.ID, 10, 64)
				paths, err := resolver.ResolveIDs([]int64{number})
				resolver.Close()
				if err != nil {
					u.PreparationError = fault.As(err)
					continue
				}
				if len(paths) != 1 || paths[0] == "/" {
					u.PreparationError = fault.New(fault.Invalid, "INVALID_REQUEST", "禁止删除根目录或无效ID路径")
					continue
				}
				u.From = paths[0]
			}
			if u.From != "" {
				v, err := g.Meta(u.From)
				if err != nil {
					u.PreparationError = fault.As(err)
				} else {
					u.Before = &v
				}
			}
			if u.To != "" {
				v, err := g.Meta(u.To)
				if err == nil {
					u.TargetBefore = &v
				} else if fault.As(err).Kind != fault.NotFound {
					u.PreparationError = fault.As(err)
				}
			}
		}
	}
	if r.Request.Kind == "offline_add" {
		g, err := e.offline.OpenOffline(ctx, cfg, c)
		if err != nil {
			return nil, err
		}
		values, err := g.List()
		g.Close()
		if err == nil {
			for i := range p.Units {
				p.Units[i].BeforeWindowComplete = len(values) < 1000
				for _, v := range values {
					p.Units[i].PriorIDs = append(p.Units[i].PriorIDs, v.ID)
				}
			}
		}
	}
	if r.Request.Kind == "share_transfer" {
		if p.Share == nil || p.Share.AccountID != r.Request.AccountID {
			return nil, fault.New(fault.Conflict, "SHARE_INSPECTION_MISMATCH", "分享检查绑定不符")
		}
		if r.Request.RetryOf != "" {
			g, err := e.shares.OpenShares(ctx, cfg, c)
			if err != nil {
				return nil, err
			}
			fresh, err := g.Inspect("https://pan.baidu.com/s/"+p.Share.Feature, p.Share.Password)
			if err != nil {
				g.Close()
				return nil, err
			}
			for _, unit := range p.Units {
				if unit.ID == "" {
					continue
				}
				found := false
				for _, entry := range fresh.Entries {
					if entry.ID == unit.ID {
						found = true
					}
				}
				if !found {
					entries, err := g.SharedDirectory(fresh, path.Dir(unit.From))
					if err != nil {
						g.Close()
						return nil, err
					}
					fresh.Entries = append(fresh.Entries, entries...)
				}
			}
			for i := range p.Units {
				unit := &p.Units[i]
				if unit.ID == "" {
					continue
				}
				var entry *file.Entry
				for j := range fresh.Entries {
					if fresh.Entries[j].ID == unit.ID {
						entry = &fresh.Entries[j]
						break
					}
				}
				if entry == nil || entry.Path != unit.From || unit.Before != nil && !same(unit.Before, entry) {
					g.Close()
					return nil, fault.New(fault.Conflict, "SHARE_SOURCE_CHANGED", "分享项目已移除或发生变化")
				}
				unit.Before = entry
			}
			g.Close()
			fresh.Entries = nil
			fresh.RootIDs = nil
			fresh.Owner = r.Owner
			fresh.AccountID = r.Request.AccountID
			fresh.CredentialRevision = r.CredentialRevision
			p.Share = &fresh
		}
		if p.Share.CredentialRevision != r.CredentialRevision {
			return nil, fault.New(fault.Conflict, "SHARE_INSPECTION_MISMATCH", "分享检查凭据版本不符")
		}
		if r.Request.RetryOf == "" && p.Options.Collect && len(p.Units) > 1 {
			target := path.Join(r.Request.RemotePath, "collection-"+r.ID)
			for i := range p.Units {
				p.Units[i].To = path.Join(target, path.Base(p.Units[i].From))
			}
			p.Units = append([]write.Unit{{To: target}}, p.Units...)
		}
		g, err := e.files.OpenWriter(ctx, cfg, c)
		if err != nil {
			return nil, err
		}
		defer g.Close()
		for i := range p.Units {
			u := &p.Units[i]
			v, err := g.Meta(u.To)
			if err == nil {
				u.TargetBefore = &v
			} else if fault.As(err).Kind != fault.NotFound {
				return nil, err
			}
		}
	}
	raw, err := json.Marshal(p)
	if len(raw) > 16<<20 {
		return nil, fault.New(fault.TooLarge, "WRITE_PLAN_LIMIT", "写任务计划超过16MiB")
	}
	return raw, err
}

type attempt struct {
	Phase   string
	Outcome write.Outcome
}
type checkpoint struct {
	Version        int
	Binding, Phase string
	Attempts       map[int]attempt
	ChildID        string
}

func writeBinding(r transfer.Record) string {
	raw, _ := json.Marshal([]any{r.ID, r.Request.AccountID, r.ConfigRevision, r.CredentialRevision, r.Request.WritePlan})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func unknown() error {
	return fault.New(fault.Conflict, "REMOTE_RESULT_UNKNOWN", "远端结果尚未得到充分证据，禁止重放写入")
}
func same(a, b *file.Entry) bool {
	return a != nil && b != nil && a.ID == b.ID && a.IsDir == b.IsDir && a.Size == b.Size && (a.MD5 == nil || b.MD5 != nil && strings.EqualFold(*a.MD5, *b.MD5))
}
func contentMatches(a, b *file.Entry) bool {
	return a != nil && b != nil && !a.IsDir && !b.IsDir && a.Size == b.Size && a.MD5 != nil && b.MD5 != nil && strings.EqualFold(*a.MD5, *b.MD5)
}
func (e *Engine) Execute(ctx context.Context, r transfer.Record, c account.Credentials, save func(json.RawMessage) error) error {
	var p write.Plan
	if json.Unmarshal(r.Plan, &p) != nil {
		return fault.New(fault.Internal, "CHECKPOINT_INVALID", "写任务计划损坏")
	}
	if c.Revision != r.CredentialRevision {
		if p.AccountUK == "" {
			return fault.New(fault.Conflict, "RECONCILIATION_IDENTITY_UNVERIFIED", "原任务没有可验证的百度身份，不能换凭据核验")
		}
		g, err := e.files.OpenWriter(ctx, configuration.NewSnapshot(r.ConfigRevision, r.Config), c)
		if err != nil {
			return err
		}
		identity, ok := g.(interface{ Identity() (string, error) })
		if !ok {
			g.Close()
			return unknown()
		}
		uk, err := identity.Identity()
		g.Close()
		if err != nil || uk != p.AccountUK {
			return fault.New(fault.Conflict, "RECONCILIATION_ACCOUNT_MISMATCH", "新凭据不属于原百度账号")
		}
	}
	cp := checkpoint{Version: 1, Binding: writeBinding(r), Phase: "executing", Attempts: map[int]attempt{}}
	if len(r.Checkpoint) > 0 {
		if json.Unmarshal(r.Checkpoint, &cp) != nil || cp.Version != 1 || cp.Binding != writeBinding(r) {
			return fault.New(fault.Conflict, "CHECKPOINT_BINDING_MISMATCH", "写检查点绑定不符")
		}
	}
	persist := func() error {
		raw, err := json.Marshal(cp)
		if err != nil {
			return err
		}
		return save(raw)
	}
	old, err := e.repo.TransferItems(ctx, r.ID, 10001)
	if err != nil {
		return err
	}
	done := map[uint64]bool{}
	completed := map[uint64]transfer.Item{}
	result := transfer.ScanResult{Complete: true, ArtifactIDs: []string{}, ChildJobIDs: []string{}}
	result.Counts.Total = uint64(len(p.Units))
	count := func(item transfer.Item) {
		result.Counts.Processed++
		result.Affected += item.Affected
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
		if len(result.MutationItems) < 1000 {
			result.MutationItems = append(result.MutationItems, item)
		}
	}
	for _, item := range old {
		done[item.Index] = true
		completed[item.Index] = item
		count(item)
	}
	record := func(item transfer.Item) error {
		count(item)
		flush, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		return e.repo.SaveScanItem(flush, r.ID, item, result)
	}
	paths := []string{}
	for i, u := range p.Units {
		if done[uint64(i)] {
			if u.ID != "" && u.To != "" && completed[uint64(i)].State == "succeeded" {
				paths = append(paths, u.To)
			}
			continue
		}
		if ctx.Err() != nil {
			flush, stop := context.WithTimeout(context.Background(), 5*time.Second)
			current, readErr := e.repo.Transfer(flush, r.ID)
			stop()
			if readErr == nil && current.State == "cancelling" && cp.Phase != "committing" {
				for j := i; j < len(p.Units); j++ {
					if done[uint64(j)] {
						continue
					}
					unit := p.Units[j]
					input := unit.From
					if unit.ID != "" {
						input = unit.ID
					}
					if unit.SourceURL != "" {
						input = fmt.Sprintf("source_%d", j)
					}
					target := unit.To
					if target == "" {
						target = unit.From
					}
					if err = record(transfer.Item{ID: fmt.Sprintf("item_%d", j), Index: uint64(j), Path: target, Input: input, State: "cancelled", RemoteEffect: "none"}); err != nil {
						return err
					}
				}
			}
			return ctx.Err()
		}
		item := transfer.Item{ID: fmt.Sprintf("item_%d", i), Index: uint64(i), Path: u.To, Input: u.From, State: "succeeded", Attempts: 1, RemoteEffect: "none"}
		if item.Path == "" {
			item.Path = u.From
		}
		if u.ID != "" {
			item.Input = u.ID
		}
		if u.SourceURL != "" {
			item.Input = fmt.Sprintf("source_%d", i)
		}
		a := cp.Attempts[i]
		var outcome write.Outcome
		if a.Phase == "done" {
			outcome = a.Outcome
		} else if a.Phase == "committing" || r.Request.Reconcile {
			if a.Phase != "committing" {
				item.State = "cancelled"
				if r.Request.ReconcileDry {
					continue
				}
				if err = record(item); err != nil {
					return err
				}
				continue
			}
			outcome, err = e.audit(ctx, r, c, u, a.Outcome)
			if err != nil {
				return unknown()
			}
			if r.Request.ReconcileDry {
				continue
			}
		} else {
			if u.PreparationError != nil {
				err = u.PreparationError
				if r.Request.Kind == "file_delete" && p.Options.IgnoreMissing && u.PreparationError.Kind == fault.NotFound {
					err = nil
					outcome.Skipped = true
				}
			} else {
				// Preflight reads run before recording the write intent. Known conflicts
				// never become an indeterminate submission and never remove a destination.
				outcome, err = e.preflight(ctx, r, c, u, p.Options)
				if err == nil && !outcome.Skipped {
					cp.Attempts[i] = attempt{Phase: "committing"}
					cp.Phase = "committing"
					if err = persist(); err != nil {
						return err
					}
					outcome, err = e.apply(ctx, r, c, u, p)
				}
			}
			if err != nil && fault.As(err).Code == "REMOTE_RESULT_UNKNOWN" {
				cp.Attempts[i] = attempt{Phase: "committing", Outcome: outcome}
				cp.Phase = "committing"
				if saveErr := persist(); saveErr != nil {
					return saveErr
				}
				return err
			}
		}
		if r.Request.ReconcileDry {
			if err != nil {
				return err
			}
			continue
		}
		if err != nil {
			item.State = "failed"
			item.Failure = fault.As(err)
		} else {
			item.RemoteEffect = "confirmed"
			if outcome.Skipped {
				item.State = "skipped"
				item.RemoteEffect = "none"
			}
			item.ResultID = outcome.ID
			item.OfflineID = outcome.OfflineID
			item.Affected = outcome.Affected
			if outcome.Path != "" {
				item.Path = outcome.Path
			}
			if u.To != "" && u.ID != "" {
				paths = append(paths, u.To)
			}
		}
		if err == nil {
			cp.Attempts[i] = attempt{Phase: "done", Outcome: outcome}
			cp.Phase = "committing"
			if err = persist(); err != nil {
				return err
			}
		}
		if err = record(item); err != nil {
			return err
		}
		delete(cp.Attempts, i)
		cp.Phase = "executing"
		if err = persist(); err != nil {
			return err
		}
	}
	if r.Request.ReconcileDry {
		return fault.New(fault.Conflict, "RECONCILIATION_DRY_RUN", "只读核验完成，任务状态保持待核验")
	}
	if r.Request.Kind == "share_transfer" && p.Options.Download && len(paths) > 0 && e.children != nil {
		child, err := e.children(ctx, r, paths)
		if err != nil {
			return err
		}
		cp.ChildID = child
		result.ChildJobIDs = []string{child}
		if err = persist(); err != nil {
			return err
		}
	}
	cp.Phase = "done"
	if err = persist(); err != nil {
		return err
	}
	flush, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	return e.repo.SaveScanResult(flush, r.ID, result)
}
func (e *Engine) preflight(ctx context.Context, r transfer.Record, c account.Credentials, u write.Unit, o write.Options) (write.Outcome, error) {
	out := write.Outcome{}
	if isFile(r.Request.Kind) || r.Request.Kind == "share_transfer" {
		g, err := e.files.OpenWriter(ctx, configuration.NewSnapshot(r.ConfigRevision, r.Config), c)
		if err != nil {
			return out, err
		}
		defer g.Close()
		if u.From != "" && isFile(r.Request.Kind) {
			current, err := g.Meta(u.From)
			if err != nil {
				if r.Request.Kind == "file_delete" && o.IgnoreMissing && fault.As(err).Kind == fault.NotFound {
					out.Skipped = true
					return out, nil
				}
				return out, err
			}
			if u.Before != nil && !same(u.Before, &current) {
				return out, fault.New(fault.Conflict, "REMOTE_SOURCE_CHANGED", "源文件已变化，拒绝写入")
			}
		}
		if u.To != "" {
			target, err := g.Meta(u.To)
			if err == nil {
				if (r.Request.Kind == "mkdir" || r.Request.Kind == "share_transfer" && u.ID == "") && target.IsDir {
					out.Skipped = true
					return out, nil
				}
				if o.Policy == "skip" {
					out.Skipped = true
					return out, nil
				}
				if o.Policy != "overwrite" || target.IsDir {
					return out, fault.New(fault.Conflict, "TARGET_EXISTS", "写入目标已存在")
				}
				if u.TargetBefore != nil && !same(u.TargetBefore, &target) {
					return out, fault.New(fault.Conflict, "REMOTE_TARGET_CHANGED", "覆盖目标已变化")
				}
			} else if fault.As(err).Kind != fault.NotFound {
				return out, err
			}
		}
	}
	return out, nil
}
func (e *Engine) apply(ctx context.Context, r transfer.Record, c account.Credentials, u write.Unit, p write.Plan) (write.Outcome, error) {
	cfg := configuration.NewSnapshot(r.ConfigRevision, r.Config)
	out := write.Outcome{Path: u.To, Affected: 1}
	switch {
	case isFile(r.Request.Kind):
		g, err := e.files.OpenWriter(ctx, cfg, c)
		if err != nil {
			return out, err
		}
		defer g.Close()
		switch r.Request.Kind {
		case "mkdir":
			err = g.Mkdir(u.To)
		case "file_delete":
			out.Path = u.From
			err = g.Delete(u.From)
			if err == nil && u.Before != nil {
				out.ID = u.Before.ID
			}
		default:
			err = g.CopyMove(r.Request.Kind, u.From, u.To, p.Options.Policy)
			if fault.As(err).Code == "TARGET_EXISTS" && p.Options.Policy == "skip" {
				out.Skipped = true
				out.Affected = 0
				return out, nil
			}
		}
		if err != nil {
			return out, err
		}
		if r.Request.Kind != "file_delete" {
			v, err := g.Meta(u.To)
			if err != nil {
				return out, unknown()
			}
			out.ID = v.ID
		}
		return out, nil
	case r.Request.Kind == "share_transfer":
		if u.ID == "" {
			g, err := e.files.OpenWriter(ctx, cfg, c)
			if err != nil {
				return out, err
			}
			defer g.Close()
			err = g.Mkdir(u.To)
			return out, err
		}
		g, err := e.shares.OpenShares(ctx, cfg, c)
		if err != nil {
			return out, err
		}
		receipt, transferErr := g.Transfer(*p.Share, []string{u.ID}, path.Dir(u.To))
		err = transferErr
		out.IDs = receipt.IDs
		if len(receipt.IDs) == 1 {
			out.ID = receipt.IDs[0]
		}
		g.Close()
		if err != nil {
			return out, err
		}
		f, err := e.files.OpenWriter(ctx, cfg, c)
		if err != nil {
			return out, unknown()
		}
		v, err := f.Meta(u.To)
		f.Close()
		if err != nil || out.ID != "" && v.ID != out.ID || u.Before != nil && (v.IsDir != u.Before.IsDir || !v.IsDir && (v.Size != u.Before.Size || u.Before.MD5 != nil && !contentMatches(u.Before, &v))) {
			return out, unknown()
		}
		out.ID = v.ID
		return out, nil
	case r.Request.Kind == "share_cancel":
		g, err := e.shares.OpenShares(ctx, cfg, c)
		if err != nil {
			return out, err
		}
		defer g.Close()
		err = g.Cancel(u.ID)
		out.ID = u.ID
		return out, err
	case strings.HasPrefix(r.Request.Kind, "offline_"):
		g, err := e.offline.OpenOffline(ctx, cfg, c)
		if err != nil {
			return out, err
		}
		defer g.Close()
		switch r.Request.Kind {
		case "offline_add":
			out.OfflineID, err = g.Add(u.SourceURL, u.To)
		case "offline_cancel":
			out.OfflineID = u.ID
			err = g.Cancel(u.ID)
		case "offline_delete":
			out.OfflineID = u.ID
			err = g.Delete(u.ID)
		case "offline_clear":
			out.Affected, err = g.Clear()
		}
		return out, err
	case strings.HasPrefix(r.Request.Kind, "recycle_"):
		g, err := e.recycle.OpenRecycle(ctx, cfg, c)
		if err != nil {
			return out, err
		}
		defer g.Close()
		switch r.Request.Kind {
		case "recycle_restore":
			ids, e := g.Restore(u.ID)
			err = e
			if len(ids) > 0 {
				out.ID = ids[0]
				out.IDs = ids
				out.Affected = uint64(len(ids))
			}
		case "recycle_delete":
			out.ID = u.ID
			err = g.Delete(u.ID)
		case "recycle_clear":
			out.Affected, err = g.Clear()
		}
		return out, err
	}
	return out, fault.New(fault.Unsupported, "UNSUPPORTED_JOB_ACTION", "未知写任务")
}
func (e *Engine) audit(ctx context.Context, r transfer.Record, c account.Credentials, u write.Unit, known write.Outcome) (write.Outcome, error) {
	cfg := configuration.NewSnapshot(r.ConfigRevision, r.Config)
	out := known
	out.Affected = 1
	if isFile(r.Request.Kind) || r.Request.Kind == "share_transfer" {
		g, err := e.files.OpenWriter(ctx, cfg, c)
		if err != nil {
			return out, err
		}
		defer g.Close()
		target := u.To
		if r.Request.Kind == "file_delete" {
			target = u.From
		}
		v, err := g.Meta(target)
		if r.Request.Kind == "file_delete" {
			if fault.As(err).Kind == fault.NotFound || err == nil && u.Before != nil && v.ID != u.Before.ID {
				out.Path = target
				if u.Before != nil {
					out.ID = u.Before.ID
				}
				return out, nil
			}
			return out, unknown()
		}
		if err != nil {
			return out, unknown()
		}
		confirmed := (r.Request.Kind == "mkdir" || r.Request.Kind == "share_transfer" && u.ID == "") && v.IsDir || known.ID != "" && known.ID == v.ID || contentMatches(u.Before, &v)
		if r.Request.Kind == "file_copy" && known.ID == "" && u.TargetBefore != nil && same(u.TargetBefore, &v) {
			confirmed = false
		}
		if r.Request.Kind == "file_move" || r.Request.Kind == "file_rename" {
			_, err := g.Meta(u.From)
			confirmed = confirmed && fault.As(err).Kind == fault.NotFound
			if u.Before != nil && v.ID == u.Before.ID && fault.As(err).Kind == fault.NotFound {
				confirmed = true
			}
		}
		if !confirmed {
			return out, unknown()
		}
		out.Path = target
		out.ID = v.ID
		return out, nil
	}
	if strings.HasPrefix(r.Request.Kind, "offline_") && r.Request.Kind != "offline_clear" {
		g, err := e.offline.OpenOffline(ctx, cfg, c)
		if err != nil {
			return out, err
		}
		defer g.Close()
		if r.Request.Kind == "offline_add" {
			if known.OfflineID != "" {
				tasks, err := g.Query([]string{known.OfflineID})
				if err == nil && len(tasks) == 1 && !tasks[0].Missing {
					return out, nil
				}
				return out, unknown()
			}
			if !u.BeforeWindowComplete {
				return out, unknown()
			}
			tasks, err := g.List()
			if err != nil {
				return out, err
			}
			prior := map[string]bool{}
			if items, e := e.repo.TransferItems(ctx, r.ID, 10001); e == nil {
				for _, item := range items {
					if item.OfflineID != "" {
						prior[item.OfflineID] = true
					}
				}
			}
			for _, id := range u.PriorIDs {
				prior[id] = true
			}
			created, _ := time.Parse(time.RFC3339Nano, r.CreatedAt)
			matches := []write.Offline{}
			for _, t := range tasks {
				if !prior[t.ID] && t.Path == u.To && t.URL == u.SourceURL && t.Created >= created.Unix() {
					matches = append(matches, t)
				}
			}
			if len(matches) != 1 {
				return out, unknown()
			}
			out.OfflineID = matches[0].ID
			return out, nil
		}
		tasks, err := g.Query([]string{u.ID})
		if err != nil {
			return out, err
		}
		missing := len(tasks) == 0 || tasks[0].Missing
		if r.Request.Kind == "offline_delete" && missing {
			out.OfflineID = u.ID
			return out, nil
		}
		if r.Request.Kind == "offline_cancel" && !missing && tasks[0].Status == 7 {
			out.OfflineID = u.ID
			return out, nil
		}
		return out, unknown()
	}
	if r.Request.Kind == "share_cancel" {
		g, err := e.shares.OpenShares(ctx, cfg, c)
		if err != nil {
			return out, err
		}
		defer g.Close()
		_, err = g.Detail(u.ID)
		if fault.As(err).Kind == fault.NotFound {
			out.ID = u.ID
			return out, nil
		}
		return out, unknown()
	}
	// Without a saved returned restore ID, disappearance from recycle alone cannot
	// distinguish restoration from permanent deletion. Global clears are equally
	// ambiguous. Preserve interruption rather than inventing transaction rollback.
	return out, unknown()
}

func (e *Engine) Audit(ctx context.Context, r transfer.Record, c account.Credentials) error {
	r.Request.Reconcile = true
	r.Request.ReconcileDry = true
	err := e.Execute(ctx, r, c, func(json.RawMessage) error { return nil })
	if fault.As(err).Code == "RECONCILIATION_DRY_RUN" {
		return nil
	}
	return err
}

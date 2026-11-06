package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/account"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/auth"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/file"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/tool"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/transfer"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/ports"
	"io"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
)

type Service struct {
	gateway     ports.ToolGateway
	reader      ports.FileReader
	repo        ports.TransferRepository
	accounts    ports.AccountRepository
	cfg         ports.ConfigurationRepository
	local       ports.ToolFiles
	artifacts   ports.ArtifactFiles
	mu          sync.Mutex
	secrets     map[string][]byte
	secretSalt  []byte
	fingerprint ports.SecretFingerprint
}

func New(g ports.ToolGateway, f ports.FileReader, r ports.TransferRepository, a ports.AccountRepository, c ports.ConfigurationRepository, l ports.ToolFiles, art ports.ArtifactFiles, fingerprints ...ports.SecretFingerprint) *Service {
	s := &Service{gateway: g, reader: f, repo: r, accounts: a, cfg: c, local: l, artifacts: art, secrets: map[string][]byte{}}
	if len(fingerprints) > 0 {
		s.fingerprint = fingerprints[0]
	}
	return s
}
func (s *Service) Session(ctx context.Context, p auth.Principal, id string) (ports.ToolSession, error) {
	if !p.AllowsAccount(id) {
		return nil, fault.New(fault.Forbidden, "FORBIDDEN", "无权访问账号")
	}
	a, e := s.accounts.Account(ctx, id)
	if e != nil {
		return nil, e
	}
	cfg, e := s.cfg.Read(ctx)
	if e != nil {
		return nil, e
	}
	return s.gateway.OpenTools(ctx, cfg, a.Credentials)
}
func (s *Service) Prepare(ctx context.Context, r transfer.Record, _ account.Credentials) (string, error) {
	var plan tool.Plan
	if e := json.Unmarshal([]byte(r.Request.ToolPlan), &plan); e != nil {
		return "", e
	}
	if len(plan.Units) == 0 || len(plan.Units) > 100 {
		return "", fault.New(fault.Invalid, "INVALID_REQUEST", "工具选择必须为1至100项")
	}

	if e := s.local.PrepareTool(ctx, plan); e != nil {
		return "", e
	}

	if plan.Destination != nil {
		return "tool/" + plan.Destination.RootID + "/" + plan.Destination.Directory, nil
	}
	return "tool/" + r.ID, nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(b []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.r.Read(b)
}
func compare(a, b tool.Checksum, algs []string) (bool, error) {
	same := true
	for _, k := range algs {
		var x, y *string
		switch k {
		case "size":
			x = &a.Size
			y = &b.Size
		case "md5":
			x = a.MD5
			y = b.MD5
		case "slice_md5":
			x = a.SliceMD5
			y = b.SliceMD5
		case "crc32":
			x = a.CRC32
			y = b.CRC32
		}
		if x == nil || y == nil {
			return false, fault.New(fault.Unsupported, "CHECKSUM_UNAVAILABLE", "上游未提供指定摘要")
		}
		if !strings.EqualFold(*x, *y) {
			same = false
		}
	}
	return same, nil
}
func (s *Service) Execute(ctx context.Context, r transfer.Record, c account.Credentials, save func(json.RawMessage) error) error {
	var plan tool.Plan
	if e := json.Unmarshal([]byte(r.Request.ToolPlan), &plan); e != nil {
		return e
	}
	if r.Request.Kind == "export" {
		return s.export(ctx, r, c, plan, save)
	}
	if r.Request.Kind == "local_encryption" || r.Request.Kind == "local_decryption" {
		return s.crypto(ctx, r, plan, save)
	}
	var g ports.ToolSession
	var e error
	if r.Request.AccountID != "" {
		g, e = s.gateway.OpenTools(ctx, configuration.NewSnapshot(r.ConfigRevision, r.Config), c)
		if e != nil {
			return e
		}
		defer g.Close()
	}
	var opts struct {
		Algorithms []string `json:"algorithms"`
	}
	_ = json.Unmarshal(plan.Options, &opts)
	result := transfer.ScanResult{Complete: true, Counts: transfer.Counts{Total: uint64(len(plan.Units))}, ArtifactIDs: []string{}}
	defer s.saveIncomplete(r.ID, &result)
	for i, u := range plan.Units {
		if e := ctx.Err(); e != nil {
			if err := s.cancelRemaining(r, plan, i, &result); err != nil {
				return err
			}
			return e
		}
		item := transfer.Item{ID: fmt.Sprint("item_", i), Index: uint64(i), Path: u.Path, RootID: u.Local.RootID, RelativePath: u.Local.Path, State: "succeeded", Attempts: 1, Input: u.Path}
		out := tool.ResultItem{Input: u.Path}
		if out.Input == "" {
			out.Input = u.Local.Path
			item.Input = out.Input
		}
		var v tool.Checksum
		var runErr error
		switch r.Request.Kind {
		case "local_checksum":
			v, runErr = s.local.Checksum(ctx, u.Local, opts.Algorithms)
		case "file_verification":
			v, runErr = g.Inspect(u.Path)
			if runErr == nil {
				local, e := s.local.Checksum(ctx, u.Local, []string{"md5", "slice_md5", "crc32"})
				runErr = e
				if e == nil {
					matches, e := compare(v, local, opts.Algorithms)
					out.Matches = &matches
					runErr = e
					if e == nil && !matches {
						runErr = fault.New(fault.Conflict, "CHECKSUM_MISMATCH", "本地文件与远端摘要不一致")
					}
				}
			}
		case "checksum_fix":
			v, runErr = g.Inspect(u.Path)
			if runErr == nil {
				if v.MD5 == nil || v.SliceMD5 == nil || v.CRC32 == nil {
					runErr = fault.New(fault.Unsupported, "CHECKSUM_UNAVAILABLE", "修复所需摘要缺失")
				} else {
					if e := save(json.RawMessage(`{"Phase":"committing"}`)); e != nil {
						return e
					}
					runErr = g.Fix(u.Path, v)
					if fault.As(runErr).Code == "REMOTE_RESULT_UNKNOWN" {
						return runErr
					}
					if e := save(json.RawMessage(`{"Phase":"prepared"}`)); e != nil {
						return e
					}
				}
			}
		default:
			return fault.New(fault.Unsupported, "UNSUPPORTED_OPTION", "未知工具任务")
		}
		if v.Size != "" {
			out.Checksum = &v
		}
		if runErr != nil && ctx.Err() != nil {
			item.State = "cancelled"
			result.Counts.Cancelled++
			result.Complete = false
		} else if runErr != nil {
			item.State = "failed"
			item.Failure = fault.As(runErr)
			out.Failure = item.Failure
			result.Counts.Failed++
		} else {
			result.Counts.Succeeded++
		}
		result.Counts.Processed++
		if len(result.IntegrityItems) < 1000 {
			result.IntegrityItems = append(result.IntegrityItems, out)
		}
		if r.Request.Kind == "checksum_fix" {
			item.RemoteEffect = "none"
			if runErr == nil {
				item.RemoteEffect = "confirmed"
				result.Affected++
			}
			result.MutationItems = append(result.MutationItems, item)
		}
		if e := s.saveItem(r.ID, item, result); e != nil {
			return e
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return s.repo.SaveScanResult(ctx, r.ID, result)
}
func (s *Service) CleanupTransfer(ctx context.Context, r transfer.Record) error {
	var p tool.Plan
	_ = json.Unmarshal([]byte(r.Request.ToolPlan), &p)
	s.ForgetSecret(p.KeyRef)
	return s.local.CleanupTool(ctx, r.Checkpoint)
}

// Methods implemented with the local cipher adapter in crypto.go.
func (s *Service) ForgetSecret(ref string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.secrets[ref] {
		s.secrets[ref][i] = 0
	}
	delete(s.secrets, ref)
}
func (s *Service) export(ctx context.Context, r transfer.Record, c account.Credentials, p tool.Plan, save func(json.RawMessage) error) error {
	var opts struct {
		Recursive bool   `json:"recursive"`
		Root      string `json:"root_path"`
		Format    string `json:"format"`
		Retries   int    `json:"max_retries"`
	}
	if e := json.Unmarshal(p.Options, &opts); e != nil {
		return e
	}
	deadline, cancel := context.WithTimeout(ctx, time.Hour)
	defer cancel()
	ctx = deadline
	files, e := s.reader.OpenFiles(ctx, configuration.NewSnapshot(r.ConfigRevision, r.Config), c)
	if e != nil {
		return e
	}
	defer files.Close()
	g, e := s.gateway.OpenTools(ctx, configuration.NewSnapshot(r.ConfigRevision, r.Config), c)
	if e != nil {
		return e
	}
	defer g.Close()
	w, e := s.artifacts.Create(ctx, r.ID, "export."+map[string]string{"jsonl": "jsonl", "legacy_command": "sh", "legacy_link": "txt"}[opts.Format], "text/plain; charset=utf-8")
	if e != nil {
		return e
	}
	done := false
	defer func() {
		if !done {
			clean, c := context.WithTimeout(context.Background(), 5*time.Second)
			defer c()
			_ = w.Abort(clean)
		}
	}()
	var dest ports.ToolOutput
	if p.Destination != nil {
		dest, e = s.local.CreateToolOutput(ctx, *p.Destination, "export_"+r.ID+".txt", save)
		if e != nil {
			return e
		}
		defer dest.Abort()
	}
	var writer io.Writer = w
	if dest != nil {
		writer = io.MultiWriter(w, dest)
	}
	result := transfer.ScanResult{Complete: true, ArtifactIDs: []string{}, MissingFields: []string{}}
	defer func() {
		if !done {
			result.Complete = false
			flush, done := context.WithTimeout(context.Background(), 5*time.Second)
			defer done()
			_ = s.repo.SaveScanResult(flush, r.ID, result)
		}
	}()
	if opts.Format == "legacy_link" && opts.Root != "" {
		result.MissingFields = append(result.MissingFields, "root_path_ignored_for_legacy_link")
	}
	type node struct {
		p, root     string
		depth, page int
		entry       *file.Entry
	}
	stack := []node{}
	for i := len(p.Units) - 1; i >= 0; i-- {
		u := p.Units[i]
		stack = append(stack, node{p: u.Path, root: path.Dir(u.Path)})
	}
	retry := func(fn func() error) error {
		var e error
		for n := 0; n <= opts.Retries; n++ {
			e = fn()
			if e == nil || !fault.As(e).Retryable {
				return e
			}
			t := time.NewTimer(time.Duration(min(n+1, 5)) * 100 * time.Millisecond)
			select {
			case <-ctx.Done():
				t.Stop()
				return ctx.Err()
			case <-t.C:
			}
		}
		return e
	}
	emit := func(n node, entry *file.Entry, cs *tool.Checksum, err error) error {
		i := result.Counts.Processed
		item := transfer.Item{ID: fmt.Sprint("item_", i), Index: i, Path: n.p, Input: n.p, Attempts: 1, State: "succeeded"}
		result.Counts.Total++
		result.Counts.Processed++
		if err != nil {
			item.State = "failed"
			item.Failure = fault.As(err)
			result.Counts.Failed++
			result.Complete = false
			if opts.Format == "jsonl" {
				b, _ := json.Marshal(map[string]any{"path": n.p, "state": "failed", "error_code": item.Failure.Code})
				if _, e := writer.Write(append(b, '\n')); e != nil {
					return e
				}
			}
		} else {
			result.Counts.Succeeded++
			if cs != nil {
				for field, value := range map[string]*string{"md5": cs.MD5, "slice_md5": cs.SliceMD5, "crc32": cs.CRC32} {
					if value == nil {
						found := false
						for _, old := range result.MissingFields {
							if old == field {
								found = true
							}
						}
						if !found {
							result.MissingFields = append(result.MissingFields, field)
						}
					}
				}
				sort.Strings(result.MissingFields)
			}
			outpath := n.p
			if opts.Root != "" {
				outpath = path.Join(opts.Root, strings.TrimPrefix(n.p, n.root))
			}
			var line string
			switch opts.Format {
			case "jsonl":
				row := map[string]any{"path": outpath, "state": "succeeded", "is_dir": entry.IsDir, "checksum": cs}
				b, e := json.Marshal(row)
				if e != nil {
					return e
				}
				line = string(b) + "\n"
			case "legacy_command":
				if entry.IsDir {
					line = "BaiduPCS-Go mkdir " + shellQuote(outpath) + "\n"
				} else if cs.MD5 == nil || cs.SliceMD5 == nil || cs.CRC32 == nil {
					return fault.New(fault.Unsupported, "CHECKSUM_UNAVAILABLE", "旧命令导出所需摘要不完整")
				} else {
					line = fmt.Sprintf("BaiduPCS-Go rapidupload -length=%s -md5=%s -slicemd5=%s -crc32=%s %s\n", cs.Size, *cs.MD5, *cs.SliceMD5, *cs.CRC32, shellQuote(outpath))
				}
			case "legacy_link":
				if !entry.IsDir {
					if cs.MD5 == nil || cs.SliceMD5 == nil {
						return fault.New(fault.Unsupported, "CHECKSUM_UNAVAILABLE", "旧链接导出所需摘要不完整")
					}
					line = fmt.Sprintf("%s#%s#%s#%s\n", *cs.MD5, *cs.SliceMD5, cs.Size, path.Base(n.p))
				}
			}
			if _, e := io.WriteString(writer, line); e != nil {
				return e
			}
		}
		return s.repo.SaveScanItem(ctx, r.ID, item, result)
	}
	seen := map[string]bool{}
	for len(stack) > 0 {
		if e := ctx.Err(); e != nil {
			return e
		}
		if result.Counts.Processed >= 100000 || len(stack) > 10000 {
			return fault.New(fault.TooLarge, "EXPORT_LIMIT", "导出达到节点限制")
		}
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n.depth > 1000 {
			return fault.New(fault.TooLarge, "EXPORT_LIMIT", "导出目录过深")
		}
		entry := file.Entry{}
		if n.entry != nil {
			entry = *n.entry
		} else {
			e = retry(func() error { var x error; entry, x = files.Meta(n.p); return x })
			if e != nil {
				if e := emit(n, nil, nil, e); e != nil {
					return e
				}
				continue
			}
		}
		if entry.IsDir {
			if n.depth > 0 && !opts.Recursive {
				continue
			}
			page := max(n.page, 1)
			var list []file.Entry
			e = retry(func() error {
				var x error
				list, x = files.List(n.p, file.ListOptions{Page: page, PageSize: 1000, OrderBy: "name", Direction: "asc"})
				return x
			})
			if e != nil {
				if e := emit(n, nil, nil, e); e != nil {
					return e
				}
				continue
			}
			if len(list) == 0 && page == 1 {
				if e := emit(n, &entry, nil, nil); e != nil {
					return e
				}
			}
			if len(list) == 1000 {
				next := n
				next.page = page + 1
				next.entry = &entry
				stack = append(stack, next)
			}
			for i := len(list) - 1; i >= 0; i-- {
				x := list[i]
				if !strings.HasPrefix(x.Path, strings.TrimRight(n.p, "/")+"/") || seen[x.Path] {
					return fault.New(fault.BadGateway, "UPSTREAM_PAGINATION_STALLED", "导出目录分页重复或越界")
				}
				seen[x.Path] = true
				stack = append(stack, node{p: x.Path, root: n.root, depth: n.depth + 1, entry: &x})
			}
			continue
		}
		var cs tool.Checksum
		e = retry(func() error { var x error; cs, x = g.Inspect(n.p); return x })
		if e == nil && opts.Format != "jsonl" && (cs.MD5 == nil || cs.SliceMD5 == nil || opts.Format == "legacy_command" && cs.CRC32 == nil) {
			e = fault.New(fault.Unsupported, "CHECKSUM_UNAVAILABLE", "旧格式导出摘要缺失")
		}
		if e := emit(n, &entry, &cs, e); e != nil {
			return e
		}
	}
	a, e := w.Complete(ctx, result.Complete)
	if e != nil {
		return e
	}
	result.ArtifactIDs = []string{a.ID}
	if dest != nil {
		if e := dest.Commit(ctx); e != nil {
			return e
		}
	}
	done = true
	return s.repo.SaveScanResult(ctx, r.ID, result)
}
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

func (s *Service) Recover(ctx context.Context) error {
	for _, kind := range []string{"local_encryption", "local_decryption", "export"} {
		records, e := s.repo.ListTransfers(ctx, transfer.Filter{Kind: kind, State: "interrupted"}, 10000, nil)
		if e != nil {
			return e
		}
		for _, r := range records {
			if e = s.local.CleanupTool(ctx, r.Checkpoint); e != nil {
				return e
			}
		}
	}
	return nil
}

func (s *Service) saveItem(id string, item transfer.Item, result transfer.ScanResult) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.repo.SaveScanItem(ctx, id, item, result)
}
func (s *Service) saveIncomplete(id string, result *transfer.ScanResult) {
	if result.Counts.Processed < result.Counts.Total {
		result.Complete = false
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.repo.SaveScanResult(ctx, id, *result)
	}
}
func (s *Service) cancelRemaining(r transfer.Record, plan tool.Plan, start int, result *transfer.ScanResult) error {
	result.Complete = false
	for i := start; i < len(plan.Units); i++ {
		u := plan.Units[i]
		input := u.Path
		if input == "" {
			input = u.Local.Path
		}
		item := transfer.Item{ID: fmt.Sprint("item_", i), Index: uint64(i), Input: input, Path: u.Path, RootID: u.Local.RootID, RelativePath: u.Local.Path, State: "cancelled"}
		result.Counts.Processed++
		result.Counts.Cancelled++
		if r.Request.Kind == "checksum_fix" {
			item.RemoteEffect = "none"
			result.MutationItems = append(result.MutationItems, item)
		} else if r.Request.Kind == "local_checksum" || r.Request.Kind == "file_verification" {
			result.IntegrityItems = append(result.IntegrityItems, tool.ResultItem{Input: input})
		}
		if e := s.saveItem(r.ID, item, *result); e != nil {
			return e
		}
	}
	return nil
}

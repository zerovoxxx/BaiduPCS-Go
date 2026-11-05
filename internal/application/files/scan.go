package files

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/account"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/file"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/transfer"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/ports"
	"strconv"
	"time"
)

type Scanner struct {
	reader    ports.FileReader
	repo      ports.TransferRepository
	artifacts ports.ArtifactFiles
}

func NewScanner(r ports.FileReader, j ports.TransferRepository, a ports.ArtifactFiles) *Scanner {
	return &Scanner{r, j, a}
}
func (s *Scanner) Execute(ctx context.Context, r transfer.Record, c account.Credentials, _ func(json.RawMessage) error) error {
	var plan file.ScanPlan
	if err := json.Unmarshal([]byte(r.Request.ScanPlan), &plan); err != nil {
		return err
	}
	limit, err := strconv.ParseUint(plan.Options.MaxNodes, 10, 64)
	if err != nil {
		return err
	}
	scanCtx, cancel := context.WithTimeout(ctx, time.Duration(plan.Options.MaxDuration)*time.Second)
	defer cancel()
	reader, err := s.reader.OpenFiles(scanCtx, configuration.NewSnapshot(r.ConfigRevision, r.Config), c)
	if err != nil {
		return err
	}
	defer reader.Close()
	writer, err := s.artifacts.Create(ctx, r.ID, "scan.jsonl", "application/x-ndjson")
	if err != nil {
		return err
	}
	result := transfer.ScanResult{Complete: true, ArtifactIDs: []string{}}
	type node struct {
		Path  string
		Root  string
		Depth int
		Entry *file.Entry
	}
	stack := []node{}
	for i := len(plan.Paths) - 1; i >= 0; i-- {
		stack = append(stack, node{Path: plan.Paths[i], Root: plan.Paths[i], Depth: -1})
	}
	seen := map[string]bool{}
	flush := func(f func(context.Context) error) error {
		save, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		return f(save)
	}
	emit := func(p string, depth int, e *file.Entry, problem error, state string) error {
		item := transfer.Item{ID: "item_" + strconv.FormatUint(result.Counts.Processed, 10), Index: result.Counts.Processed, Path: p, State: state, Attempts: 1}
		result.Counts.Processed++
		result.Counts.Total = result.Counts.Processed
		if state == "succeeded" {
			result.Counts.Succeeded++
			if e != nil {
				size := e.Size
				item.Size = &size
				if e.IsDir {
					result.Directories++
				} else {
					result.Files++
					result.Bytes += uint64(e.Size)
				}
			}
		} else if state == "cancelled" {
			result.Counts.Cancelled++
		} else {
			result.Counts.Failed++
			item.Failure = fault.As(problem)
		}
		// The result file has public facts only, never an internal record/config.
		output := map[string]any{"path": p, "state": state}
		if plan.Options.Mode == "tree" {
			output["depth"] = max(depth+1, 0)
		}
		if e != nil {
			output["name"] = e.Name
			output["is_dir"] = e.IsDir
			output["size_bytes"] = strconv.FormatInt(e.Size, 10)
			if plan.Options.IncludeIDs {
				output["fs_id"] = e.ID
			}
		}
		if item.Failure != nil {
			output["error_code"] = item.Failure.Code
		}
		if plan.Options.Mode != "statistics" {
			if err := json.NewEncoder(writer).Encode(output); err != nil {
				return err
			}
		}
		return flush(func(save context.Context) error { return s.repo.SaveScanItem(save, r.ID, item, result) })
	}
	var runErr error
	for len(stack) > 0 {
		if scanCtx.Err() != nil {
			result.Complete = false
			result.Truncation = "cancelled"
			if ctx.Err() == nil {
				result.Truncation = "max_duration"
			}
			break
		}
		if result.Counts.Processed >= limit {
			result.Complete = false
			result.Truncation = "max_nodes"
			break
		}
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[n.Path] {
			continue
		}
		seen[n.Path] = true
		e := file.Entry{}
		if n.Entry != nil {
			e = *n.Entry
		} else {
			e, err = reader.Meta(n.Path)
		}
		if err != nil {
			if scanCtx.Err() != nil {
				stack = append(stack, n)
				result.Complete = false
				result.Truncation = "cancelled"
				if ctx.Err() == nil {
					result.Truncation = "max_duration"
				}
				break
			}
			result.Complete = false
			result.Truncation = "upstream_error"
			if runErr = emit(n.Path, n.Depth, nil, err, "failed"); runErr != nil {
				break
			}
			err = nil
			continue
		}
		if runErr = emit(n.Path, n.Depth, &e, nil, "succeeded"); runErr != nil {
			break
		}
		if !e.IsDir || plan.Options.Depth >= 0 && n.Depth >= plan.Options.Depth {
			continue
		}
		if result.Counts.Processed >= limit {
			result.Complete = false
			result.Truncation = "max_nodes"
			break
		}
		pageSeen := map[string]bool{}
		children := []node{}
		for page := 1; ; page++ {
			if scanCtx.Err() != nil {
				err = scanCtx.Err()
				break
			}
			list, e := reader.List(n.Path, file.ListOptions{Page: page, PageSize: 1000, OrderBy: "name", Direction: "asc"})
			err = e
			if err != nil {
				break
			}
			stalled := false
			for i := range list {
				v := list[i]
				if pageSeen[v.ID] || v.Path == n.Path {
					err = fault.New(fault.BadGateway, "UPSTREAM_PAGINATION_STALLED", "百度目录分页未推进")
					stalled = true
					break
				}
				pageSeen[v.ID] = true
				children = append(children, node{Path: v.Path, Root: n.Root, Depth: n.Depth + 1, Entry: &v})
				// Don't fetch the rest of a huge directory merely to discover a limit.
				if uint64(len(children)+len(stack))+result.Counts.Processed >= limit {
					result.Complete = false
					result.Truncation = "max_nodes"
					break
				}
			}
			if stalled || len(list) < 1000 || uint64(len(children)+len(stack))+result.Counts.Processed >= limit {
				break
			}
		}
		if err != nil {
			if scanCtx.Err() != nil {
				stack = append(stack, n)
				result.Complete = false
				result.Truncation = "cancelled"
				if ctx.Err() == nil {
					result.Truncation = "max_duration"
				}
				break
			}
			result.Complete = false
			result.Truncation = "upstream_error"
			if runErr = emit(n.Path, n.Depth, nil, err, "failed"); runErr != nil {
				break
			}
			err = nil
		}
		for i := len(children) - 1; i >= 0; i-- {
			stack = append(stack, children[i])
		}
	}
	if scanCtx.Err() != nil && len(stack) > 0 {
		// Save an explicit retry unit for each unfinished selection root. This also
		// preserves already discovered success when a request is cancelled.
		pending := map[string]bool{}
		for _, n := range stack {
			if pending[n.Root] {
				continue
			}
			pending[n.Root] = true
			if e := emit(n.Root, -1, nil, nil, "cancelled"); e != nil {
				runErr = errors.Join(runErr, e)
				break
			}
		}
	}
	if runErr != nil {
		result.Complete = false
		result.Truncation = "upstream_error"
	}
	if plan.Options.Mode == "statistics" {
		if e := json.NewEncoder(writer).Encode(map[string]any{"files": strconv.FormatUint(result.Files, 10), "directories": strconv.FormatUint(result.Directories, 10), "size_bytes": strconv.FormatUint(result.Bytes, 10), "complete": result.Complete, "truncation_reason": result.Truncation}); e != nil {
			runErr = errors.Join(runErr, e)
		}
	}
	if runErr == nil {
		err = flush(func(save context.Context) error {
			a, e := writer.Complete(save, result.Complete)
			if e == nil {
				result.ArtifactIDs = []string{a.ID}
			}
			return e
		})
		runErr = errors.Join(runErr, err)
	} else {
		_ = flush(writer.Abort)
	}
	if e := flush(func(save context.Context) error { return s.repo.SaveScanResult(save, r.ID, result) }); e != nil {
		runErr = errors.Join(runErr, e)
	}
	if runErr != nil {
		return runErr
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return nil
}

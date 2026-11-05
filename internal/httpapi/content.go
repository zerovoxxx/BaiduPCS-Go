package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/auth"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"time"

	appfiles "github.com/qjfoidnh/BaiduPCS-Go/internal/application/files"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/file"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/transfer"
)

func (a *API) serveContent(id string, w http.ResponseWriter, r *http.Request) error {
	p := principal(r.Context())
	account := r.PathValue("account_id")
	if id == "DWN05" {
		var in struct {
			Path      string `json:"path"`
			Seconds   *int   `json:"expires_in_seconds"`
			BasePath  string `json:"base_path"`
			ContextID string `json:"context_id"`
		}
		if err := decodeRequest(w, r, &in); err != nil {
			return err
		}
		if in.Path == "" {
			return invalid("缺少path")
		}
		remote, err := appfiles.Resolve(in.Path, file.Scope{BasePath: in.BasePath, ContextID: in.ContextID})
		if err != nil {
			return err
		}
		key, err := idempotencyKey(r)
		if err != nil {
			return err
		}
		value, claim, err := a.deps.Content.Issue(r.Context(), p, account, remote, deref(in.Seconds, 60), key)
		if err != nil {
			return err
		}
		w.Header().Set("Location", "/api/v1/accounts/"+account+"/content?path="+url.QueryEscape(remote)+"&ticket="+url.QueryEscape(value))
		a.respond(w, r, 201, map[string]any{"url": "/api/v1/accounts/" + account + "/content?path=" + url.QueryEscape(remote) + "&ticket=" + url.QueryEscape(value), "expires_at": time.Unix(claim.Expires, 0).UTC().Format(time.RFC3339), "path": remote, "account_id": account})
		return nil
	}
	if id == "DWN02" {
		var in struct {
			Selector *struct {
				Kind   string   `json:"kind"`
				Values []string `json:"values"`
			} `json:"selector"`
			Source    string `json:"source"`
			HTTPS     *bool  `json:"https,omitempty"`
			Index     int    `json:"link_index"`
			BasePath  string `json:"base_path"`
			ContextID string `json:"context_id"`
		}
		if err := decodeRequest(w, r, &in); err != nil {
			return err
		}
		if in.Selector == nil || len(in.Selector.Values) == 0 || len(in.Selector.Values) > 100 || in.Index < 0 || in.Index > 99 {
			return invalid("直链选择无效")
		}
		if in.HTTPS != nil && !*in.HTTPS {
			return fault.New(fault.Unsupported, "HTTPS_REQUIRED", "服务只返回HTTPS直链")
		}
		if in.Source == "" {
			in.Source = "pcs"
		}
		items := []any{}
		counts := transfer.Counts{Total: uint64(len(in.Selector.Values)), Processed: uint64(len(in.Selector.Values))}
		for _, v := range in.Selector.Values {
			var remote string
			var err error
			if in.Selector.Kind == "paths" {
				remote, err = appfiles.Resolve(v, file.Scope{BasePath: in.BasePath, ContextID: in.ContextID})
			} else if in.Selector.Kind == "fs_ids" {
				number, e := strconv.ParseInt(v, 10, 64)
				if e != nil || number <= 0 {
					return invalid("fs_id无效")
				}
				paths, e := a.deps.Content.ResolveIDs(r.Context(), p, account, []int64{number})
				err = e
				if e == nil {
					remote = paths[0]
				}
			} else {
				return invalid("直链选择器无效")
			}
			if err != nil {
				counts.Failed++
				items = append(items, map[string]any{"status": "failed", "input": v, "problem": publicProblem(fault.As(err), RequestID(r.Context()))})
				continue
			}
			links, err := a.deps.Content.Links(r.Context(), p, account, remote, in.Source, in.Index)
			if err != nil {
				counts.Failed++
				items = append(items, map[string]any{"status": "failed", "input": v, "problem": publicProblem(fault.As(err), RequestID(r.Context()))})
				continue
			}
			out := []any{}
			for _, link := range links {
				out = append(out, map[string]any{"url": link.URL, "link_ref": link.Reference, "expires_at": nil, "required_user_agent": link.UserAgent, "requires_baidu_cookie": link.RequiresCookie, "usable_without_baidu_cookie": !link.RequiresCookie})
			}
			counts.Succeeded++
			items = append(items, map[string]any{"status": "succeeded", "input": v, "links": out})
		}
		a.success(w, r, map[string]any{"items": items, "summary": publicCounts(counts), "complete": true})
		return nil
	}
	q, err := queryValues(r, "path", "base_path", "context_id", "ticket")
	if err != nil {
		return err
	}
	if q.Get("path") == "" {
		return invalid("缺少path")
	}
	remote, err := appfiles.Resolve(q.Get("path"), file.Scope{BasePath: q.Get("base_path"), ContextID: q.Get("context_id")})
	if err != nil {
		return err
	}
	meta, err := a.deps.Content.Metadata(r.Context(), p, account, remote)
	if err != nil {
		return err
	}
	hash := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%s:%d:%d", account, remote, meta.MD5, meta.Size, meta.Mtime)))
	etag := `"` + hex.EncodeToString(hash[:]) + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Accept-Ranges", "bytes")
	modified := time.Unix(meta.Mtime, 0)
	w.Header().Set("Last-Modified", modified.UTC().Format(http.TimeFormat))
	first, last, status := int64(0), meta.Size-1, 200
	ranges, ifRanges := r.Header.Values("Range"), r.Header.Values("If-Range")
	if len(ranges) > 1 || len(ifRanges) > 1 {
		return invalid("Range或If-Range不能重复")
	}
	if len(ranges) == 1 {
		from, to, e := artifactRange(ranges[0], meta.Size)
		match := len(ifRanges) == 0 || ifRanges[0] == etag
		if len(ifRanges) == 1 {
			if date, e := http.ParseTime(ifRanges[0]); e == nil {
				match = !modified.After(date)
			}
		}
		if e != nil {
			if f, ok := e.(*httpError); !ok || f.status != 416 || match {
				if ok && f.status == 416 {
					w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", meta.Size))
				}
				return e
			}
		} else if match {
			first, last, status = from, to, 206
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", first, last, meta.Size))
		}
	}
	var body io.ReadCloser
	if r.Method != "HEAD" && meta.Size > 0 {
		body, err = a.deps.Content.Open(r.Context(), p, account, remote, first, last, meta.Size)
		if err != nil {
			return err
		}
		defer body.Close()
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(max(last-first+1, 0), 10))
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": meta.Name}))
	w.WriteHeader(status)
	if body == nil {
		return nil
	}
	controller := http.NewResponseController(w)
	defer controller.SetWriteDeadline(time.Time{})
	buffer := make([]byte, 64<<10)
	remaining := last - first + 1
	for remaining > 0 {
		if r.Context().Err() != nil {
			return nil
		}
		n, readErr := body.Read(buffer[:min(int64(len(buffer)), remaining)])
		if n > 0 {
			_ = controller.SetWriteDeadline(time.Now().Add(30 * time.Second))
			written, err := w.Write(buffer[:n])
			remaining -= int64(written)
			if err != nil || written != n {
				return nil
			}
			_ = controller.Flush()
		}
		if readErr != nil || n == 0 {
			return nil
		}
	}
	return nil
}

func (a *API) authenticateTicket(r *http.Request) (auth.Principal, error) {
	q, err := queryValues(r, "path", "base_path", "context_id", "ticket")
	if err != nil {
		return auth.Principal{}, err
	}
	remote, err := appfiles.Resolve(q.Get("path"), file.Scope{BasePath: q.Get("base_path"), ContextID: q.Get("context_id")})
	if err != nil {
		return auth.Principal{}, err
	}
	owner, err := a.deps.Content.Redeem(r.Context(), q.Get("ticket"), r.PathValue("account_id"), remote)
	if err != nil {
		return auth.Principal{}, err
	}
	return a.deps.Auth.Principal(r.Context(), owner)
}

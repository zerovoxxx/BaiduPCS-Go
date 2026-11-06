package httpapi

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func artifactRange(value string, size int64) (int64, int64, error) {
	if !strings.HasPrefix(value, "bytes=") {
		return 0, 0, &httpError{400, "INVALID_RANGE", "Range格式无效"}
	}
	if strings.Contains(value, ",") {
		return 0, 0, &httpError{400, "MULTIPLE_RANGES_UNSUPPORTED", "只支持单个Range"}
	}
	start, end, ok := strings.Cut(strings.TrimPrefix(value, "bytes="), "-")
	if !ok || start == "" && end == "" {
		return 0, 0, &httpError{400, "INVALID_RANGE", "Range格式无效"}
	}
	number := func(v string) (int64, error) {
		if v == "" {
			return 0, fmt.Errorf("empty")
		}
		for _, c := range v {
			if c < '0' || c > '9' {
				return 0, fmt.Errorf("invalid")
			}
		}
		return strconv.ParseInt(v, 10, 64)
	}
	var first, last int64
	if start == "" {
		suffix, err := number(end)
		if err != nil {
			return 0, 0, &httpError{400, "INVALID_RANGE", "Range格式无效"}
		}
		if suffix == 0 || size == 0 {
			return 0, 0, &httpError{416, "RANGE_NOT_SATISFIABLE", "Range不满足文件长度"}
		}
		first = max(size-suffix, 0)
		last = size - 1
	} else {
		var err error
		first, err = number(start)
		if err != nil {
			return 0, 0, &httpError{400, "INVALID_RANGE", "Range格式无效"}
		}
		last = size - 1
		if end != "" {
			last, err = number(end)
			if err != nil {
				return 0, 0, &httpError{400, "INVALID_RANGE", "Range格式无效"}
			}
			if last < first {
				return 0, 0, &httpError{400, "INVALID_RANGE", "Range结束位置小于开始位置"}
			}
			last = min(last, size-1)
		}
		if first >= size {
			return 0, 0, &httpError{416, "RANGE_NOT_SATISFIABLE", "Range不满足文件长度"}
		}
	}
	return first, last, nil
}
func (a *API) serveArtifactContent(w http.ResponseWriter, r *http.Request) error {
	meta, content, err := a.deps.Artifacts.Open(r.Context(), principal(r.Context()), r.PathValue("artifact_id"))
	if err != nil {
		return err
	}
	defer content.Close()
	modified, err := time.Parse(time.RFC3339Nano, meta.CreatedAt)
	if err != nil {
		return err
	}
	etag := `"` + meta.ID + "-" + strconv.FormatInt(meta.Size, 10) + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Last-Modified", modified.UTC().Format(http.TimeFormat))
	w.Header().Set("Accept-Ranges", "bytes")
	ranges := r.Header.Values("Range")
	if len(ranges) > 1 {
		return &httpError{400, "MULTIPLE_RANGES_UNSUPPORTED", "只支持单个Range"}
	}
	ifRanges := r.Header.Values("If-Range")
	if len(ifRanges) > 1 {
		return invalid("If-Range不能重复")
	}
	first, last, status := int64(0), meta.Size-1, 200
	if len(ranges) == 1 {
		// Validate syntax even when If-Range does not match. The response may still
		// fall back to the full representation for a syntactically valid range.
		from, to, rangeErr := artifactRange(ranges[0], meta.Size)

		match := true
		if len(ifRanges) == 1 {
			match = ifRanges[0] == etag
			if date, e := http.ParseTime(ifRanges[0]); e == nil {
				match = !modified.Truncate(time.Second).After(date)
			}
		}
		if rangeErr != nil {
			if e, ok := rangeErr.(*httpError); ok && e.status == 416 && !match {
				rangeErr = nil
			} else {
				if e, ok := rangeErr.(*httpError); ok && e.status == 416 {
					w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", meta.Size))
				}
				return rangeErr
			}
		}
		if match {
			first, last, status = from, to, 206
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", first, last, meta.Size))
		}
	}
	length := max(last-first+1, 0)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(length, 10))
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": meta.Filename}))
	if _, err = content.Seek(first, io.SeekStart); err != nil {
		return err
	}
	w.WriteHeader(status)
	if r.Method == "HEAD" {
		return nil
	}
	controller := http.NewResponseController(w)
	buffer := make([]byte, 64<<10)
	remaining := length
	defer controller.SetWriteDeadline(time.Time{})
	for remaining > 0 {
		if r.Context().Err() != nil {
			return nil
		}
		n, readErr := content.Read(buffer[:min(int64(len(buffer)), remaining)])
		if n > 0 {
			_ = controller.SetWriteDeadline(time.Now().Add(30 * time.Second))
			written, writeErr := w.Write(buffer[:n])
			remaining -= int64(written)
			if writeErr != nil || written != n {
				a.deps.Logger.Warn("content stream terminated", "request_id", RequestID(r.Context()), "code", "CONTENT_WRITE_FAILED")
				return nil
			}
		}
		if readErr != nil || n == 0 {
			if remaining > 0 {
				a.deps.Logger.Warn("content stream terminated", "request_id", RequestID(r.Context()), "code", "CONTENT_READ_FAILED")
			}
			return nil
		}
	}
	return nil
}

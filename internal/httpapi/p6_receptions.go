package httpapi

import (
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/staging"
	"mime"
	"net/http"
	"strconv"
	"time"
)

func receptionValue(v staging.Reception) any {
	var stage any
	if v.State == "completed" {
		stage = v.StageID
	}
	return map[string]any{"session_id": v.ID, "filename": v.Filename, "size_bytes": strconv.FormatInt(v.Size, 10), "offset_bytes": strconv.FormatInt(v.Offset, 10), "state": v.State, "staged_file_id": stage, "expires_at": time.Unix(v.Expires, 0).UTC().Format(time.RFC3339Nano)}
}
func (a *API) serveReceptions(id string, w http.ResponseWriter, r *http.Request) error {
	ctx, p, sessionID := r.Context(), principal(r.Context()), r.PathValue("session_id")
	if id == "UPL07" {
		kind, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if e != nil || kind != "application/octet-stream" {
			return &httpError{415, "UNSUPPORTED_MEDIA_TYPE", "需要application/octet-stream"}
		}
		headers := r.Header.Values("Upload-Offset")
		if len(headers) != 1 {
			return invalid("需要唯一Upload-Offset")
		}
		offset, e := strconv.ParseInt(headers[0], 10, 64)
		if e != nil || offset < 0 || strconv.FormatInt(offset, 10) != headers[0] {
			return invalid("Upload-Offset无效")
		}
		v, e := a.deps.Staging.PatchReception(ctx, p, sessionID, offset, r.Body)
		if e != nil {
			return e
		}
		w.Header().Set("Upload-Offset", strconv.FormatInt(v.Offset, 10))
		a.success(w, r, receptionValue(v))
		return nil
	}
	if id == "UPL06" {
		v, e := a.deps.Staging.Reception(ctx, p, sessionID)
		if e != nil {
			return e
		}
		w.Header().Set("Upload-Offset", strconv.FormatInt(v.Offset, 10))
		a.success(w, r, receptionValue(v))
		return nil
	}
	key, e := idempotencyKey(r)
	if e != nil {
		return e
	}
	if id == "UPL05" {
		var in struct {
			Name     string            `json:"filename"`
			Size     *string           `json:"size_bytes"`
			Expected *staging.Expected `json:"expected_checksum,omitempty"`
		}
		if e := decodeRequest(w, r, &in); e != nil {
			return e
		}
		size, e := decimal(in.Size, false)
		if e != nil || size == nil {
			return invalid("接收长度无效")
		}
		if *size > 256<<20 {
			return &httpError{413, "STAGING_SIZE_LIMIT", "单文件最多256MiB"}
		}
		if in.Expected != nil {
			n := 16
			if in.Expected.Algorithm == "sha256" {
				n = 32
			} else if in.Expected.Algorithm != "md5" {
				return invalid("期望摘要算法无效")
			}
			if !validDigest(in.Expected.Value, n) {
				return invalid("期望摘要无效")
			}
		}
		v, e := a.deps.Staging.CreateReception(ctx, p, in.Name, *size, in.Expected, key)
		if e != nil {
			return e
		}
		w.Header().Set("Location", "/api/v1/staging-upload-sessions/"+v.ID)
		a.respond(w, r, 201, receptionValue(v))
		return nil
	}
	if r.ContentLength > 0 {
		return invalid("此操作不接受请求体")
	}
	if id == "UPL09" {
		if e := a.deps.Staging.DeleteReception(ctx, p, sessionID); e != nil {
			return e
		}
		w.WriteHeader(204)
		return nil
	}
	v, e := a.deps.Staging.CompleteReception(ctx, p, sessionID)
	if e != nil {
		return e
	}
	w.Header().Set("Location", "/api/v1/staging-files/"+v.ID)
	a.respond(w, r, 201, publicStage(v))
	return nil
}

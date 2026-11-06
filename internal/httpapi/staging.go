package httpapi

import (
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/staging"
	"io"
	"net/http"
)

func publicStage(f staging.File) any {
	return map[string]any{"staged_file_id": f.ID, "filename": f.Filename, "size_bytes": uintString(uint64(f.Size)), "state": f.State, "reference_count": uintString(f.References), "expires_at": f.ExpiresAt}
}
func (a *API) serveStaging(id string, w http.ResponseWriter, r *http.Request) error {
	p := principal(r.Context())
	if id == "UPL03" {
		f, err := a.deps.Staging.Get(r.Context(), p, r.PathValue("file_id"))
		if err != nil {
			return err
		}
		a.success(w, r, publicStage(f))
		return nil
	}
	key, err := idempotencyKey(r)
	if err != nil {
		return err
	}
	if id == "UPL04" {
		if err = a.deps.Staging.Delete(r.Context(), p, r.PathValue("file_id"), key); err != nil {
			return err
		}
		w.WriteHeader(204)
		return nil
	}
	mr, err := r.MultipartReader()
	if err != nil {
		return &httpError{415, "UNSUPPORTED_MEDIA_TYPE", "需要multipart/form-data"}
	}
	filename := ""
	seenName := false
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			return invalid("缺少file part")
		}
		if err != nil {
			return invalid("multipart格式无效")
		}
		switch part.FormName() {
		case "filename":
			if seenName {
				return invalid("filename不能重复")
			}
			value, err := io.ReadAll(io.LimitReader(part, 4097))
			if err != nil || len(value) > 4096 {
				return invalid("filename无效")
			}
			filename = string(value)
			seenName = true
		case "file":
			if filename == "" {
				filename = part.FileName()
			}
			if filename == "" {
				filename = "upload.bin"
			}
			f, err := a.deps.Staging.Receive(r.Context(), p, filename, key, part, func() (string, error) {
				for {
					next, err := mr.NextPart()
					if err == io.EOF {
						return filename, nil
					}
					if err != nil {
						return "", invalid("multipart格式无效")
					}
					if next.FormName() != "filename" || seenName {
						return "", invalid("只允许单个file和filename part")
					}
					value, err := io.ReadAll(io.LimitReader(next, 4097))
					if err != nil || len(value) > 4096 {
						return "", invalid("filename无效")
					}
					filename = string(value)
					seenName = true
				}
			})
			if err != nil {
				return err
			}
			w.Header().Set("Location", "/api/v1/staging-files/"+f.ID)
			a.respond(w, r, 201, publicStage(f))
			return nil
		default:
			return invalid("未知multipart字段")
		}
	}
}

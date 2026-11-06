package transfers

import (
	"encoding/json"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/transfer"
)

// MetricsSnapshot never takes mu: a reconciliation may hold it across upstream
// IO. The dispatcher samples at 100ms; storage stalls can make the sample older.
func (s *Service) MetricsSnapshot() (queued, executing int64) {
	return s.queued.Load(), int64(len(s.workers))
}

func confirmedBytes(r transfer.Record) (int64, int64) {
	if r.ScanResult != nil {
		return max(0, r.ScanResult.Transferred), max(0, r.ScanResult.Written)
	}
	var cp struct {
		Phase        string
		Offset, Size int64
		Skipped      bool
		Upload       *struct {
			BlockList []struct {
				Range    struct{ Begin, End int64 }
				CheckSum string `json:"checksum"`
			} `json:"block_list"`
		}
	}
	_ = json.Unmarshal(r.Checkpoint, &cp)
	n := max(0, cp.Offset)
	if r.Request.Kind == "upload" {
		n = 0
		if cp.Upload != nil {
			for _, b := range cp.Upload.BlockList {
				if b.CheckSum != "" {
					n += max(0, b.Range.End-b.Range.Begin)
				}
			}
		}
		if cp.Phase == "done" && !cp.Skipped {
			n = max(0, cp.Size)
		}
	}
	if r.Request.Kind == "download" {
		var options struct {
			Test bool `json:"test"`
		}
		_ = json.Unmarshal(r.EffectiveOptions, &options)
		if options.Test {
			return n, 0
		}
		return n, n
	}
	return n, 0
}

// Package tool holds the immutable selection for P6 integrity and local jobs.
package tool

import (
	"encoding/json"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/auth"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
)

type Checksum struct {
	Size     string   `json:"size_bytes"`
	MD5      *string  `json:"md5"`
	SliceMD5 *string  `json:"slice_md5"`
	CRC32    *string  `json:"crc32"`
	Blocks   []string `json:"block_list,omitempty"`
	Status   string   `json:"md5_status"`
}
type LocalFile struct {
	RootID string `json:"root_id"`
	Path   string `json:"relative_path"`
}
type Unit struct {
	Path  string
	Local LocalFile
}
type Plan struct {
	Units       []Unit
	Options     json.RawMessage
	Destination *configuration.Directory
	// KeyRef identifies process-local secret memory, never a password or key.
	KeyRef string `json:",omitempty"`
}
type ResultItem struct {
	Input    string
	Checksum *Checksum
	Matches  *bool
	Failure  *fault.Error
}

func Operation(kind string) string {
	switch kind {
	case "checksum_fix":
		return "INT02"
	case "local_checksum":
		return "INT03"
	case "file_verification":
		return "INT04"
	case "export":
		return "EXP01"
	case "local_encryption":
		return "LOC02"
	case "local_decryption":
		return "LOC03"
	}
	return ""
}
func Permission(p auth.Principal, owner, accountID, kind string, plan Plan) error {
	deny := func() error {
		return fault.New(fault.Forbidden, "FORBIDDEN", "无权访问工具任务或其来源和目标")
	}
	if owner != p.ID && !p.Has("admin") {
		return deny()
	}
	if accountID != "" && !p.AllowsAccount(accountID) {
		return deny()
	}
	scope := "read"
	switch kind {
	case "checksum_fix":
		scope = "write"
	case "file_verification":
		scope = "transfer"
	case "local_checksum", "local_encryption", "local_decryption":
		scope = "local.read"
	}
	if !p.Has(scope) {
		return deny()
	}
	for _, u := range plan.Units {
		if u.Local.RootID != "" && (!p.Has("local.read") || !p.AllowsRoot(u.Local.RootID)) {
			return deny()
		}
	}
	if plan.Destination != nil && (!p.Has("local.write") || !p.AllowsRoot(plan.Destination.RootID)) {
		return deny()
	}
	return nil
}

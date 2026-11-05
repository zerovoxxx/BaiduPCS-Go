package write

import (
	"encoding/json"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/file"
)

type Unit struct {
	PriorIDs                []string
	BeforeWindowComplete    bool
	From, To, ID, SourceURL string
	Before                  *file.Entry
	TargetBefore            *file.Entry
	PreparationError        *fault.Error
}
type Options struct {
	Policy        string `json:"conflict_policy,omitempty"`
	IgnoreMissing bool   `json:"ignore_missing,omitempty"`
	Collect       bool   `json:"collect,omitempty"`
	Randomize     bool   `json:"randomize_name,omitempty"`
	Download      bool   `json:"download_after,omitempty"`
	Destination   *struct {
		RootID    string `json:"root_id"`
		Directory string `json:"directory"`
	} `json:"download_destination,omitempty"`
	DownloadOptions json.RawMessage `json:"download_options,omitempty"`
}
type Plan struct {
	AccountUK string
	Units     []Unit
	Options   Options
	Share     *Access
	ChildID   string
}
type Outcome struct {
	Path, ID, OfflineID string
	IDs, Paths          []string
	Affected            uint64
	Skipped             bool
}
type Share struct {
	ID, Link, Password string
	Combined           bool
	Fields             map[string]any
}

// Access is private, encrypted inspection state. Tokens and randsk must never
// enter a public job, event, URL log or DTO.
type Access struct {
	Feature, ShareID, OwnerUK, Token, Password, Owner, AccountID, ExpiresAt string
	CredentialRevision                                                      uint64
	Randsk                                                                  string
	RootIDs                                                                 []string
	Entries                                                                 []file.Entry
}
type Inspection struct {
	ID     string
	Access Access
}
type Offline struct {
	ID                                      string
	Status                                  int
	StatusText                              string
	Size, Finished, Created, Started, Ended int64
	Path, URL, Name                         string
	Type                                    int
	Files                                   []OfflineFile
	Missing                                 bool
}
type OfflineFile struct {
	Name string
	Size int64
}
type Recycled struct {
	Entry    file.Entry
	LeftTime string
}

func Operation(kind string) string {
	switch kind {
	case "mkdir":
		return "FIL07"
	case "file_delete":
		return "FIL08"
	case "file_copy":
		return "FIL09"
	case "file_move":
		return "FIL10"
	case "file_rename":
		return "FIL11"
	case "share_cancel":
		return "SHR04"
	case "share_transfer":
		return "TRF02"
	case "offline_add":
		return "OFF01"
	case "offline_cancel":
		return "OFF04"
	case "offline_delete":
		return "OFF05"
	case "offline_clear":
		return "OFF06"
	case "recycle_restore":
		return "REC02"
	case "recycle_delete":
		return "REC03"
	case "recycle_clear":
		return "REC04"
	}
	return ""
}

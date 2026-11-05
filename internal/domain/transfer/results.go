package transfer

import "github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"

type Worker struct {
	ID, State           string
	Offset, Transferred int64
}
type Item struct {
	Input, RemoteEffect, ResultID, OfflineID string
	Affected                                 uint64
	Workers                                  []Worker
	ID                                       string
	Index                                    uint64
	Path, RootID, RelativePath, State        string
	Attempts                                 int
	Size                                     *int64
	Transferred, Written                     int64
	Failure                                  *fault.Error
	CheckpointAvailable                      bool
	Offset                                   *int64
}
type Counts struct{ Total, Processed, Succeeded, Failed, Skipped, Cancelled uint64 }
type ScanResult struct {
	MutationItems             []Item
	ChildJobIDs               []string
	Affected                  uint64
	Transferred, Written      int64
	Counts                    Counts
	Complete                  bool
	Truncation                string
	Files, Directories, Bytes uint64
	ArtifactIDs               []string
}
type Event struct {
	Sequence         uint64
	Type, OccurredAt string
	Record           *Record
	Item             *Item
}
type EventBatch struct {
	Events       []Event
	Head, Oldest uint64
}
type Filter struct {
	AccountID, Kind, State, After, Before string
	Owner                                 string `json:"-"`
}

func Terminal(state string) bool {
	return state == "succeeded" || state == "partially_succeeded" || state == "failed" || state == "cancelled"
}

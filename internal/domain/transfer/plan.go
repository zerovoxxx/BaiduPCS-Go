package transfer

// Batch is the canonical, unresolved selection. Plan is the immutable set of
// execution units, stored encrypted with the job, never accepted from clients.
type Source struct {
	Type         string `json:"type"`
	RootID       string `json:"root_id,omitempty"`
	RelativePath string `json:"relative_path,omitempty"`
	StagedFileID string `json:"staged_file_id,omitempty"`
}
type Batch struct {
	RetryUnits []Unit   `json:"retry_units,omitempty"`
	Sources    []Source `json:"sources,omitempty"`
	Paths      []string `json:"paths,omitempty"`
	Directory  string   `json:"directory,omitempty"`
	DryRun     bool     `json:"dry_run,omitempty"`
}
type Unit struct {
	RootID, RelativePath, RemotePath string
	Directory                        bool
	Size, Mtime                      int64
	SHA256                           string
	StagedFileID                     string
}
type Plan struct {
	Units  []Unit
	DryRun bool
}

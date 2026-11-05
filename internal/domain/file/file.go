package file

// Entry contains upstream facts; nil means the upstream did not report a value.
type Entry struct {
	ID, Path, Name        string
	AppID                 *string
	IsDir                 bool
	Size                  int64
	CreatedAt, ModifiedAt *string
	MD5                   *string
	MD5Status             string
	Blocks                []string
	HasSubdirectories     *bool
}
type Scope struct{ BasePath, ContextID string }
type ListOptions struct {
	Page, PageSize     int
	OrderBy, Direction string
}
type ScanOptions struct {
	Mode        string `json:"mode"`
	Depth       int    `json:"depth"`
	IncludeIDs  bool   `json:"include_ids"`
	MaxNodes    string `json:"max_nodes"`
	MaxDuration int    `json:"max_duration_seconds"`
}
type ScanPlan struct {
	Paths   []string
	Options ScanOptions
}

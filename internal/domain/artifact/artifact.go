package artifact

type Metadata struct {
	ID, JobID, Filename, MediaType, CreatedAt, ExpiresAt string
	Size                                                 int64
	Complete                                             bool
	State                                                string
}
type Root struct {
	ID, Label, State   string
	Readable, Writable bool
	Available          *uint64
}

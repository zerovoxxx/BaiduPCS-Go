package staging

type File struct {
	ID, Owner, Filename, State, ExpiresAt string
	Size                                  int64
	References                            uint64
}

type Expected struct {
	Algorithm string `json:"algorithm"`
	Value     string `json:"value"`
}
type Reception struct {
	ID, Owner, Filename, StageID, State string
	Size, Offset, Expires               int64
	Expected                            *Expected
	Revision                            uint64
}

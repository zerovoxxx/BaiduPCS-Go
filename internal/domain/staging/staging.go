package staging

type File struct {
	ID, Owner, Filename, State, ExpiresAt string
	Size                                  int64
	References                            uint64
}

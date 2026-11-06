package content

type Metadata struct {
	Path, Name, MD5 string
	Size, Mtime     int64
}
type Link struct {
	URL, Reference, UserAgent string
	RequiresCookie            bool
}

type Ticket struct {
	TTLSeconds             int
	Owner, AccountID, Path string
	CredentialRevision     uint64
	Expires                int64
}

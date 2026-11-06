package account

// Credentials are supplied by the credential adapter. They must never be exposed
// through HTTP DTOs or logs. Every factory call copies this canonical cookie set.
type Credentials struct {
	AccountID   string            `json:"-"`
	Revision    uint64            `json:"-"`
	UID         uint64            `json:"-"`
	AccessToken string            `json:"-"`
	Cookies     map[string]string `json:"-"`
}

func (c Credentials) Clone() Credentials {
	copied := make(map[string]string, len(c.Cookies))
	for k, v := range c.Cookies {
		copied[k] = v
	}
	c.Cookies = copied
	return c
}

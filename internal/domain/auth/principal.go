package auth

// Principal is a value snapshot of a configured service identity, not a Baidu login.
type Principal struct {
	ID       string
	Scopes   []string
	Accounts []string
	Roots    []string
}

func (p Principal) Clone() Principal {
	p.Scopes = append([]string(nil), p.Scopes...)
	p.Accounts = append([]string(nil), p.Accounts...)
	p.Roots = append([]string(nil), p.Roots...)
	return p
}
func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
func (p Principal) Has(scope string) bool {
	return contains(p.Scopes, "admin") || contains(p.Scopes, scope)
}
func (p Principal) AllowsAccount(id string) bool {
	return contains(p.Accounts, "*") || contains(p.Accounts, id)
}
func (p Principal) AllowsRoot(id string) bool { return contains(p.Roots, "*") || contains(p.Roots, id) }

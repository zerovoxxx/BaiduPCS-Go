package requester

import (
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/publicsuffix"
)

// CookieEntry is the persistent form of a cookie. HostOnly distinguishes a
// host cookie from a Domain cookie; a Cookie header alone loses this information.
type CookieEntry struct {
	Name     string        `json:"name"`
	Value    string        `json:"value"`
	Domain   string        `json:"domain"`
	Path     string        `json:"path"`
	HostOnly bool          `json:"host_only,omitempty"`
	Secure   bool          `json:"secure,omitempty"`
	HTTPOnly bool          `json:"http_only,omitempty"`
	SameSite http.SameSite `json:"same_site,omitempty"`
	Expires  int64         `json:"expires,omitempty"`
}

func (e CookieEntry) String() string {
	return fmt.Sprintf("Cookie{%s @ %s%s [redacted]}", e.Name, e.Domain, e.Path)
}

// CookieJar uses the standard library for request matching, and records the
// attributes which http.CookieJar.Cookies intentionally does not return.
type CookieJar struct {
	mu      sync.Mutex
	jar     *cookiejar.Jar
	entries map[string]CookieEntry
}

var _ http.CookieJar = (*CookieJar)(nil)

func NewCookieJar(entries []CookieEntry) (*CookieJar, error) {
	inner, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	if err != nil {
		return nil, err
	}
	j := &CookieJar{jar: inner, entries: make(map[string]CookieEntry)}
	for _, entry := range entries {
		if entry.Domain == "" || strings.ContainsAny(entry.Domain, "/:@ \\?#") || !strings.HasPrefix(entry.Path, "/") {
			return nil, fmt.Errorf("invalid saved cookie attributes")
		}
		domain := strings.ToLower(strings.TrimPrefix(entry.Domain, "."))
		cookie := &http.Cookie{Name: entry.Name, Value: entry.Value, Path: entry.Path,
			Secure: entry.Secure, HttpOnly: entry.HTTPOnly, SameSite: entry.SameSite}
		if !entry.HostOnly {
			cookie.Domain = domain
		}
		if entry.Expires != 0 {
			cookie.Expires = time.Unix(entry.Expires, 0)
		}
		if cookie.Valid() != nil {
			return nil, fmt.Errorf("invalid saved cookie")
		}
		u := &url.URL{Scheme: "https", Host: domain, Path: entry.Path}
		if u.Hostname() != domain {
			return nil, fmt.Errorf("invalid saved cookie host")
		}
		if !entry.HostOnly {
			if suffix, _ := publicsuffix.PublicSuffix(domain); suffix == domain {
				return nil, fmt.Errorf("saved cookie domain is a public suffix")
			}
		}
		j.SetCookies(u, []*http.Cookie{cookie})
	}
	return j, nil
}

func (j *CookieJar) Cookies(u *url.URL) []*http.Cookie { return j.jar.Cookies(u) }

func (j *CookieJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.jar.SetCookies(u, cookies)
	now := time.Now()
	host := strings.ToLower(u.Hostname())
	for _, cookie := range cookies {
		if cookie.Valid() != nil {
			continue
		}
		domain := strings.ToLower(strings.TrimPrefix(cookie.Domain, "."))
		hostOnly := domain == ""
		if hostOnly {
			domain = host
		} else {
			if host != domain && !strings.HasSuffix(host, "."+domain) {
				continue
			}
			if suffix, _ := publicsuffix.PublicSuffix(domain); suffix == domain {
				continue
			}
		}
		path := cookie.Path
		if !strings.HasPrefix(path, "/") {
			path = "/"
			if i := strings.LastIndex(u.Path, "/"); i > 0 {
				path = u.Path[:i]
			}
		}
		key := domain + "\x00" + path + "\x00" + cookie.Name
		expires := cookie.Expires
		if cookie.MaxAge > 0 {
			// Match cookiejar's Max-Age precedence without overflowing Duration.
			expires = now.AddDate(100, 0, 0)
			if int64(cookie.MaxAge) < 100*365*24*60*60 {
				expires = now.Add(time.Duration(cookie.MaxAge) * time.Second)
			}
		}
		if cookie.MaxAge < 0 || (!expires.IsZero() && !expires.After(now)) {
			delete(j.entries, key)
			continue
		}
		entry := CookieEntry{Name: cookie.Name, Value: cookie.Value, Domain: domain, Path: path,
			HostOnly: hostOnly, Secure: cookie.Secure, HTTPOnly: cookie.HttpOnly, SameSite: cookie.SameSite}
		if !expires.IsZero() {
			entry.Expires = expires.Unix()
		}
		j.entries[key] = entry
	}
}

// Entries returns a detached snapshot, excluding expired cookies.
func (j *CookieJar) Entries() []CookieEntry {
	j.mu.Lock()
	defer j.mu.Unlock()
	entries := make([]CookieEntry, 0, len(j.entries))
	now := time.Now().Unix()
	for key, entry := range j.entries {
		if entry.Expires != 0 && entry.Expires <= now {
			delete(j.entries, key)
			continue
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, k int) bool {
		a, b := entries[i], entries[k]
		if a.Domain != b.Domain {
			return a.Domain < b.Domain
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Name < b.Name
	})
	return entries
}

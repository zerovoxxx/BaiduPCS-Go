//go:build (darwin && (amd64 || arm64)) || (linux && (amd64 || arm64)) || (windows && amd64)

package persistence

import (
	"database/sql"
	"net/url"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

func Supported() bool { return true }
func openDatabase(file string) (*sql.DB, error) {
	path := filepath.ToSlash(file)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	uri := &url.URL{Scheme: "file", Path: path}
	q := uri.Query()
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(1)")
	uri.RawQuery = q.Encode()
	return sql.Open("sqlite", uri.String())
}

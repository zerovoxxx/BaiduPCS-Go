//go:build !((darwin && (amd64 || arm64)) || (linux && (amd64 || arm64)) || (windows && amd64))

package persistence

import (
	"database/sql"
	"errors"
)

func Supported() bool { return false }
func openDatabase(string) (*sql.DB, error) {
	return nil, errors.New("HTTP server storage is unsupported on this target; CLI remains available")
}

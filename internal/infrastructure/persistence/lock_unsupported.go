//go:build !((darwin && (amd64 || arm64)) || (linux && (amd64 || arm64)) || (windows && amd64))

package persistence

import "errors"

type DirectoryLock struct{}

func Lock(string) (*DirectoryLock, error) {
	return nil, errors.New("HTTP server is unsupported on this target")
}
func (*DirectoryLock) Close() error { return nil }

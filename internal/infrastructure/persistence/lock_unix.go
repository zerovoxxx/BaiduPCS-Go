//go:build (darwin && (amd64 || arm64)) || (linux && (amd64 || arm64))

package persistence

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
)

type DirectoryLock struct{ file *os.File }

func Lock(directory string) (*DirectoryLock, error) {
	file, err := os.OpenFile(filepath.Join(directory, "service.lock"), os.O_CREATE|os.O_RDWR|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		file.Close()
		return nil, errors.New("service data directory is already locked")
	}
	return &DirectoryLock{file: file}, nil
}
func (l *DirectoryLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}

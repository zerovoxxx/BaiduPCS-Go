//go:build windows && amd64

package persistence

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
)

type DirectoryLock struct {
	file       *os.File
	overlapped windows.Overlapped
}

func Lock(directory string) (*DirectoryLock, error) {
	path := filepath.Join(directory, "service.lock")
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return nil, errors.New("service lock must be a regular file")
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	lock := &DirectoryLock{file: file}
	if err = windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &lock.overlapped); err != nil {
		file.Close()
		return nil, errors.New("service data directory is already locked")
	}
	return lock, nil
}
func (l *DirectoryLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}

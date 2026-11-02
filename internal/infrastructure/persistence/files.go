package persistence

import (
	"crypto/rand"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

func PrepareDirectory(directory string) (string, error) {
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return "", err
	}
	if err = os.MkdirAll(absolute, 0700); err != nil {
		return "", err
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("service data directory must be a real directory")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return "", errors.New("service data directory permissions must be 0700")
	}
	return filepath.EvalSymlinks(absolute)
}
func ReadPrivateFile(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit || runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("secret file must be a private regular file within size limit")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(io.LimitReader(file, limit+1))
}
func WritePrivateFile(path string, value []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		file.Close()
		if !ok {
			os.Remove(path)
		}
	}()
	if _, err = file.Write(value); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	ok = true
	return nil
}
func MasterKey(directory string) ([]byte, error) {
	name := filepath.Join(directory, "master.key")
	key, err := ReadPrivateFile(name, 32)
	if err == nil {
		if len(key) != 32 {
			return nil, errors.New("invalid service master key")
		}
		return key, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	if _, err := os.Lstat(filepath.Join(directory, "service.db")); err == nil || !os.IsNotExist(err) {
		return nil, errors.New("existing database requires its original master.key")
	}
	key = make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		return nil, err
	}
	if err = WritePrivateFile(name, key); err != nil {
		return nil, err
	}
	return key, nil
}

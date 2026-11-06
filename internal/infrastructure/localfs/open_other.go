//go:build !unix

package localfs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

func (r *Registry) OpenFile(ctx context.Context, id, relative string, flags int, mode os.FileMode) (*os.File, error) {
	name, err := r.File(ctx, id, relative, flags&(os.O_WRONLY|os.O_RDWR|os.O_CREATE) != 0)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(name, flags, mode)
	if err != nil {
		return nil, err
	}
	current, err := r.File(ctx, id, relative, flags&(os.O_WRONLY|os.O_RDWR|os.O_CREATE) != 0)
	a, aErr := os.Lstat(current)
	b, bErr := f.Stat()
	if err != nil || aErr != nil || bErr != nil || !b.Mode().IsRegular() || !os.SameFile(a, b) {
		f.Close()
		return nil, errors.New("local file changed during open")
	}
	return f, nil
}
func (r *Registry) CommitFile(ctx context.Context, id, source, target string, file *os.File) error {
	if filepath.Dir(source) != filepath.Dir(target) {
		return errors.New("commit must use one parent directory")
	}
	src, err := r.File(ctx, id, source, true)
	if err != nil {
		return err
	}
	dst, err := r.File(ctx, id, target, true)
	if err != nil {
		return err
	}
	a, err := os.Lstat(src)
	if err != nil {
		return err
	}
	b, err := file.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(a, b) {
		return errors.New("commit source changed")
	}
	// Windows does not allow unlinking the open temporary handle used here.
	if err = file.Close(); err != nil {
		return err
	}
	if err = os.Link(src, dst); err != nil {
		return err
	}
	return os.Remove(src)
}
func (r *Registry) RemoveFile(ctx context.Context, id, relative string) error {
	name, err := r.File(ctx, id, relative, true)
	if err != nil {
		return err
	}
	err = os.Remove(name)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (r *Registry) RemoveOpenedFile(ctx context.Context, id, relative string, f *os.File) error {
	p, e := r.File(ctx, id, relative, true)
	if e != nil {
		return e
	}
	a, e := os.Lstat(p)
	if e != nil {
		return e
	}
	b, e := f.Stat()
	if e != nil {
		return e
	}
	if !os.SameFile(a, b) {
		return os.ErrInvalid
	}
	return os.Remove(p)
}

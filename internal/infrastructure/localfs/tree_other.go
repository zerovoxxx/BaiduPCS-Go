//go:build !unix

package localfs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (r *Registry) openTree(ctx context.Context, id, relative string) (*os.File, error) {
	root, err := r.treeRoot(ctx, id, relative, false)
	if err != nil {
		return nil, err
	}
	p := root.Path
	for _, part := range strings.Split(relative, "/") {
		if part == "" || part == "." {
			continue
		}
		p = filepath.Join(p, part)
		info, e := os.Lstat(p)
		if e != nil {
			return nil, e
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, os.ErrPermission
		}
	}
	return os.Open(p)
}
func (r *Registry) MkdirAll(ctx context.Context, id, relative string) error {
	root, err := r.treeRoot(ctx, id, relative, true)
	if err != nil {
		return err
	}
	p := root.Path
	for _, part := range strings.Split(relative, "/") {
		if part == "" || part == "." {
			continue
		}
		p = filepath.Join(p, part)
		err = os.Mkdir(p, 0700)
		if err != nil && !os.IsExist(err) {
			return err
		}
		info, e := os.Lstat(p)
		if e != nil {
			return e
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return os.ErrPermission
		}
	}
	return nil
}
func (r *Registry) ReplaceFile(ctx context.Context, id, source, target string, f *os.File) error {
	src, err := r.File(ctx, id, source, true)
	if err != nil {
		return err
	}
	dst, err := r.File(ctx, id, target, true)
	if err != nil {
		return err
	}
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	opened, err := f.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(info, opened) {
		return os.ErrPermission
	}
	return os.Rename(src, dst)
}

func SetFileMtime(f *os.File, seconds int64) error {
	return os.Chtimes(f.Name(), time.Unix(seconds, 0), time.Unix(seconds, 0))
}

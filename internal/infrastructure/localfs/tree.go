package localfs

import (
	"context"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"os"
	"path/filepath"
)

func (r *Registry) treeRoot(ctx context.Context, id, relative string, write bool) (Root, error) {
	root, ok := r.roots[id]
	if ctx.Err() != nil {
		return root, ctx.Err()
	}
	if !ok || !configuration.ValidRelative(relative) || write && !root.Write || !write && !root.Read {
		return root, fault.New(fault.Forbidden, "LOCAL_PATH_FORBIDDEN", "本地目录不可访问")
	}
	return root, nil
}

// Walk opens each directory through the same no-follow boundary as files.
func (r *Registry) Walk(ctx context.Context, id, relative string, visit func(string, os.FileInfo) error) error {
	if _, err := r.treeRoot(ctx, id, relative, false); err != nil {
		return err
	}
	var walk func(string) error
	walk = func(rel string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		f, err := r.openTree(ctx, id, rel)
		if err != nil {
			return err
		}
		info, err := f.Stat()
		if err != nil {
			f.Close()
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			f.Close()
			return fault.New(fault.Forbidden, "LOCAL_PATH_FORBIDDEN", "上传目录包含非普通文件")
		}
		if err = visit(rel, info); err != nil {
			f.Close()
			return err
		}
		if !info.IsDir() {
			return f.Close()
		}
		children, err := f.ReadDir(-1)
		f.Close()
		if err != nil {
			return err
		}
		for _, child := range children {
			if err = walk(filepath.ToSlash(filepath.Join(rel, child.Name()))); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(relative)
}

package localfs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
)

type Root struct {
	ID    string `json:"id"`
	Path  string `json:"path"`
	Read  bool   `json:"read"`
	Write bool   `json:"write"`
}
type Registry struct{ roots map[string]Root }

func New(roots []Root, protectedPaths ...string) (*Registry, error) {
	registry := &Registry{roots: map[string]Root{}}
	for _, root := range roots {
		if !configuration.ValidID(root.ID) || !filepath.IsAbs(root.Path) {
			return nil, errors.New("root requires a valid ID and absolute path")
		}
		if _, ok := registry.roots[root.ID]; ok {
			return nil, errors.New("duplicate root ID")
		}
		resolved, err := filepath.EvalSymlinks(root.Path)
		if err != nil {
			return nil, errors.New("root directory is unavailable")
		}
		info, err := os.Stat(resolved)
		if err != nil || !info.IsDir() {
			return nil, errors.New("root must be a directory")
		}
		root.Path = filepath.Clean(resolved)
		for _, protected := range protectedPaths {
			if protected == "" {
				continue
			}
			absolute, err := filepath.Abs(protected)
			if err != nil {
				return nil, errors.New("protected service path is invalid")
			}
			target, err := filepath.EvalSymlinks(absolute)
			if err != nil {
				return nil, errors.New("protected service path is unavailable")
			}
			for _, pair := range [][2]string{{root.Path, target}, {target, root.Path}} {
				rel, err := filepath.Rel(pair[0], pair[1])
				if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
					return nil, errors.New("local roots must be separate from service credentials/configuration")
				}
			}
		}
		registry.roots[root.ID] = root
	}
	return registry, nil
}
func (r *Registry) ValidateDestination(ctx context.Context, d configuration.Directory) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	root, ok := r.roots[d.RootID]
	if !ok || !root.Write || !configuration.ValidRelative(d.Directory) {
		return errors.New("local root/path forbidden")
	}
	target := filepath.Join(root.Path, filepath.FromSlash(d.Directory))
	// Walk the existing prefix; a missing child is allowed, a dangling link is not.
	for current := target; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				if _, err = filepath.EvalSymlinks(current); err != nil {
					return errors.New("dangling local symlink")
				}
			}
			resolved, err := filepath.EvalSymlinks(current)
			if err != nil {
				return errors.New("cannot resolve local path")
			}
			rel, err := filepath.Rel(root.Path, resolved)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
				return errors.New("local path escapes root")
			}
			if !info.IsDir() && current == target && info.Mode()&os.ModeSymlink == 0 {
				return errors.New("destination is not a directory")
			}
			resolvedInfo, err := os.Stat(resolved)
			if err != nil || !resolvedInfo.IsDir() {
				return errors.New("destination parent is not a directory")
			}
			return nil
		}
		if !os.IsNotExist(err) || current == root.Path || filepath.Dir(current) == current {
			return errors.New("local path unavailable")
		}
	}
}

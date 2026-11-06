package localfs

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
)

// File resolves one file under an administrator-owned root. P2 requires an
// existing parent and rejects a symlink leaf, including dangling symlinks.
func (r *Registry) File(ctx context.Context, rootID, relative string, write bool) (string, error) {
	root, ok := r.roots[rootID]
	if !ok || !configuration.ValidRelative(relative) || relative == "." || relative == "" || (write && !root.Write) || (!write && !root.Read) {
		return "", errors.New("local file forbidden")
	}
	// ValidateDestination also checks the real existing prefix. For read-only
	// roots use a temporary registry view; no shared permissions are mutated.
	view := &Registry{roots: map[string]Root{rootID: {ID: root.ID, Path: root.Path, Read: root.Read, Write: true}}}
	if err := view.ValidateDestination(ctx, configuration.Directory{RootID: rootID, Directory: filepath.ToSlash(filepath.Dir(relative))}); err != nil {
		return "", err
	}
	target := filepath.Join(root.Path, filepath.FromSlash(relative))
	info, err := os.Lstat(target)
	if err == nil && !info.Mode().IsRegular() {
		if info.IsDir() {
			if write {
				return "", fault.New(fault.Conflict, "TARGET_EXISTS", "下载目标是已有目录")
			}
			return "", fault.New(fault.Unsupported, "UNSUPPORTED_OPTION", "P2不支持目录传输")
		}
		return "", errors.New("local file must be regular and not a symlink")
	}
	if err != nil && (!write || !os.IsNotExist(err)) {
		return "", err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(target))
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(target)), nil
}

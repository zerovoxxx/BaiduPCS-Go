//go:build unix

package localfs

import (
	"context"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"strings"
)

func (r *Registry) openTree(ctx context.Context, id, relative string) (*os.File, error) {
	root, err := r.treeRoot(ctx, id, relative, false)
	if err != nil {
		return nil, err
	}
	fd, err := unix.Open(root.Path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	for _, part := range strings.Split(relative, "/") {
		if part == "" || part == "." {
			continue
		}
		next, e := unix.Openat(fd, part, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
		unix.Close(fd)
		if e != nil {
			return nil, e
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), filepath.Join(root.Path, relative)), nil
}
func (r *Registry) MkdirAll(ctx context.Context, id, relative string) error {
	root, err := r.treeRoot(ctx, id, relative, true)
	if err != nil {
		return err
	}
	fd, err := unix.Open(root.Path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer func() { unix.Close(fd) }()
	for _, part := range strings.Split(relative, "/") {
		if part == "" || part == "." {
			continue
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		err = unix.Mkdirat(fd, part, 0700)
		if err != nil && err != unix.EEXIST {
			return err
		}
		next, e := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if e != nil {
			return e
		}
		unix.Close(fd)
		fd = next
	}
	return unix.Fsync(fd)
}
func (r *Registry) ReplaceFile(ctx context.Context, id, source, target string, f *os.File) error {
	if _, err := r.File(ctx, id, target, true); err != nil {
		return err
	}
	fd, leaf, err := r.openParent(id, target)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	var a, b unix.Stat_t
	if err = unix.Fstatat(fd, source[strings.LastIndex(source, "/")+1:], &a, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	if err = unix.Fstat(int(f.Fd()), &b); err != nil {
		return err
	}
	if a.Ino != b.Ino || a.Dev != b.Dev || a.Mode&unix.S_IFMT != unix.S_IFREG {
		return unix.EINVAL
	}
	if err = unix.Renameat(fd, source[strings.LastIndex(source, "/")+1:], fd, leaf); err != nil {
		return err
	}
	return unix.Fsync(fd)
}

// SetFileMtime changes the opened inode, even if its directory entry is swapped.
func SetFileMtime(f *os.File, seconds int64) error {
	if seconds < 0 || seconds > 9223372036 {
		return unix.EINVAL
	}
	stamp := unix.NsecToTimeval(seconds * 1000000000)
	if int64(stamp.Sec) != seconds {
		return unix.EINVAL
	}
	return unix.Futimes(int(f.Fd()), []unix.Timeval{stamp, stamp})
}

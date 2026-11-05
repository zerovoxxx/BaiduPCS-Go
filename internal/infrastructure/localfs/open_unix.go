//go:build unix

package localfs

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// OpenFile walks directory descriptors with O_NOFOLLOW and opens the leaf from
// its pinned parent. Swapping a path component cannot redirect a file open.
func (r *Registry) OpenFile(ctx context.Context, id, relative string, flags int, mode os.FileMode) (*os.File, error) {
	if _, err := r.File(ctx, id, relative, flags&(os.O_WRONLY|os.O_RDWR|os.O_CREATE) != 0); err != nil {
		return nil, err
	}
	fd, leafName, err := r.openParent(id, relative)
	if err != nil {
		return nil, err
	}
	defer unix.Close(fd)
	leaf, err := unix.Openat(fd, leafName, flags|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, uint32(mode.Perm()))
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(leaf), filepath.Join(r.roots[id].Path, relative))
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		if err == nil {
			err = unix.EINVAL
		}
		return nil, err
	}
	return f, nil
}
func (r *Registry) openParent(id, relative string) (int, string, error) {
	root := r.roots[id]
	fd, err := unix.Open(root.Path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return -1, "", err
	}
	parts := strings.Split(filepath.ToSlash(relative), "/")
	for _, part := range parts[:len(parts)-1] {
		next, err := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		unix.Close(fd)
		if err != nil {
			return -1, "", err
		}
		fd = next
	}
	return fd, parts[len(parts)-1], nil
}

// CommitFile pins the parent directory for a no-overwrite link and unlink. The
// source's open descriptor must still identify the file being linked.
func (r *Registry) CommitFile(ctx context.Context, id, source, target string, file *os.File) error {
	if filepath.Dir(source) != filepath.Dir(target) {
		return unix.EINVAL
	}
	if _, err := r.File(ctx, id, target, true); err != nil {
		return err
	}
	fd, leaf, err := r.openParent(id, target)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	src := filepath.Base(source)
	var st unix.Stat_t
	if err = unix.Fstatat(fd, src, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	var opened unix.Stat_t
	if err = unix.Fstat(int(file.Fd()), &opened); err != nil {
		return err
	}
	if st.Ino != opened.Ino || st.Dev != opened.Dev || st.Mode&unix.S_IFMT != unix.S_IFREG {
		return unix.EINVAL
	}
	if err = unix.Linkat(fd, src, fd, leaf, 0); err != nil {
		return err
	}
	if err = unix.Unlinkat(fd, src, 0); err != nil {
		return err
	}
	// The file was synced before commit. Flush the directory metadata as well.
	return unix.Fsync(fd)
}

func (r *Registry) RemoveFile(ctx context.Context, id, relative string) error {
	if _, err := r.File(ctx, id, relative, true); err != nil {
		return err
	}
	fd, leaf, err := r.openParent(id, relative)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	err = unix.Unlinkat(fd, leaf, 0)
	if err == unix.ENOENT {
		return nil
	}
	return err
}

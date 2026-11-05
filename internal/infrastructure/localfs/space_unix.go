//go:build linux || darwin

package localfs

import "golang.org/x/sys/unix"

func availableBytes(path string) *uint64 {
	var stat unix.Statfs_t
	if unix.Statfs(path, &stat) != nil {
		return nil
	}
	v := uint64(stat.Bavail) * uint64(stat.Bsize)
	return &v
}

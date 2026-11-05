//go:build unix

package localfs

import (
	"context"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestP4DirectoryWalkRejectsFIFOWithoutBlocking(t *testing.T) {
	root := t.TempDir()
	if err := unix.Mkfifo(filepath.Join(root, "pipe"), 0600); err != nil {
		t.Fatal(err)
	}
	registry, err := New([]Root{{ID: "root", Path: root, Read: true}})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- registry.Walk(context.Background(), "root", ".", func(string, os.FileInfo) error { return nil })
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("FIFO blocked file preparation")
	}
}

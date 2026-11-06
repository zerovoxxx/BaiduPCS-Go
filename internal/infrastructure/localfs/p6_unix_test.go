//go:build unix

package localfs

import (
	"context"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/tool"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestP6ToolsRejectSymlinkAndFIFO(t *testing.T) {
	r, root := p6Local(t)
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret"), []byte("secret"), 0600)
	if e := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(root, "escape")); e != nil {
		t.Fatal(e)
	}
	if e := unix.Mkfifo(filepath.Join(root, "fifo"), 0600); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"escape", "fifo"} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		e := r.PrepareTool(ctx, tool.Plan{Units: []tool.Unit{{Local: stagingLocal(name)}}})
		cancel()
		if e == nil {
			t.Fatal("unsafe source accepted", name)
		}
	}
}

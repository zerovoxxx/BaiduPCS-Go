package localfs

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/infrastructure/persistence"
)

func TestP4StagingDiskFullNeverPublishesReadyFile(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	cfg := configuration.Client{AppID: 266719, CacheSize: 65536, MaxParallel: 1, MaxUploadParallel: 1, MaxDownloadLoad: 1, MaxUploadLoad: 1, PCSAddr: "pcs.baidu.com", EnableHTTPS: true, UploadPolicy: "skip"}
	repo, err := persistence.Open(context.Background(), dir, make([]byte, 32), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	files, err := NewStaging(dir, repo)
	if err != nil {
		t.Fatal(err)
	}
	files.copyFile = func(dst io.Writer, src io.Reader) (int64, error) {
		_, err := dst.Write([]byte("partial"))
		if err != nil {
			t.Fatal(err)
		}
		return 7, syscall.ENOSPC
	}
	f, err := files.Receive(context.Background(), "owner", "input", "", strings.NewReader("input"))
	if err != syscall.ENOSPC {
		t.Fatal("disk failure hidden", err)
	}
	if _, err = files.Metadata(context.Background(), f.ID); err == nil {
		t.Fatal("failed content was published")
	}
	entries, err := os.ReadDir(filepath.Join(dir, "staging"))
	if err != nil || len(entries) != 0 {
		t.Fatal("failed allocation leaked", err)
	}
}

package localfs

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
)

func TestRootBoundaryAndServiceSecrets(t *testing.T) {
	base := t.TempDir()
	root, private := filepath.Join(base, "uploads"), filepath.Join(base, "private")
	for _, path := range []string{root, private} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	registry, err := New([]Root{{ID: "upload", Path: root, Write: true}}, private)
	if err != nil {
		t.Fatal(err)
	}
	if err = registry.ValidateDestination(context.Background(), configuration.Directory{RootID: "upload", Directory: "future/child"}); err != nil {
		t.Fatal(err)
	}
	if _, err = New([]Root{{ID: "all", Path: base, Write: true}}, private); err == nil {
		t.Fatal("root exposed private service directory")
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(cwd, private)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = New([]Root{{ID: "all", Path: base, Write: true}}, relative); err == nil {
		t.Fatal("relative secret path bypassed root boundary")
	}

	if err = os.Symlink(private, filepath.Join(root, "escape")); err != nil {
		t.Skip("symbolic links unavailable on this platform")
	}
	if err = registry.ValidateDestination(context.Background(), configuration.Directory{RootID: "upload", Directory: "escape/new"}); err == nil {
		t.Fatal("symlink escaped authorized root")
	}
	if err = os.Symlink(filepath.Join(private, "missing"), filepath.Join(root, "dangling")); err != nil {
		t.Fatal(err)
	}
	if err = registry.ValidateDestination(context.Background(), configuration.Directory{RootID: "upload", Directory: "dangling/new"}); err == nil {
		t.Fatal("dangling link accepted")
	}
}

package bootstrap

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestLayerDependencyDirection(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	module := "github.com/qjfoidnh/BaiduPCS-Go/"
	rules := map[string][]string{
		"internal/domain":      {"internal/domain/"},
		"internal/ports":       {"internal/domain/"},
		"internal/application": {"internal/application/", "internal/domain/", "internal/ports"},
		"internal/httpapi":     {"internal/application/", "internal/domain/"},
	}
	for directory, allowed := range rules {
		err := filepath.WalkDir(filepath.Join(root, directory), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			source, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, item := range source.Imports {
				imported, _ := strconv.Unquote(item.Path.Value)
				if imported == "os" || imported == "os/signal" {
					t.Errorf("%s depends on process lifecycle: %s", path, imported)
				}
				if !strings.HasPrefix(imported, module) {
					continue
				}
				relative := strings.TrimPrefix(imported, module)
				valid := false
				for _, prefix := range allowed {
					if strings.HasPrefix(relative, prefix) {
						valid = true
						break
					}
				}
				if !valid {
					t.Errorf("forbidden layer dependency: %s -> %s", path, relative)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	// Infrastructure must never reach back into entry points or HTTP/application.
	err = filepath.WalkDir(filepath.Join(root, "internal/infrastructure"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		source, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		for _, item := range source.Imports {
			imported, _ := strconv.Unquote(item.Path.Value)
			for _, forbidden := range []string{"internal/application/", "internal/httpapi", "internal/bootstrap"} {
				if strings.HasPrefix(imported, module+forbidden) {
					t.Errorf("infrastructure dependency points upward: %s -> %s", path, imported)
				}
			}
		}
		ast.Inspect(source, func(node ast.Node) bool {
			if call, ok := node.(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
					if id, ok := sel.X.(*ast.Ident); ok && id.Name == "os" && sel.Sel.Name == "Exit" {
						t.Errorf("process exit inside infrastructure: %s", path)
					}
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestP4TransferUnitsDoNotReadCLIConfigOrRegisterSignals(t *testing.T) {
	for _, file := range []string{"../pcsfunctions/pcsupload/upload_task_unit.go", "../pcsfunctions/pcsupload/upload.go", "../pcsfunctions/pcsdownload/download_task_unit.go"} {
		source, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range source.Imports {
			p, _ := strconv.Unquote(item.Path.Value)
			if strings.HasSuffix(p, "/pcsconfig") || p == "os/signal" {
				t.Fatalf("TaskUnit still uses global config/lifecycle: %s", file)
			}
		}
	}
}

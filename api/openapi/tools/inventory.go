//go:build ignore

// Run from the repository root: go run api/openapi/tools/inventory.go
// This tool parses source; it never imports or initializes the CLI.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type entry struct {
	Kind    string   `json:"kind"`
	Name    string   `json:"name"`
	File    string   `json:"file"`
	Line    int      `json:"line"`
	Type    string   `json:"type,omitempty"`
	Aliases []string `json:"aliases,omitempty"`
}

var fset = token.NewFileSet()
var entries = []entry{}
var hashes = map[string]string{}

func syntax(n ast.Node) string {
	var b bytes.Buffer
	if err := format.Node(&b, fset, n); err != nil {
		panic(err)
	}
	return b.String()
}

func parse(file string) *ast.File {
	b, err := os.ReadFile(file)
	if err != nil {
		panic(err)
	}
	hashes[file] = fmt.Sprintf("%x", sha256.Sum256(b))
	f, err := parser.ParseFile(fset, file, b, 0)
	if err != nil {
		panic(err)
	}
	return f
}

func kv(c *ast.CompositeLit, name string) ast.Expr {
	for _, e := range c.Elts {
		if k, ok := e.(*ast.KeyValueExpr); ok {
			if id, ok := k.Key.(*ast.Ident); ok && id.Name == name {
				return k.Value
			}
		}
	}
	return nil
}

func lit(e ast.Expr) string {
	if b, ok := e.(*ast.BasicLit); ok {
		s, err := strconv.Unquote(b.Value)
		if err == nil {
			return s
		}
	}
	return ""
}

func flags(e ast.Expr, command string) {
	c, ok := e.(*ast.CompositeLit)
	if !ok {
		return
	}
	for _, e := range c.Elts {
		c, ok := e.(*ast.CompositeLit)
		if !ok {
			panic("non-literal CLI flag")
		}
		names := strings.Split(lit(kv(c, "Name")), ",")
		if names[0] == "" {
			panic("unnamed CLI flag")
		}
		aliases := []string{}
		for _, a := range names[1:] {
			aliases = append(aliases, strings.TrimSpace(a))
		}
		kind := "flag"
		if command == "" {
			kind = "global_flag"
		}
		entries = append(entries, entry{Kind: kind, Name: strings.TrimSpace(command + " --" + names[0]), File: "main.go", Line: fset.Position(c.Pos()).Line, Type: syntax(c.Type), Aliases: aliases})
	}
}

func commands(e ast.Expr, parent string) {
	c, ok := e.(*ast.CompositeLit)
	if !ok {
		panic("non-literal commands")
	}
	for _, e := range c.Elts {
		c, ok := e.(*ast.CompositeLit)
		if !ok {
			panic("non-literal command")
		}
		name := strings.TrimSpace(parent + " " + lit(kv(c, "Name")))
		if name == parent {
			panic("unnamed command")
		}
		aliases := []string{}
		if a, ok := kv(c, "Aliases").(*ast.CompositeLit); ok {
			for _, e := range a.Elts {
				aliases = append(aliases, lit(e))
			}
		}
		kind := "command"
		if kv(c, "Subcommands") != nil {
			kind = "command_group"
		}
		entries = append(entries, entry{Kind: kind, Name: name, File: "main.go", Line: fset.Position(kv(c, "Name").Pos()).Line, Aliases: aliases})
		flags(kv(c, "Flags"), name)
		if e := kv(c, "Subcommands"); e != nil {
			commands(e, name)
		}
	}
}

func main() {
	f := parse("main.go")
	ast.Inspect(f, func(n ast.Node) bool {
		a, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, l := range a.Lhs {
			s, ok := l.(*ast.SelectorExpr)
			if !ok {
				continue
			}
			id, ok := s.X.(*ast.Ident)
			if !ok || id.Name != "app" {
				continue
			}
			if s.Sel.Name == "Commands" {
				commands(a.Rhs[i], "")
			}
			if s.Sel.Name == "Flags" {
				flags(a.Rhs[i], "")
			}
		}
		return true
	})
	// All active declarations in these package directories, including methods
	// with an unexported receiver. Block comments and tests are excluded by AST.
	dirs := []string{"baidupcs", "internal/pcscommand", "internal/pcsconfig", "internal/pcsfunctions/pcsupload", "internal/pcsfunctions/pcsdownload", "pcsutil/taskframework", "requester/uploader", "requester/downloader"}
	optionTypes := map[string]bool{"OrderOptions": true, "ShareOption": true, "TransferOption": true, "DownloadOptions": true, "LocateDownloadOption": true, "ExportOptions": true, "LsOptions": true, "SearchOptions": true, "Runner": true, "TreeOptions": true, "UploadOptions": true, "ListTask": true}
	for _, dir := range dirs {
		files, err := filepath.Glob(dir + "/*.go")
		if err != nil {
			panic(err)
		}
		for _, file := range files {
			if strings.HasSuffix(file, "_test.go") {
				continue
			}
			f := parse(file)
			for _, d := range f.Decls {
				if fn, ok := d.(*ast.FuncDecl); ok && ast.IsExported(fn.Name.Name) {
					name := fn.Name.Name
					if fn.Recv != nil {
						name = syntax(fn.Recv.List[0].Type) + "." + name
					}
					entries = append(entries, entry{Kind: "function", Name: name, File: file, Line: fset.Position(fn.Pos()).Line})
				}
				gd, ok := d.(*ast.GenDecl)
				if !ok {
					continue
				}
				for _, sp := range gd.Specs {
					ts, ok := sp.(*ast.TypeSpec)
					if !ok {
						continue
					}
					st, ok := ts.Type.(*ast.StructType)
					if !ok {
						continue
					}
					kind := ""
					if dir == "internal/pcsconfig" && ts.Name.Name == "PCSConfig" {
						kind = "configuration_field"
					}
					if (dir == "baidupcs" || dir == "internal/pcscommand") && optionTypes[ts.Name.Name] {
						kind = "option_field"
					}
					if kind == "" {
						continue
					}
					for _, field := range st.Fields.List {
						for _, id := range field.Names {
							if ast.IsExported(id.Name) {
								entries = append(entries, entry{Kind: kind, Name: ts.Name.Name + "." + id.Name, Type: syntax(field.Type), File: file, Line: fset.Position(id.Pos()).Line})
							}
						}
					}
				}
			}
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.File != b.File {
			return a.File < b.File
		}
		return a.Name < b.Name
	})
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(struct {
		Files   map[string]string `json:"files"`
		Entries []entry           `json:"entries"`
	}{hashes, entries}); err != nil {
		panic(err)
	}
}

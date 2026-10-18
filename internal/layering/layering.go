// Package layering is the repository's import and call law, read from go/parser
// ASTs: the packages that must stay pure may not import the operating system
// or call the clock, so effects reach them as events and time as a value.
package layering

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ForbiddenImports and ForbiddenCalls are what internal/kernel and
// internal/measure may not use. The time.Duration type is not on the list.
var (
	ForbiddenImports = []string{"os", "os/exec"}
	ForbiddenCalls   = []string{"time.Now", "time.Since", "time.Until", "time.Sleep", "time.After", "time.Tick", "time.NewTimer", "time.NewTicker"}
)

// Finding is one breach of the law. File is the path relative to the parent of
// the checked directory, with forward slashes: "kernel/clock.go".
type Finding struct {
	File string
	Line int
	What string
}

func (f Finding) String() string { return fmt.Sprintf("%s:%d: %s", f.File, f.Line, f.What) }

// Check parses the non-test Go files under each of dirs (testdata directories
// excepted) and reports every import of a forbiddenImports path and every use
// of a forbiddenCalls function, written "importpath.Func", as `time.Now`. A
// call is found through the import it names, so an alias does not hide it and
// an unrelated identifier does not trip it. A directory that does not exist is
// skipped. Findings come back ordered by file and line.
func Check(dirs, forbiddenImports, forbiddenCalls []string) ([]Finding, error) {
	var out []Finding
	for _, dir := range dirs {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}
		parent := filepath.Dir(filepath.Clean(dir))
		err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" && p != dir {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			rel, err := filepath.Rel(parent, p)
			if err != nil {
				return err
			}
			found, err := checkFile(p, filepath.ToSlash(rel), forbiddenImports, forbiddenCalls)
			out = append(out, found...)
			return err
		})
		if err != nil {
			return nil, err
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
	return out, nil
}

func checkFile(file, name string, forbiddenImports, forbiddenCalls []string) ([]Finding, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var out []Finding
	local := map[string]string{} // the name a file knows an import by -> its path
	var dotted []string          // paths imported with a dot: their names are bare in this file
	for _, imp := range f.Imports {
		ipath, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			return nil, err
		}
		for _, bad := range forbiddenImports {
			if ipath == bad {
				out = append(out, Finding{name, fset.Position(imp.Pos()).Line, "imports " + ipath})
			}
		}
		switch {
		case imp.Name == nil:
			local[path.Base(ipath)] = ipath
		case imp.Name.Name != "_" && imp.Name.Name != ".":
			local[imp.Name.Name] = ipath
		case imp.Name.Name == ".":
			dotted = append(dotted, ipath)
		}
	}
	banned := map[string]bool{}
	for _, c := range forbiddenCalls {
		banned[c] = true
	}
	ast.Inspect(f, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			// A bare call to a name a dot-import brought in.
			if id, ok := call.Fun.(*ast.Ident); ok {
				for _, ipath := range dotted {
					if c := ipath + "." + id.Name; banned[c] {
						out = append(out, Finding{name, fset.Position(id.Pos()).Line, "calls " + c})
					}
				}
			}
			return true
		}
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		id, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		if ipath, ok := local[id.Name]; ok {
			if call := ipath + "." + sel.Sel.Name; banned[call] {
				out = append(out, Finding{name, fset.Position(sel.Pos()).Line, "calls " + call})
			}
		}
		return true
	})
	return out, nil
}

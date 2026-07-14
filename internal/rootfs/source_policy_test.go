package rootfs

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

func TestProductionOpenCallGraphHasNoCheckThenOpenBypass(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read rootfs sources: %v", err)
	}
	fileSet := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fileSet, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			absoluteRootOpens := 0
			ast.Inspect(function.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				if identifier, ok := call.Fun.(*ast.Ident); ok {
					if identifier.Name == "openPlatformRoot" && function.Name.Name != "OpenRoot" {
						t.Errorf("%s: %s reopens a root instead of using an owned handle", fileSet.Position(call.Pos()), function.Name.Name)
					}
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				qualifier, ok := selector.X.(*ast.Ident)
				if !ok {
					return true
				}
				callName := qualifier.Name + "." + selector.Sel.Name
				position := fileSet.Position(call.Pos())
				switch callName {
				case "os.Open", "os.OpenFile", "os.ReadFile", "os.Stat", "os.Lstat",
					"filepath.WalkDir", "filepath.EvalSymlinks", "filepath.Abs", "filepath.Join":
					t.Errorf("%s: production bypass %s is forbidden", position, callName)
				case "unix.Open", "windows.CreateFile":
					if function.Name.Name != "openPlatformRoot" {
						t.Errorf("%s: absolute open %s outside openPlatformRoot", position, callName)
					} else {
						absoluteRootOpens++
					}
				case "syscall.Open":
					t.Errorf("%s: raw absolute open %s is forbidden", position, callName)
				case "unix.Openat", "unix.Openat2":
					if len(call.Args) == 0 || isSelector(call.Args[0], "unix", "AT_FDCWD") {
						t.Errorf("%s: %s is not anchored to an owned directory handle", position, callName)
					}
				case "windows.NtCreateFile":
					if function.Name.Name != "openWindowsRelative" && function.Name.Name != "openWindowsSearchRelative" {
						t.Errorf("%s: NtCreateFile bypasses the relative-open helpers", position)
					}
				}
				return true
			})
			if function.Name.Name == "openPlatformRoot" && absoluteRootOpens != 1 {
				t.Errorf("%s: openPlatformRoot performs %d absolute opens, want exactly one", name, absoluteRootOpens)
			}
		}
	}
}

func isSelector(expression ast.Expr, qualifier, name string) bool {
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != name {
		return false
	}
	identifier, ok := selector.X.(*ast.Ident)
	return ok && identifier.Name == qualifier
}

package model

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// Only this package may ask a quota check or build a Granted in production
// code. Every route reaches a model through Gate, so a new route cannot add
// its own copy of a check or skip the gate without failing this test (decision
// 0162). Tests of the quota functions themselves are allowed to call them.
func TestOnlyTheModelPackageChecksQuotaOrGrants(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	gateOnly := map[string]bool{
		"CheckWorkerFloor":     true,
		"QuotaRefusal":         true,
		"QuotaBurnHold":        true,
		"QuotaRoomForProvider": true,
		"workerQuotaRoom":      true,
	}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "worktrees" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if strings.HasPrefix(filepath.ToSlash(rel), "internal/model/") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.CallExpr:
				if sel, ok := node.Fun.(*ast.SelectorExpr); ok && gateOnly[sel.Sel.Name] {
					t.Errorf("%s calls %s; only internal/model may run a gate check", rel, sel.Sel.Name)
				}
			case *ast.CompositeLit:
				if name := typeName(node.Type); name == "Granted" || name == "model.Granted" {
					t.Errorf("%s builds a Granted; only Gate may make one", rel)
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

// typeName is the last part of a literal's type, so model.Granted and
// Granted are both seen.
func typeName(e ast.Expr) string {
	switch node := e.(type) {
	case *ast.SelectorExpr:
		return typeName(node.X) + "." + node.Sel.Name
	case *ast.Ident:
		return node.Name
	}
	return ""
}

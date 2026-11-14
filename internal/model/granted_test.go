package model

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The gate's answer is a value that carries its own checks. No route can build
// one by hand: every field is unexported, so a struct literal outside the
// package does not compile.
func TestGrantedHasNoExportedFields(t *testing.T) {
	typ := reflect.TypeOf(Granted{})
	if typ.NumField() == 0 {
		t.Fatal("Granted has no fields: the check would pass for nothing")
	}
	for i := 0; i < typ.NumField(); i++ {
		if f := typ.Field(i); f.PkgPath == "" {
			t.Errorf("Granted field %q is exported: a route could build a grant", f.Name)
		}
	}
}

// The zero Granted is not a grant, and Gate by itself makes a usable one.
func TestGateReturnsAUsableGranted(t *testing.T) {
	c := Candidate{Name: "terra", Harness: "codex", Model: "gpt-6.1-terra"}
	if !(Granted{}).IsZero() {
		t.Fatal("the zero Granted looks usable")
	}
	got, _, err := Gate([]Candidate{c}, Checks{}, gateFacts())
	if err != nil {
		t.Fatal(err)
	}
	if got.IsZero() || got.Candidate().Name != c.Name {
		t.Fatalf("gate granted %+v", got)
	}
}

// A Go file outside this package may not build a Granted with a literal: the
// gate is the only way to get one.
func TestNoFileOutsideTheGateBuildsAGranted(t *testing.T) {
	root := filepath.Join("..", "..", "internal")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		if filepath.Dir(path) == filepath.Join(root, "model") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			if sel, ok := lit.Type.(*ast.SelectorExpr); ok && sel.Sel.Name == "Granted" {
				t.Errorf("%s builds a Granted", path)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

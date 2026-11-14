// Command testchanged tells scripts/test-changed.sh what a Go diff needs.
//
// It reads the two versions of a file and answers with what the shell cannot
// see: whether only comments or blank lines changed, which test functions
// changed, and which test functions a file declares. The script owns the rules;
// this helper only reads Go syntax, so the script stays POSIX sh.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"sort"
	"strings"
)

var (
	posType           = reflect.TypeOf(token.Pos(0))
	commentGroupPtr   = reflect.TypeOf((*ast.CommentGroup)(nil))
	commentGroupSlice = reflect.TypeOf([]*ast.CommentGroup(nil))
	commentPtr        = reflect.TypeOf((*ast.Comment)(nil))
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "comments-only":
		if len(os.Args) != 4 {
			usage()
		}
		var same bool
		same, err = commentsOnlyFiles(os.Args[2], os.Args[3])
		if err != nil {
			fail(err)
		}
		if !same {
			os.Exit(1)
		}
	case "changed-tests":
		if len(os.Args) != 4 {
			usage()
		}
		var names []string
		names, err = changedTestFiles(os.Args[2], os.Args[3])
		if err != nil {
			fail(err)
		}
		printNames(names)
	case "names":
		if len(os.Args) != 3 {
			usage()
		}
		var names []string
		names, err = testNamesFile(os.Args[2])
		if err != nil {
			fail(err)
		}
		printNames(names)
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: testchanged comments-only BASE NEW | changed-tests BASE NEW | names FILE")
	os.Exit(2)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "testchanged:", err)
	os.Exit(2)
}

func printNames(names []string) {
	for _, name := range names {
		fmt.Println(name)
	}
}

func readSource(path string) (string, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	return string(b), err
}

// commentsOnlyFiles tells whether the two versions differ only in comments and
// blank lines. A compiler directive such as //go:build is a comment to the
// parser but not to the build, so a change to one is a code change.
func commentsOnlyFiles(basePath, newPath string) (bool, error) {
	base, err := readSource(basePath)
	if err != nil {
		return false, err
	}
	next, err := readSource(newPath)
	if err != nil {
		return false, err
	}
	if base == "" {
		return false, nil
	}
	return commentsOnly(base, next)
}

func commentsOnly(base, next string) (bool, error) {
	a, err := parse(base)
	if err != nil {
		return false, err
	}
	b, err := parse(next)
	if err != nil {
		return false, err
	}
	if !reflect.DeepEqual(a, b) {
		return false, nil
	}
	return reflect.DeepEqual(directives(base), directives(next)), nil
}

// directives returns the comments the Go toolchain reads as instructions.
func directives(src string) []string {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "input.go", src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil
	}
	var out []string
	for _, group := range f.Comments {
		for _, c := range group.List {
			text := c.Text
			switch {
			case strings.HasPrefix(text, "//go:"),
				strings.HasPrefix(text, "// +build"),
				strings.HasPrefix(text, "//line "),
				strings.HasPrefix(text, "/*line "):
				out = append(out, text)
			}
		}
	}
	sort.Strings(out)
	return out
}

// parse returns a file with comments gone and positions cleared, so two files
// that differ only in layout or comments compare equal.
func parse(src string) (*ast.File, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "input.go", src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	f.Scope = nil
	clearLayout(reflect.ValueOf(f).Elem())
	return f, nil
}

// clearLayout zeroes source positions and comment nodes. Parsing without
// ParseComments leaves the text out but keeps the line breaks, so the
// positions must go too.
func clearLayout(v reflect.Value) {
	if !v.IsValid() {
		return
	}
	switch v.Kind() {
	case reflect.Ptr:
		if v.IsNil() {
			return
		}
		switch v.Type() {
		case commentGroupPtr, commentPtr:
			if v.CanSet() {
				v.Set(reflect.Zero(v.Type()))
			}
			return
		}
		clearLayout(v.Elem())
	case reflect.Interface:
		if !v.IsNil() {
			clearLayout(v.Elem())
		}
	case reflect.Slice:
		if v.Type() == commentGroupSlice {
			if v.CanSet() {
				v.Set(reflect.Zero(v.Type()))
			}
			return
		}
		for i := 0; i < v.Len(); i++ {
			clearLayout(v.Index(i))
		}
	case reflect.Struct:
		t := v.Type()
		for i := 0; i < v.NumField(); i++ {
			field := v.Field(i)
			if t.Field(i).Type == posType {
				if field.CanSet() {
					field.SetInt(0)
				}
				continue
			}
			clearLayout(field)
		}
	}
}

// changedTestFiles prints the tests in newPath whose declaration changed or
// that are new, sorted. When only a helper changed, it prints every test in the
// file: a helper change can break any of them, and there is no smaller set.
func changedTestFiles(basePath, newPath string) ([]string, error) {
	base, err := readSource(basePath)
	if err != nil {
		return nil, err
	}
	next, err := readSource(newPath)
	if err != nil {
		return nil, err
	}
	return changedTests(base, next)
}

func changedTests(base, next string) ([]string, error) {
	nextFile, err := parseRaw(next)
	if err != nil {
		return nil, err
	}
	baseFile, err := parseRaw(base)
	if err != nil {
		return nil, err
	}
	baseFuncs := topLevelFuncs(baseFile)
	newFuncs := topLevelFuncs(nextFile)

	var changed []string
	helpersChanged := false
	for name, fd := range newFuncs {
		old, ok := baseFuncs[name]
		if isTestName(name) {
			if !ok || !sameFunc(old, fd) {
				changed = append(changed, name)
			}
			continue
		}
		if !ok || !sameFunc(old, fd) {
			helpersChanged = true
		}
	}
	for name := range baseFuncs {
		if _, ok := newFuncs[name]; !ok && !isTestName(name) {
			helpersChanged = true
		}
	}
	if len(changed) == 0 && helpersChanged {
		for name := range newFuncs {
			if isTestName(name) {
				changed = append(changed, name)
			}
		}
	}
	sort.Strings(changed)
	return changed, nil
}

func parseRaw(src string) (*ast.File, error) {
	if src == "" {
		return &ast.File{}, nil
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "input.go", src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	f.Scope = nil
	return f, nil
}

func topLevelFuncs(f *ast.File) map[string]*ast.FuncDecl {
	out := make(map[string]*ast.FuncDecl)
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Recv != nil {
			continue
		}
		out[fd.Name.Name] = fd
	}
	return out
}

func sameFunc(a, b *ast.FuncDecl) bool {
	clearLayout(reflect.ValueOf(a).Elem())
	clearLayout(reflect.ValueOf(b).Elem())
	return reflect.DeepEqual(a, b)
}

// isTestName reports whether go test can select this function with -run.
// TestMain is excluded: it runs once for the package, not as a test.
func isTestName(name string) bool {
	if name == "TestMain" {
		return false
	}
	return strings.HasPrefix(name, "Test") ||
		strings.HasPrefix(name, "Example") ||
		strings.HasPrefix(name, "Fuzz")
}

func testNamesFile(path string) ([]string, error) {
	src, err := readSource(path)
	if err != nil {
		return nil, err
	}
	return testNames(src)
}

func testNames(src string) ([]string, error) {
	f, err := parseRaw(src)
	if err != nil {
		return nil, err
	}
	var names []string
	for name := range topLevelFuncs(f) {
		if isTestName(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}

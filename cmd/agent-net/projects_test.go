package main

import (
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseInterspersedFlagsAnywhere(t *testing.T) {
	for _, args := range [][]string{
		{"~/code", "--depth", "2", "--dry-run"},
		{"--depth", "2", "~/code", "--dry-run"},
		{"--dry-run", "--depth=2", "~/code"},
	} {
		fs := flag.NewFlagSet("x", flag.ContinueOnError)
		dry := fs.Bool("dry-run", false, "")
		depth := fs.Int("depth", 0, "")
		pos, err := parseInterspersed(fs, args)
		if err != nil || !reflect.DeepEqual(pos, []string{"~/code"}) || !*dry || *depth != 2 {
			t.Errorf("%v: pos %v dry %v depth %d err %v", args, pos, *dry, *depth, err)
		}
	}
	fs := flag.NewFlagSet("x", flag.ContinueOnError)
	fs.Bool("dry-run", false, "")
	pos, _ := parseInterspersed(fs, []string{"a", "b", "--dry-run", "c"})
	if !reflect.DeepEqual(pos, []string{"a", "b", "c"}) {
		t.Errorf("several positionals: %v", pos)
	}
}

func TestAbsPath(t *testing.T) {
	cwd, _ := os.Getwd()
	for in, want := range map[string]string{
		"~":          "~",
		"~/code":     "~/code",
		"/srv/x":     "/srv/x",
		"repo":       filepath.Join(cwd, "repo"),
		"../sibling": filepath.Join(filepath.Dir(cwd), "sibling"),
		".":          cwd,
	} {
		if got := absPath(in); got != want {
			t.Errorf("absPath(%q) = %q, want %q", in, got, want)
		}
	}
}

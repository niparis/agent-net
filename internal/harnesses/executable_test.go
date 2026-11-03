package harnesses

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestExecutable(t *testing.T) {
	pathDir, loginDir := t.TempDir(), t.TempDir()
	for _, dir := range []string{pathDir, loginDir} {
		if err := os.WriteFile(filepath.Join(dir, "go"), []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	opts := Options{PATH: pathDir, Dirs: []string{}, LoginShellPATH: func(context.Context) string { return loginDir }}
	if got := Executable(context.Background(), "go", opts); got != filepath.Join(loginDir, "go") {
		t.Fatalf("login executable: %q", got)
	}
	opts.LoginShellPATH = func(context.Context) string { return "" }
	if got := Executable(context.Background(), "go", opts); got != filepath.Join(pathDir, "go") {
		t.Fatalf("PATH executable: %q", got)
	}
	if got := Executable(context.Background(), "absent-tool", opts); got != "" {
		t.Fatalf("missing executable: %q", got)
	}
}

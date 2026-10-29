package harnesses

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func script(t *testing.T, dir, name, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDetectFindsToolsInPathAndKnownDirs(t *testing.T) {
	root := t.TempDir()
	inPath := filepath.Join(root, "inpath")
	known := filepath.Join(root, "home", ".opencode", "bin")
	claude := script(t, inPath, "claude", `echo "2.1.0 (Claude Code)"`)
	oc := script(t, known, "opencode", `case "$1" in --version) echo v9.9.9;; models) printf 'b/two\na/one\nnot a model\na/one\n\nlog line\n';; esac`)
	called := false
	got := Detect(context.Background(), Options{
		PATH: inPath, Dirs: []string{known},
		LoginShellPATH: func(context.Context) string { called = true; return "" },
	})
	if len(got) != 2 || got[0].Name != "claude" || got[1].Name != "opencode" {
		t.Fatalf("got %+v", got)
	}
	if got[0].Path != claude || got[0].Version != "2.1.0 (Claude Code)" || got[0].Models != nil {
		t.Fatalf("claude: %+v", got[0])
	}
	if got[1].Path != oc || got[1].Version != "v9.9.9" || !reflect.DeepEqual(got[1].Models, []string{"a/one", "b/two"}) {
		t.Fatalf("opencode: %+v", got[1])
	}
	if !called {
		t.Fatal("pi and codex are missing, so the login shell PATH should have been tried")
	}
}

func TestDetectUsesLoginShellPathOnlyWhenSomethingIsMissing(t *testing.T) {
	root := t.TempDir()
	dirs := map[string]string{}
	for _, n := range Names {
		dirs[n] = filepath.Join(root, n)
		script(t, dirs[n], n, `echo "`+n+` 1.0"`)
	}
	calls := 0
	opts := Options{PATH: dirs["claude"], Dirs: []string{dirs["opencode"], dirs["pi"]},
		LoginShellPATH: func(context.Context) string { calls++; return dirs["codex"] }}
	got := Detect(context.Background(), opts)
	if len(got) != 4 || got[3].Name != "codex" || got[3].Version != "codex 1.0" {
		t.Fatalf("got %+v", got)
	}
	if calls != 1 {
		t.Fatalf("login shell asked %d times, want 1", calls)
	}

	opts.Dirs = append(opts.Dirs, dirs["codex"])
	calls = 0
	Detect(context.Background(), opts)
	if calls != 0 {
		t.Fatalf("login shell asked although everything was found")
	}
}

func TestDetectNothingGivesEmptyNonNilList(t *testing.T) {
	got := Detect(context.Background(), Options{PATH: t.TempDir(), Dirs: []string{}, LoginShellPATH: func(context.Context) string { return "" }})
	if got == nil || len(got) != 0 {
		t.Fatalf("got %#v", got)
	}
}

func TestDetectIgnoresNonExecutableAndDirectories(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "claude"), []byte("x"), 0o644)
	os.MkdirAll(filepath.Join(root, "pi"), 0o755)
	got := Detect(context.Background(), Options{PATH: root, Dirs: []string{}, LoginShellPATH: func(context.Context) string { return "" }})
	if len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestFailingVersionAndModelsCommandsLeaveFieldsEmpty(t *testing.T) {
	root := t.TempDir()
	script(t, root, "opencode", `exit 3`)
	got := Detect(context.Background(), Options{PATH: root, Dirs: []string{}, LoginShellPATH: func(context.Context) string { return "" }})
	if len(got) != 1 || got[0].Version != "" || got[0].Models != nil {
		t.Fatalf("got %+v", got)
	}
}

func TestChildSeesTheSearchPath(t *testing.T) {
	// A wrapper like "#!/usr/bin/env node" needs its interpreter in PATH.
	root := t.TempDir()
	tools := filepath.Join(root, "tools")
	script(t, tools, "helper", `echo helper-ok`)
	script(t, root, "pi", `helper`)
	t.Setenv("PATH", "/usr/bin:/bin")
	got := Detect(context.Background(), Options{PATH: root, Dirs: []string{tools}, LoginShellPATH: func(context.Context) string { return "" }})
	if len(got) != 1 || got[0].Version != "helper-ok" {
		t.Fatalf("got %+v", got)
	}
}

func TestParseModels(t *testing.T) {
	out := "openai/gpt-5\nanthropic/claude-x\n  openai/gpt-5  \n/bad\nbad/\nno-slash\nwith space/model\n"
	want := []string{"anthropic/claude-x", "openai/gpt-5"}
	if got := ParseModels(out); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
	if ParseModels("nothing here") != nil {
		t.Fatal("want nil when no model is found")
	}
}

func TestLoginShellPathRejectsUnknownShell(t *testing.T) {
	t.Setenv("SHELL", "/bin/echo")
	if p := loginShellPATH(context.Background()); p != "" {
		t.Fatalf("got %q", p)
	}
	t.Setenv("SHELL", "bash")
	if p := loginShellPATH(context.Background()); p != "" {
		t.Fatalf("relative shell accepted: %q", p)
	}
}

func TestLoginShellPathRunsTheShell(t *testing.T) {
	sh := "/bin/bash"
	if _, err := os.Stat(sh); err != nil {
		t.Skip("no /bin/bash")
	}
	t.Setenv("SHELL", sh)
	t.Setenv("PATH", "/usr/bin:/bin")
	p := loginShellPATH(context.Background())
	if p == "" {
		t.Fatal("empty PATH from the login shell")
	}
}

package harnesses

import (
	"context"
	"flag"
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
	if got[0].Path != claude || got[0].Version != "2.1.0 (Claude Code)" || len(got[0].Models) == 0 {
		t.Fatalf("claude: %+v", got[0])
	}
	if got[1].Path != oc || got[1].Version != "v9.9.9" || !reflect.DeepEqual(got[1].Models, []string{"a/one", "b/two"}) {
		t.Fatalf("opencode: %+v", got[1])
	}
	if !called {
		t.Fatal("the login shell PATH should always be asked")
	}
}

func TestDetectUsesLoginShellPathToo(t *testing.T) {
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
	if len(got) != 4 || got[3].Name != "codex" || got[3].Version != "codex 1.0" || !got[3].Primary {
		t.Fatalf("got %+v", got)
	}
	if calls != 1 {
		t.Fatalf("login shell asked %d times, want 1", calls)
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

func TestDetectReportsEveryCopyAndMarksTheLoginShellOne(t *testing.T) {
	root := t.TempDir()
	v1 := script(t, filepath.Join(root, "home", ".opencode", "bin"), "opencode", `echo 1.18.16`)
	v2 := script(t, filepath.Join(root, "npm", "bin"), "opencode", `echo 2.2.0`)
	// A symlink to v2 is the same install and must not be listed twice.
	link := filepath.Join(root, "link")
	os.MkdirAll(link, 0o755)
	if err := os.Symlink(v2, filepath.Join(link, "opencode")); err != nil {
		t.Fatal(err)
	}
	got := Detect(context.Background(), Options{
		PATH: filepath.Dir(v1), Dirs: []string{link, filepath.Dir(v2)},
		LoginShellPATH: func(context.Context) string { return filepath.Dir(v2) + ":" + filepath.Dir(v1) },
	})
	if len(got) != 1 {
		t.Fatalf("got %+v", got)
	}
	h := got[0]
	if h.Path != v2 || h.Version != "2.2.0" || !h.Primary {
		t.Fatalf("main: %+v", h)
	}
	if len(h.Others) != 1 || h.Others[0].Path != v1 || h.Others[0].Version != "1.18.16" {
		t.Fatalf("others: %+v", h.Others)
	}
}

func TestDetectWithoutLoginShellAnswerFirstFoundIsNotPrimary(t *testing.T) {
	root := t.TempDir()
	a := script(t, filepath.Join(root, "a"), "pi", `echo 1`)
	b := script(t, filepath.Join(root, "b"), "pi", `echo 2`)
	got := Detect(context.Background(), Options{PATH: filepath.Dir(a), Dirs: []string{filepath.Dir(b)},
		LoginShellPATH: func(context.Context) string { return "" }})
	if len(got) != 1 || got[0].Path != a || got[0].Primary || len(got[0].Others) != 1 || got[0].Others[0].Path != b {
		t.Fatalf("got %+v", got)
	}
}

func TestDetectSingleCopyHasNoOthers(t *testing.T) {
	root := t.TempDir()
	script(t, root, "pi", `echo 1`)
	got := Detect(context.Background(), Options{PATH: root, Dirs: []string{root}, LoginShellPATH: func(context.Context) string { return root }})
	if len(got) != 1 || got[0].Others != nil || !got[0].Primary {
		t.Fatalf("got %+v", got)
	}
}

func TestDetectFindsToolsUnderNvmWithNoPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("NVM_DIR", "")
	t.Setenv("FNM_DIR", "")
	bin := filepath.Join(home, ".nvm", "versions", "node", "v22.23.3", "bin")
	script(t, bin, "opencode", `echo 2.2.0`)
	script(t, bin, "pi", `echo 0.7.0`)
	got := Detect(context.Background(), Options{PATH: "/nonexistent", Home: home, LoginShellPATH: func(context.Context) string { return "" }})
	if len(got) != 2 || got[0].Name != "opencode" || got[0].Version != "2.2.0" || got[1].Name != "pi" || got[1].Path != filepath.Join(bin, "pi") {
		t.Fatalf("got %+v", got)
	}
}

func mkNode(t *testing.T, root, ver string) string {
	t.Helper()
	d := filepath.Join(root, "versions", "node", ver, "bin")
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	return d
}

func writeAlias(t *testing.T, root, name, val string) {
	t.Helper()
	p := filepath.Join(root, "alias", filepath.FromSlash(name))
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(val+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestNvmOrderNewestFirstWithoutDefault(t *testing.T) {
	root := t.TempDir()
	for _, v := range []string{"v18.20.0", "v22.9.0", "v22.23.3", "v20.1.0"} {
		mkNode(t, root, v)
	}
	got := nvmBins(root)
	want := []string{"v22.23.3", "v22.9.0", "v20.1.0", "v18.20.0"}
	for i, w := range want {
		if got[i] != filepath.Join(root, "versions", "node", w, "bin") {
			t.Fatalf("got %v", got)
		}
	}
}

func TestNvmDefaultAliasWins(t *testing.T) {
	cases := map[string]string{
		"exact":  "v20.1.0",
		"prefix": "20",
		"lts":    "lts/iron",
	}
	for name, val := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			for _, v := range []string{"v18.20.0", "v20.1.0", "v22.23.3"} {
				mkNode(t, root, v)
			}
			writeAlias(t, root, "default", val)
			writeAlias(t, root, "lts/iron", "v20.1.0")
			got := nvmBins(root)
			if got[0] != filepath.Join(root, "versions", "node", "v20.1.0", "bin") || len(got) != 3 || got[1] != filepath.Join(root, "versions", "node", "v22.23.3", "bin") {
				t.Fatalf("got %v", got)
			}
		})
	}
}

func TestNvmUnresolvableDefaultFallsBackToNewest(t *testing.T) {
	root := t.TempDir()
	mkNode(t, root, "v18.20.0")
	mkNode(t, root, "v22.23.3")
	writeAlias(t, root, "default", "v99.0.0")
	if got := nvmBins(root); got[0] != filepath.Join(root, "versions", "node", "v22.23.3", "bin") {
		t.Fatalf("got %v", got)
	}
	writeAlias(t, root, "default", "lts/loop")
	writeAlias(t, root, "lts/loop", "lts/loop")
	if got := nvmBins(root); got[0] != filepath.Join(root, "versions", "node", "v22.23.3", "bin") {
		t.Fatalf("loop: %v", got)
	}
}

func TestNvmDirEnvIsSearchedFirst(t *testing.T) {
	home := t.TempDir()
	custom := filepath.Join(t.TempDir(), "nvmx")
	mkNode(t, custom, "v20.0.0")
	mkNode(t, filepath.Join(home, ".nvm"), "v22.0.0")
	t.Setenv("NVM_DIR", custom)
	dirs := versionManagerDirs(home)
	if len(dirs) < 2 || dirs[0] != filepath.Join(custom, "versions", "node", "v20.0.0", "bin") || dirs[1] != filepath.Join(home, ".nvm", "versions", "node", "v22.0.0", "bin") {
		t.Fatalf("got %v", dirs)
	}
}

func TestFnmDefaultAlias(t *testing.T) {
	home := t.TempDir()
	t.Setenv("FNM_DIR", "")
	root := filepath.Join(home, ".local", "share", "fnm")
	for _, v := range []string{"v18.0.0", "v22.0.0"} {
		os.MkdirAll(filepath.Join(root, "node-versions", v, "installation", "bin"), 0o755)
	}
	os.MkdirAll(filepath.Join(root, "aliases"), 0o755)
	if err := os.Symlink(filepath.Join(root, "node-versions", "v18.0.0", "installation"), filepath.Join(root, "aliases", "default")); err != nil {
		t.Fatal(err)
	}
	got := fnmBins(root)
	if len(got) != 2 || got[0] != filepath.Join(root, "node-versions", "v18.0.0", "installation", "bin") {
		t.Fatalf("got %v", got)
	}
}

func TestDefaultDirsListVersionManagersAndFriends(t *testing.T) {
	home := t.TempDir()
	for _, k := range []string{"NVM_DIR", "FNM_DIR", "VOLTA_HOME", "ASDF_DATA_DIR", "PNPM_HOME", "BUN_INSTALL"} {
		t.Setenv(k, "")
	}
	have := map[string]bool{}
	for _, d := range DefaultDirs(home) {
		have[d] = true
	}
	for _, rel := range []string{".volta/bin", ".asdf/shims", ".local/share/mise/shims", ".mise/shims", ".local/share/pnpm", "Library/pnpm", ".bun/bin", ".local/bin"} {
		if !have[filepath.Join(home, rel)] {
			t.Errorf("missing %s in %v", rel, DefaultDirs(home))
		}
	}
}

func TestVersionLess(t *testing.T) {
	if !versionLess("v22.9.0", "v22.23.3") || versionLess("v22.23.3", "v22.9.0") || versionLess("v1.0.0", "v1.0.0") {
		t.Fatal("wrong order")
	}
}

// ---- model lists for every harness ----

func TestDetectPiModels(t *testing.T) {
	root := t.TempDir()
	pi := script(t, root, "pi", `case "$1" in
--version) echo 1.4.0;;
--list-models) printf 'provider  model        context  max-out  thinking  images\nopenai    gpt-5        400K     128K     yes       yes\nlocal     llama-9      8K       4K       no        no\n';;
esac`)
	got := Detect(context.Background(), Options{PATH: root, Dirs: []string{}, LoginShellPATH: func(context.Context) string { return "" }})
	if len(got) != 1 || got[0].Path != pi {
		t.Fatalf("got %+v", got)
	}
	if want := []string{"local/llama-9", "openai/gpt-5"}; !reflect.DeepEqual(got[0].Models, want) {
		t.Fatalf("pi models: %v, want %v", got[0].Models, want)
	}
}

func TestDetectClaudeModels(t *testing.T) {
	root := t.TempDir()
	script(t, root, "claude", `echo "2.1.0 (Claude Code)"`)
	got := Detect(context.Background(), Options{PATH: root, Dirs: []string{}, LoginShellPATH: func(context.Context) string { return "" }})
	if len(got) != 1 || len(got[0].Models) == 0 {
		t.Fatalf("got %+v", got)
	}
	for _, want := range []string{"haiku", "sonnet", "opus", "fable", "claude-sonnet-5"} {
		if !contains(got[0].Models, want) {
			t.Errorf("claude models lack %q: %v", want, got[0].Models)
		}
	}
}

func TestDetectCodexModelsFromAppServer(t *testing.T) {
	root := t.TempDir()
	script(t, root, "codex", `while IFS= read -r line; do
case "$line" in
*'"initialize"'*) printf '%s\n' '{"id":1,"result":{}}';;
*'"model/list"'*) printf '%s\n' '{"id":2,"result":{"data":[{"id":"gpt-6-luna","model":"gpt-6-luna","defaultReasoningEffort":"high"},{"id":"gpt-6.1-luna","model":"gpt-6.1-luna"}],"nextCursor":null}}';;
esac
done`)
	got := Detect(context.Background(), Options{PATH: root, Dirs: []string{}, LoginShellPATH: func(context.Context) string { return "" }})
	if len(got) != 1 {
		t.Fatalf("got %+v", got)
	}
	if got[0].ModelDefaultEfforts["gpt-6-luna"] != "high" {
		t.Fatalf("default efforts: %v", got[0].ModelDefaultEfforts)
	}
	if want := []string{"gpt-6-luna", "gpt-6.1-luna"}; !reflect.DeepEqual(got[0].Models, want) {
		t.Fatalf("codex models: %v, want %v", got[0].Models, want)
	}
}

func TestDetectCodexModelListFailureGivesNoList(t *testing.T) {
	root := t.TempDir()
	script(t, root, "codex", `exit 3`)
	got := Detect(context.Background(), Options{PATH: root, Dirs: []string{}, LoginShellPATH: func(context.Context) string { return "" }})
	if len(got) != 1 || got[0].Models != nil {
		t.Fatalf("got %+v", got)
	}
}

func TestParsePiModelsIgnoresTheHeaderAndJunk(t *testing.T) {
	table := "provider  model        context  max-out  thinking  images\n" +
		"openai    gpt-5        400K     128K     yes       yes\n" +
		"bad line\n" +
		"local\n"
	want := []string{"openai/gpt-5"}
	if got := ParsePiModels(table); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

var realDetect = flag.Bool("real", false, "run detection against this machine (read-only) and print paths and versions")

// go test ./internal/harnesses -run TestRealDetection -real -v
func TestRealDetection(t *testing.T) {
	if !*realDetect {
		t.Skip("pass -real to run")
	}
	for _, h := range Detect(context.Background(), Options{}) {
		t.Logf("%s %s %q primary=%v", h.Name, h.Path, h.Version, h.Primary)
		for _, o := range h.Others {
			t.Logf("  other: %s %q", o.Path, o.Version)
		}
	}
}

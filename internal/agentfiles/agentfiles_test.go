package agentfiles

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/guidance"
	"github.com/niparis/agent-net/plugins/opencode"
	"github.com/niparis/agent-net/plugins/pi"
)

// fakeHome points HOME at a temp dir and clears XDG_CONFIG_HOME.
func fakeHome(t *testing.T) string {
	t.Helper()
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("XDG_CONFIG_HOME", "")
	return h
}

func mkdirs(t *testing.T, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func write(t *testing.T, path, s string) {
	t.Helper()
	mkdirs(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func symlink(t *testing.T, target, path string) {
	t.Helper()
	mkdirs(t, filepath.Dir(path))
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
}

func isSymlink(t *testing.T, path string) bool {
	t.Helper()
	st, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	return st.Mode()&os.ModeSymlink != 0
}

func byName(rs []Result) map[string]Result {
	m := map[string]Result{}
	for _, r := range rs {
		m[r.Name] = r
	}
	return m
}

// Old files from an earlier release are replaced; text around the guidance
// block is kept.
func TestSyncRefreshesOldFiles(t *testing.T) {
	h := fakeHome(t)
	write(t, PiExtensionPath(), "old pi")
	write(t, OpencodePluginPath(), "old opencode")
	write(t, filepath.Join(h, ".claude", "CLAUDE.md"), "# mine\n\n"+guidance.Begin+"\nold text\n"+guidance.End+"\n\n# after\n")
	rs := byName(Sync())
	for _, n := range []string{"pi extension", "opencode plugin", "claude guidance"} {
		if !rs[n].Changed || rs[n].Text != "updated" || rs[n].Err != nil {
			t.Errorf("%s: %+v", n, rs[n])
		}
	}
	if !bytes.Equal([]byte(read(t, PiExtensionPath())), pi.Extension) {
		t.Error("pi extension is not the embedded one")
	}
	if !bytes.Equal([]byte(read(t, OpencodePluginPath())), opencode.Plugin) {
		t.Error("opencode plugin is not the embedded one")
	}
	cm := read(t, filepath.Join(h, ".claude", "CLAUDE.md"))
	if want := "# mine\n\n" + guidance.Block(guidance.Claude) + "\n# after\n"; cm != want {
		t.Errorf("CLAUDE.md = %q, want %q", cm, want)
	}
	// A second run changes nothing.
	for _, r := range Sync() {
		if r.Changed || r.Err != nil {
			t.Errorf("second run: %+v", r)
		}
	}
}

// Without a harness folder nothing is created.
func TestSyncCreatesNothingWithoutHarness(t *testing.T) {
	h := fakeHome(t)
	for _, r := range Sync() {
		if r.Changed || r.Err != nil || !strings.HasPrefix(r.Text, "skipped") {
			t.Errorf("%+v", r)
		}
	}
	ents, _ := os.ReadDir(h)
	if len(ents) != 0 {
		t.Errorf("home is not empty: %v", ents)
	}
}

// A symlinked plugin file is left alone and reported as such. OpenCode honours
// XDG_CONFIG_HOME.
func TestSyncLeavesSymlinksAlone(t *testing.T) {
	h := fakeHome(t)
	dev := filepath.Join(h, "dev")
	write(t, filepath.Join(dev, "pi.ts"), "dev pi")
	write(t, filepath.Join(dev, "oc.ts"), "dev oc")
	mkdirs(t, filepath.Dir(PiExtensionPath()))
	if err := os.Symlink(filepath.Join(dev, "pi.ts"), PiExtensionPath()); err != nil {
		t.Fatal(err)
	}
	xdg := filepath.Join(h, "xdg")
	t.Setenv("XDG_CONFIG_HOME", xdg)
	mkdirs(t, filepath.Dir(OpencodePluginPath()))
	if err := os.Symlink(filepath.Join(dev, "oc.ts"), OpencodePluginPath()); err != nil {
		t.Fatal(err)
	}
	rs := byName(Sync())
	for _, n := range []string{"pi extension", "opencode plugin"} {
		if !rs[n].Symlink || rs[n].Changed || rs[n].Err != nil {
			t.Errorf("%s: %+v", n, rs[n])
		}
	}
	if got := read(t, filepath.Join(dev, "pi.ts")); got != "dev pi" {
		t.Errorf("pi dev file = %q", got)
	}
	if got := read(t, filepath.Join(dev, "oc.ts")); got != "dev oc" {
		t.Errorf("opencode dev file = %q", got)
	}
}

// A symlink to an older agent-net plugin file is replaced with a real file
// holding the current version. The old target is left alone and its path is
// kept in the result text.
func TestSyncReplacesStaleAgentNetSymlinks(t *testing.T) {
	h := fakeHome(t)
	dev := filepath.Join(h, "dev")
	cases := []struct {
		name   string
		path   string
		target string
		old    string
		want   []byte
	}{
		{
			name:   "pi extension",
			path:   PiExtensionPath(),
			target: filepath.Join(dev, "pi.ts"),
			old:    "// agent-net extension for Pi. An old copy.\nconst old = true;\n",
			want:   pi.Extension,
		},
		{
			name:   "opencode plugin",
			path:   OpencodePluginPath(),
			target: filepath.Join(dev, "oc.ts"),
			old:    "// agent-net plugin for OpenCode v2. An old copy.\nconst old = true;\n",
			want:   opencode.Plugin,
		},
	}
	for _, c := range cases {
		write(t, c.target, c.old)
		symlink(t, c.target, c.path)
	}
	rs := byName(Sync())
	for _, c := range cases {
		r := rs[c.name]
		if !r.Changed || r.Symlink || r.Err != nil {
			t.Errorf("%s: %+v", c.name, r)
		}
		if want := "replaced an old symlink to " + c.target + " with the current file"; r.Text != want {
			t.Errorf("%s text = %q, want %q", c.name, r.Text, want)
		}
		if isSymlink(t, c.path) {
			t.Errorf("%s is still a symlink", c.name)
		}
		if got := read(t, c.path); !bytes.Equal([]byte(got), c.want) {
			t.Errorf("%s is not the embedded current file", c.name)
		}
	}
	// The old targets keep their content.
	for _, c := range cases {
		if got := read(t, c.target); got != c.old {
			t.Errorf("%s target changed to %q", c.name, got)
		}
	}
	// A second run changes nothing now that the files are real.
	for _, c := range cases {
		if r := byName(Sync())[c.name]; r.Changed || r.Err != nil {
			t.Errorf("%s second run: %+v", c.name, r)
		}
	}
}

// A symlink to a copy that already holds the current version is kept.
func TestSyncKeepsCurrentAgentNetSymlink(t *testing.T) {
	h := fakeHome(t)
	dev := filepath.Join(h, "dev")
	cases := []struct {
		name   string
		path   string
		target string
		data   []byte
	}{
		{"pi extension", PiExtensionPath(), filepath.Join(dev, "pi.ts"), pi.Extension},
		{"opencode plugin", OpencodePluginPath(), filepath.Join(dev, "oc.ts"), opencode.Plugin},
	}
	for _, c := range cases {
		write(t, c.target, string(c.data))
		symlink(t, c.target, c.path)
	}
	rs := byName(Sync())
	for _, c := range cases {
		r := rs[c.name]
		if r.Changed || !r.Symlink || r.Err != nil {
			t.Errorf("%s: %+v", c.name, r)
		}
		if !isSymlink(t, c.path) {
			t.Errorf("%s is no longer a symlink", c.name)
		}
		if got := read(t, c.target); !bytes.Equal([]byte(got), c.data) {
			t.Errorf("%s target changed", c.name)
		}
	}
}

// A symlink to a file that agent-net did not write is left alone, even when
// its content differs from the current version.
func TestSyncLeavesForeignSymlinksAlone(t *testing.T) {
	h := fakeHome(t)
	dev := filepath.Join(h, "dev")
	write(t, filepath.Join(dev, "pi.ts"), "// my own extension\n")
	write(t, filepath.Join(dev, "oc.ts"), "// my own plugin\n")
	symlink(t, filepath.Join(dev, "pi.ts"), PiExtensionPath())
	symlink(t, filepath.Join(dev, "oc.ts"), OpencodePluginPath())
	rs := byName(Sync())
	for _, n := range []string{"pi extension", "opencode plugin"} {
		if rs[n].Symlink != true || rs[n].Changed || rs[n].Err != nil {
			t.Errorf("%s: %+v", n, rs[n])
		}
	}
	for _, c := range []struct{ path, target, want string }{
		{PiExtensionPath(), filepath.Join(dev, "pi.ts"), "// my own extension\n"},
		{OpencodePluginPath(), filepath.Join(dev, "oc.ts"), "// my own plugin\n"},
	} {
		if !isSymlink(t, c.path) {
			t.Errorf("%s is no longer a symlink", c.path)
		}
		if got := read(t, c.target); got != c.want {
			t.Errorf("%s target changed to %q", c.target, got)
		}
	}
}

// Only the harnesses that exist get files, and a guidance file that is not
// there yet is created only when its folder exists.
func TestSyncOnlyDetectedHarnesses(t *testing.T) {
	h := fakeHome(t)
	mkdirs(t, filepath.Join(h, ".config", "opencode"))
	rs := byName(Sync())
	if !rs["opencode plugin"].Changed || !rs["opencode guidance"].Changed {
		t.Errorf("opencode: %+v %+v", rs["opencode plugin"], rs["opencode guidance"])
	}
	for _, p := range []string{".pi", ".claude"} {
		if _, err := os.Stat(filepath.Join(h, p)); err == nil {
			t.Errorf("%s was created", p)
		}
	}
}

// SyncAndLog writes one line per changed file and per symlink, and nothing
// when everything is up to date.
func TestSyncAndLog(t *testing.T) {
	h := fakeHome(t)
	write(t, PiExtensionPath(), "old pi")
	mkdirs(t, filepath.Join(h, ".config", "opencode", "plugins"))
	dev := filepath.Join(h, "dev.ts")
	write(t, dev, "dev")
	if err := os.Symlink(dev, OpencodePluginPath()); err != nil {
		t.Fatal(err)
	}
	var lines []string
	logf := func(f string, a ...any) { lines = append(lines, fmt.Sprintf(f, a...)) }
	SyncAndLog(logf)
	want := []string{
		"agent files: pi extension updated: ~/.pi/agent/extensions/agent-net.ts",
		"agent files: opencode plugin left alone (symlink): ~/.config/opencode/plugins/agent-net.ts",
		"agent files: agent guidance created: ~/.pi/agent/AGENTS.md",
		"agent files: opencode guidance created: ~/.config/opencode/AGENTS.md",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("log lines:\n%s\nwant:\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	lines = nil
	SyncAndLog(logf)
	if len(lines) != 1 || !strings.Contains(lines[0], "left alone (symlink)") {
		t.Fatalf("second run logged %q, want only the symlink line", lines)
	}
}

// Replacing an old symlink is logged with the old target path.
func TestSyncAndLogReplacedSymlink(t *testing.T) {
	h := fakeHome(t)
	write(t, filepath.Join(h, ".pi", "agent", "AGENTS.md"), guidance.Block(guidance.Other))
	target := filepath.Join(h, "old-agent-net.ts")
	write(t, target, "// agent-net extension for Pi. old\n")
	symlink(t, target, PiExtensionPath())
	var lines []string
	SyncAndLog(func(f string, a ...any) { lines = append(lines, fmt.Sprintf(f, a...)) })
	want := "agent files: pi extension replaced an old symlink to " + target + " with the current file: ~/.pi/agent/extensions/agent-net.ts"
	for _, l := range lines {
		if l == want {
			return
		}
	}
	t.Fatalf("log lines %q, want one %q", lines, want)
}

func TestSyncGuidanceSharedSymlinkTarget(t *testing.T) {
	h := fakeHome(t)
	t.Setenv("CODEX_HOME", "")
	target := filepath.Join(h, "dotfiles", "AGENTS.md")
	write(t, target, "before\n"+guidance.Begin+"\nold\n"+guidance.End+"\nafter\n")
	for _, f := range GuidanceFiles() {
		symlink(t, target, f.Path)
	}
	for _, r := range SyncGuidance() {
		if r.Err != nil {
			t.Fatal(r.Err)
		}
	}
	got := read(t, target)
	for _, want := range []string{"before\n" + guidance.Begin, guidance.End + "\nafter\n", "Claude Code:", "Pi, OpenCode, Codex:", "`SendMessage`", "`agent_reply`"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Count(got, guidance.Begin) != 1 {
		t.Error("expected one block")
	}
	oldTime := time.Unix(1000000000, 0)
	if err := os.Chtimes(target, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	for _, r := range SyncGuidance() {
		if r.Err != nil || r.Changed {
			t.Errorf("second sync: %+v", r)
		}
	}
	st, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if !st.ModTime().Equal(oldTime) || read(t, target) != got {
		t.Error("second sync touched target")
	}
	for _, f := range GuidanceFiles() {
		if !isSymlink(t, f.Path) {
			t.Errorf("%s no longer a symlink", f.Path)
		}
	}
}

func TestSyncGuidanceSeparateFiles(t *testing.T) {
	fakeHome(t)
	t.Setenv("CODEX_HOME", "")
	for _, f := range GuidanceFiles() {
		write(t, f.Path, "before\n"+guidance.Begin+"\nold\n"+guidance.End+"\nafter\n")
	}
	for _, r := range SyncGuidance() {
		if r.Err != nil {
			t.Fatal(r.Err)
		}
	}
	for _, f := range GuidanceFiles() {
		if got, want := read(t, f.Path), "before\n"+guidance.Block(f.Variant)+"after\n"; got != want {
			t.Errorf("%s: got %q, want %q", f.Name, got, want)
		}
	}
}

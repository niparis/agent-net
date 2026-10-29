package agentfiles

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

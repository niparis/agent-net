package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
)

func promptTestDaemon(t *testing.T) (*Daemon, string) {
	t.Helper()
	home, state := t.TempDir(), t.TempDir()
	d := &Daemon{cfg: Config{InventoryHome: home, StateDir: state}}
	return d, home
}

func scanTestHome(t *testing.T, d *Daemon, home string) {
	t.Helper()
	d.toolsReport = inventory.Scan(inventory.Options{Home: home, Machine: "m"})
}

func TestSkillBundleOfCommandAndAgentFiles(t *testing.T) {
	d, home := promptTestDaemon(t)
	command := filepath.Join(home, ".claude/commands/review.md")
	agent := filepath.Join(home, ".claude/agents/planner.md")
	for path, text := range map[string]string{command: "Review the diff.\n", agent: "---\ndescription: Plans\n---\nPlan.\n"} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	scanTestHome(t, d, home)
	for _, c := range []struct{ kind, name, path string }{{protocol.InventoryPrompt, "review", command}, {protocol.InventoryAgent, "planner", agent}} {
		var hash string
		for _, it := range d.toolsReport.Items {
			if it.Kind == c.kind && it.Name == c.name {
				hash = it.Hash
			}
		}
		bundle, err := d.skillBundleFor(protocol.SkillBundleRequest{Name: c.name, Hash: hash, Location: c.path})
		if err != nil {
			t.Fatalf("%s: %v", c.kind, err)
		}
		dir := t.TempDir()
		if err = inventory.UnpackSkill(bundle, dir); err != nil {
			t.Fatal(err)
		}
		got, err := inventory.SkillHash(dir)
		if err != nil || got != hash {
			t.Fatalf("%s: bundle hash %q err %v, want %q", c.kind, got, err, hash)
		}
		if _, err = os.Stat(filepath.Join(dir, c.name+".md")); err != nil {
			t.Fatalf("%s: bundle lacks %s.md: %v", c.kind, c.name, err)
		}
		// A copy that is not a current observation is refused.
		if _, err = d.skillBundleFor(protocol.SkillBundleRequest{Name: c.name, Hash: "stale", Location: c.path}); err == nil {
			t.Fatalf("%s: stale hash accepted", c.kind)
		}
	}
}

func TestSkillTextOfCommandFile(t *testing.T) {
	d, home := promptTestDaemon(t)
	command := filepath.Join(home, ".claude/commands/review.md")
	if err := os.MkdirAll(filepath.Dir(command), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(command, []byte("Review the diff.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	scanTestHome(t, d, home)
	var hash string
	for _, it := range d.toolsReport.Items {
		if it.Kind == protocol.InventoryPrompt && it.Name == "review" {
			hash = it.Hash
		}
	}
	text, err := d.skillTextFor(protocol.SkillTextRequest{Name: "review", Hash: hash, Path: "review.md", Location: protocol.ToolImportLocation{Location: command}})
	if err != nil || !strings.Contains(text, "Review the diff.") {
		t.Fatalf("text %q err %v", text, err)
	}
	if _, err = d.skillTextFor(protocol.SkillTextRequest{Name: "review", Hash: hash, Path: "other.md", Location: protocol.ToolImportLocation{Location: command}}); err == nil {
		t.Fatal("file outside the manifest accepted")
	}
}

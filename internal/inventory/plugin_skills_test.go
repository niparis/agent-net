package inventory

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestPluginSkillRootsAndNames(t *testing.T) {
	home := t.TempDir()
	skills := []string{
		".claude/plugins/cache/market/remotion/1.0.0/skills",
		".claude/plugins/marketplaces/market/plugins/typesafe/skills",
		".claude/plugins/marketplaces/anthropic-skills/skills",
	}
	for _, rel := range skills {
		if err := os.MkdirAll(filepath.Join(home, rel, "one"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// Folders that must not be walked: node_modules, a dot folder, a link.
	for _, rel := range []string{".claude/plugins/cache/market/remotion/1.0.0/node_modules/x/skills", ".claude/plugins/cache/.hidden/skills"} {
		if err := os.MkdirAll(filepath.Join(home, rel), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	other := t.TempDir()
	if err := os.MkdirAll(filepath.Join(other, "skills"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, filepath.Join(home, ".claude/plugins/cache/linked")); err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, rel := range skills {
		want = append(want, filepath.Join(home, rel))
	}
	got := PluginSkillRoots(home)
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("roots = %v, want %v", got, want)
	}
	for rel, name := range map[string]string{
		skills[0] + "/one": "remotion",
		skills[1] + "/one": "typesafe",
		skills[2] + "/one": "anthropic-skills",
	} {
		if got := PluginName(filepath.Join(home, rel)); got != name {
			t.Errorf("PluginName(%s) = %q, want %q", rel, got, name)
		}
	}
	if PluginName(filepath.Join(home, ".claude/skills/x")) != "" {
		t.Error("a user skill has no plugin")
	}
}

// OnlyRoot limits the scan to one skill folder and lists nothing else.
func TestScanOnlyRoot(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".claude/plugins/cache/m/p/1/skills")
	for rel, body := range map[string]string{".claude/plugins/cache/m/p/1/skills/in-plugin": "a", ".claude/skills/user-skill": "b"} {
		if err := os.MkdirAll(filepath.Join(home, rel), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, rel, "SKILL.md"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	report := Scan(Options{Home: home, OnlyRoot: &SkillRoot{Harness: "claude", Base: home, Path: root}})
	if len(report.Items) != 1 || report.Items[0].Name != "in-plugin" || report.Items[0].Kind != protocol.InventorySkill || report.Items[0].Hash == "" {
		t.Fatalf("items: %+v", report.Items)
	}
}

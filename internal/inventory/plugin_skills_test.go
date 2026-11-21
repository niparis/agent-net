package inventory

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func mkdirs(t *testing.T, home string, rels ...string) {
	t.Helper()
	for _, rel := range rels {
		if err := os.MkdirAll(filepath.Join(home, rel), 0o700); err != nil {
			t.Fatal(err)
		}
	}
}

// Without installed_plugins.json only the installed-copy store (cache/) is
// walked. A marketplace clone is never walked: it holds every plugin the
// marketplace offers, installed or not.
func TestPluginSkillRootsWithoutInstallListWalksOnlyCache(t *testing.T) {
	home := t.TempDir()
	mkdirs(t, home,
		".claude/plugins/cache/market/remotion/1.0.0/skills/one",
		".claude/plugins/marketplaces/market/plugins/uninstalled/skills/one",
		// Folders that must not be walked: node_modules, a dot folder.
		".claude/plugins/cache/market/remotion/1.0.0/node_modules/x/skills",
		".claude/plugins/cache/.hidden/skills")
	other := t.TempDir()
	mkdirs(t, other, "skills")
	if err := os.Symlink(other, filepath.Join(home, ".claude/plugins/cache/linked")); err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(home, ".claude/plugins/cache/market/remotion/1.0.0/skills")}
	if got := PluginSkillRoots(home); !reflect.DeepEqual(got, want) {
		t.Fatalf("roots = %v, want %v", got, want)
	}
}

// With installed_plugins.json, exactly the listed install paths are read, in
// either file layout, and a path outside the plugin store is refused.
func TestPluginSkillRootsReadInstallList(t *testing.T) {
	for name, list := range map[string]string{
		"v2": `{"version":2,"plugins":{"remotion@market":[{"scope":"user","installPath":"%s/.claude/plugins/cache/market/remotion/1.0.0"}],
			"docs@local":[{"installPath":"%s/.claude/plugins/marketplaces/local"}],
			"evil@market":[{"installPath":"%s/elsewhere"}]}}`,
		"v1": `{"version":1,"plugins":{"remotion@market":{"installPath":"%s/.claude/plugins/cache/market/remotion/1.0.0"},
			"docs@local":{"installPath":"%s/.claude/plugins/marketplaces/local"},
			"evil@market":{"installPath":"%s/elsewhere"}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			mkdirs(t, home,
				".claude/plugins/cache/market/remotion/1.0.0/skills/one",
				".claude/plugins/cache/market/old-version/0.1.0/skills/one", // in the cache, not installed
				".claude/plugins/marketplaces/local/skills/one",             // a directory marketplace that is installed
				".claude/plugins/marketplaces/market/plugins/uninstalled/skills/one",
				"elsewhere/skills/one")
			text := strings.ReplaceAll(list, "%s", home)
			if err := os.WriteFile(filepath.Join(home, ".claude/plugins/installed_plugins.json"), []byte(text), 0o600); err != nil {
				t.Fatal(err)
			}
			got := PluginSkillRoots(home)
			sort.Strings(got)
			want := []string{
				filepath.Join(home, ".claude/plugins/cache/market/remotion/1.0.0/skills"),
				filepath.Join(home, ".claude/plugins/marketplaces/local/skills"),
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("roots = %v, want %v", got, want)
			}
		})
	}
}

func TestPluginName(t *testing.T) {
	home := "/h"
	for rel, name := range map[string]string{
		".claude/plugins/cache/market/remotion/1.0.0/skills/one":          "remotion",
		".claude/plugins/marketplaces/market/plugins/typesafe/skills/one": "typesafe",
		".claude/plugins/marketplaces/anthropic-skills/skills/one":        "anthropic-skills",
		".claude/skills/x": "",
	} {
		if got := PluginName(filepath.Join(home, rel)); got != name {
			t.Errorf("PluginName(%s) = %q, want %q", rel, got, name)
		}
	}
}

// ScanSkillRoot lists the skill folders of one root and nothing else.
func TestScanSkillRootListsOneRoot(t *testing.T) {
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
	report := ScanSkillRoot(home, SkillRoot{Harness: "claude", Base: home, Path: root})
	if len(report.Items) != 1 || report.Items[0].Name != "in-plugin" || report.Items[0].Kind != protocol.InventorySkill || report.Items[0].Hash == "" {
		t.Fatalf("items: %+v", report.Items)
	}
}

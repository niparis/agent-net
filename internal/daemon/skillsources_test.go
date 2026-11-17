package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
)

func writeSourceSkill(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The archive intake asks for the skill folders the inventory scan does not
// list: plugin skills and the CTO's own skill. Each one is then served by
// node.skill_bundle, and nothing else is.
func TestSkillSourcesListPluginSkills(t *testing.T) {
	d, home := promptTestDaemon(t)
	plugin := filepath.Join(home, ".claude/plugins/cache/market/remotion/1.0.0/skills/remotion")
	market := filepath.Join(home, ".claude/plugins/marketplaces/anthropic-skills/skills/docx")
	offered := filepath.Join(home, ".claude/plugins/marketplaces/big-market/plugins/never-installed/skills/unused")
	broken := filepath.Join(home, ".claude/plugins/cache/market/typesafe/2.0.0/skills/typesafe")
	writeSourceSkill(t, plugin, "---\ndescription: Video.\n---\nbody\n")
	writeSourceSkill(t, market, "---\ndescription: Docs.\n---\nbody\n")
	writeSourceSkill(t, offered, "---\ndescription: Not installed.\n---\nbody\n")
	installed := `{"version":2,"plugins":{"remotion@market":[{"installPath":"` + filepath.Join(home, ".claude/plugins/cache/market/remotion/1.0.0") + `"}],
		"docx@anthropic-skills":[{"installPath":"` + filepath.Join(home, ".claude/plugins/marketplaces/anthropic-skills") + `"}],
		"typesafe@market":[{"installPath":"` + filepath.Join(home, ".claude/plugins/cache/market/typesafe/2.0.0") + `"}]}}`
	if err := os.WriteFile(filepath.Join(home, ".claude/plugins/installed_plugins.json"), []byte(installed), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(broken, "SKILL.md"), 0o700); err != nil {
		t.Fatal(err)
	}
	got := map[string]protocol.SkillSource{}
	sourcesNow, _ := d.skillSources(nil)
	for _, s := range sourcesNow {
		got[s.Location] = s
	}
	for location, want := range map[string]struct{ name, origin string }{
		plugin: {"remotion", "plugin remotion"},
		market: {"docx", "plugin anthropic-skills"},
	} {
		s, ok := got[location]
		if !ok || s.Name != want.name || s.Origin != want.origin || s.State != protocol.InventoryOK || s.Hash == "" {
			t.Fatalf("source %s = %+v (listed %v)", location, s, ok)
		}
		bundle, err := d.skillBundleFor(protocol.SkillBundleRequest{Name: s.Name, Hash: s.Hash, Location: location})
		if err != nil {
			t.Fatalf("bundle %s: %v", location, err)
		}
		dir := t.TempDir()
		if err = inventory.UnpackSkill(bundle, dir); err != nil {
			t.Fatal(err)
		}
		if hash, err := inventory.SkillHash(dir); err != nil || hash != s.Hash {
			t.Fatalf("bundle hash %q (%v), want %q", hash, err, s.Hash)
		}
	}
	if _, ok := got[offered]; ok {
		t.Fatal("a plugin that is only offered by a marketplace was listed")
	}
	// The CTO's skill comes from the archive, not from a harness folder (decision
	// 0171), so no source is an agent-net folder.
	for _, s := range sourcesNow {
		if s.Origin == "agent-net" {
			t.Fatalf("a CTO harness folder is listed as a skill source: %+v", s)
		}
	}
	if s := got[broken]; s.State != protocol.InventoryError || s.Reason == "" || s.Origin != "plugin typesafe" {
		t.Fatalf("unreadable plugin skill: %+v", s)
	}
	if _, err := d.skillBundleFor(protocol.SkillBundleRequest{Name: "remotion", Hash: "stale", Location: plugin}); err == nil {
		t.Fatal("a changed hash was served")
	}
	if _, err := d.skillBundleFor(protocol.SkillBundleRequest{Name: "x", Hash: "x", Location: home}); err == nil {
		t.Fatal("an unlisted folder was served")
	}
}

func TestSkillSourcesWithoutPluginsIsEmpty(t *testing.T) {
	d, _ := promptTestDaemon(t)
	if got, _ := d.skillSources(nil); len(got) != 0 {
		t.Fatalf("sources: %+v", got)
	}
}

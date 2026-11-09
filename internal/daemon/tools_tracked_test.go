package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
)

func writeSkillFile(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("skill "+dir), 0600); err != nil {
		t.Fatal(err)
	}
}

// A project skill is tracked when git lists files under it. A skill that sits
// in the checkout but is not committed (or is kept out of git) is local.
func TestInventoryMarksTrackedProjectSkills(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	plain := t.TempDir() // not a git repository at all
	writeSkillFile(t, filepath.Join(repo, ".claude", "skills", "committed"))
	writeSkillFile(t, filepath.Join(repo, ".claude", "skills", "local-only"))
	writeSkillFile(t, filepath.Join(repo, ".agents", "skills", "also-committed"))
	writeSkillFile(t, filepath.Join(plain, ".claude", "skills", "no-repo"))
	gitIn(t, repo, "init", "-q", "-b", "main")
	gitIn(t, repo, "add", ".claude/skills/committed", ".agents/skills/also-committed")
	gitIn(t, repo, "-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-q", "-m", "skills")
	report := inventory.Scan(inventory.Options{Home: home, Machine: "m", Projects: []protocol.ProjectEntry{{Name: "repo", Path: repo}, {Name: "plain", Path: plain}}})
	markTrackedSkills(&report)
	got := map[string]bool{}
	seen := map[string]bool{}
	for _, it := range report.Items {
		if it.Kind == protocol.InventorySkill && it.Scope == protocol.InventoryProject {
			got[it.Name] = it.Tracked
			seen[it.Name] = true
		}
	}
	for name, want := range map[string]bool{"committed": true, "also-committed": true, "local-only": false, "no-repo": false} {
		if !seen[name] {
			t.Fatalf("scan lacks %s: %v", name, got)
		}
		if got[name] != want {
			t.Errorf("%s tracked=%v, want %v", name, got[name], want)
		}
	}
}

func TestInventoryTrackedUsesOneListingPerProjectRoot(t *testing.T) {
	repo := t.TempDir()
	for _, n := range []string{"a", "b", "c"} {
		writeSkillFile(t, filepath.Join(repo, ".claude", "skills", n))
	}
	gitIn(t, repo, "init", "-q", "-b", "main")
	report := inventory.Scan(inventory.Options{Home: t.TempDir(), Machine: "m", Projects: []protocol.ProjectEntry{{Name: "repo", Path: repo}}})
	calls := 0
	markTrackedSkillsWith(&report, func(root string) map[string]bool { calls++; return trackedUnder(root) })
	if calls != 1 {
		t.Fatalf("%d listings for one project root", calls)
	}
}

// A project command or agent is tracked when git lists that file. One that is
// only in the checkout is a local copy.
func TestInventoryMarksTrackedProjectCommandsAndAgents(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	for _, f := range []string{".claude/commands/committed.md", ".claude/commands/local-only.md", ".claude/agents/committed-agent.md", ".claude/agents/local-agent.md"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(repo, f)), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, f), []byte("text "+f), 0600); err != nil {
			t.Fatal(err)
		}
	}
	gitIn(t, repo, "init", "-q", "-b", "main")
	gitIn(t, repo, "add", ".claude/commands/committed.md", ".claude/agents/committed-agent.md")
	gitIn(t, repo, "-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-q", "-m", "files")
	report := inventory.Scan(inventory.Options{Home: home, Machine: "m", Projects: []protocol.ProjectEntry{{Name: "repo", Path: repo}}})
	markTrackedSkills(&report)
	got := map[string]bool{}
	for _, it := range report.Items {
		if protocol.IsFileKind(it.Kind) && it.Scope == protocol.InventoryProject {
			got[it.Kind+":"+it.Name] = it.Tracked
		}
	}
	want := map[string]bool{"prompt:committed": true, "prompt:local-only": false, "agent:committed-agent": true, "agent:local-agent": false}
	for k, v := range want {
		if g, ok := got[k]; !ok || g != v {
			t.Errorf("%s tracked=%v (seen %v), want %v", k, g, ok, v)
		}
	}
}

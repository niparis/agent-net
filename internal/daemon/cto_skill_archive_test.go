package daemon

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func skillRepo(t *testing.T) string {
	t.Helper()
	return skillRepoWithFiles(t, map[string]string{"SKILL.md": "---\ndescription: Ask hard questions.\n---\nbody\n"})
}

// skillRepoWithFiles commits one skill folder, grill-me, holding the given files
// (paths relative to the folder). It returns the repository's file URL.
func skillRepoWithFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.invalid",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.invalid",
			"LC_ALL=C")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	skill := filepath.Join(dir, "grill-me")
	for path, body := range files {
		full := filepath.Join(skill, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run("add", "-A")
	run("commit", "-q", "-m", "skill")
	return "file://" + dir
}

func TestCTOSkillArchiveAddsFromGitListsAndUndoes(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	ctx := context.Background()
	if err := n.d.registerCTOSession(ctx); err != nil {
		t.Fatal(err)
	}
	url := skillRepo(t)
	added := callCTOSkillArchive[protocol.SkillArchiveMultiResult](t, n.d, ctx, `{"action":"add_git","url":"`+url+`"}`)
	if len(added.Skills) != 1 || added.Skills[0].State != "added" || added.Skills[0].Change == "" {
		t.Fatalf("add: %+v", added)
	}
	listed := callCTOSkillArchive[struct {
		Skills []protocol.ArchivedSkill `json:"skills"`
	}](t, n.d, ctx, `{"action":"list"}`)
	if len(listed.Skills) != 1 || listed.Skills[0].Name != "grill-me" || listed.Skills[0].SizeTokens <= 0 || listed.Skills[0].ContextTokens <= 0 {
		t.Fatalf("list: %+v", listed)
	}
	if _, err := n.d.skillArchiveOwnerUndo(ctx, added.Skills[0].Change); err != nil {
		t.Fatal(err)
	}
	listed = callCTOSkillArchive[struct {
		Skills []protocol.ArchivedSkill `json:"skills"`
	}](t, n.d, ctx, `{"action":"list"}`)
	if len(listed.Skills) != 0 {
		t.Fatalf("list after undo: %+v", listed)
	}
}

func callCTOSkillArchive[T any](t *testing.T, d *Daemon, ctx context.Context, args string) T {
	t.Helper()
	result, err := d.runCTOTool(ctx, "skill_archive", json.RawMessage(args))
	if err != nil {
		t.Fatalf("skill_archive %s: %v", args, err)
	}
	b, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var out T
	if err = json.Unmarshal(b, &out); err != nil {
		t.Fatalf("skill_archive answer %s: %v", b, err)
	}
	return out
}

// The whole path: a real gateway and node, a user skill, a plugin skill and
// the CTO's own skill, one intake call. The report names the machine and every
// row has a source line.
func TestCTOSkillArchiveIntakeReachesPluginAndCTOSkills(t *testing.T) {
	home := t.TempDir()
	writeSourceSkill(t, filepath.Join(home, ".claude/skills/mine"), "---\ndescription: Mine.\n---\nbody\n")
	writeSourceSkill(t, filepath.Join(home, ".claude/plugins/cache/market/remotion/1.0.0/skills/remotion"), "---\ndescription: Video.\n---\nbody\n")
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi", detect: fakeInventory, cfg: func(c *Config) { c.InventoryHome = home }})
	ctx := context.Background()
	if err := n.d.registerCTOSession(ctx); err != nil {
		t.Fatal(err)
	}
	// The CTO's skill is not on disk any more: the daemon ships it (decision
	// 0171), so intake finds the two user skills only.
	if err := n.d.ensureCTOSkill(ctx); err != nil {
		t.Fatal(err)
	}
	var got protocol.SkillArchiveMultiResult
	eventually(t, "inventory of the user skill", func() bool {
		got = callCTOSkillArchive[protocol.SkillArchiveMultiResult](t, n.d, ctx, `{"action":"intake"}`)
		return len(got.Machines) == 1 && got.Machines[0].Result == "scanned" && got.Machines[0].Found >= 2
	})
	lines := map[string]string{}
	for _, row := range listSkillArchive(t, n.d, ctx) {
		lines[row.Name] = row.Source
	}
	if lines["remotion"] != "plugin remotion" || lines["agent-net-setup"] != "agent-net" || !strings.HasPrefix(lines["mine"], "machine ") {
		t.Fatalf("source lines: %+v (report %+v)", lines, got)
	}
}

func listSkillArchive(t *testing.T, d *Daemon, ctx context.Context) []protocol.ArchivedSkill {
	t.Helper()
	return callCTOSkillArchive[struct {
		Skills []protocol.ArchivedSkill `json:"skills"`
	}](t, d, ctx, `{"action":"list"}`).Skills
}

// promote goes through the CTO tool to the gateway. A version that is already
// the latest changes nothing, and a promote without a version is refused.
func TestCTOSkillArchivePromoteRoutesToGateway(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	ctx := context.Background()
	if err := n.d.registerCTOSession(ctx); err != nil {
		t.Fatal(err)
	}
	url := skillRepo(t)
	callCTOSkillArchive[protocol.SkillArchiveMultiResult](t, n.d, ctx, `{"action":"add_git","url":"`+url+`"}`)
	got := callCTOSkillArchive[protocol.SkillArchiveAddResult](t, n.d, ctx, `{"action":"promote","name":"grill-me","version":1}`)
	if got.State != "unchanged" || got.Latest != 1 {
		t.Fatalf("promote: %+v", got)
	}
	if _, err := n.d.runCTOTool(ctx, "skill_archive", json.RawMessage(`{"action":"promote","name":"grill-me"}`)); err == nil {
		t.Fatal("promote without a version was accepted")
	}
}

// A remove with a version takes out that version and leaves the name, and the
// Desk line names the version.
func TestCTOSkillArchiveRemovesOneVersion(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	ctx := context.Background()
	if err := n.d.registerCTOSession(ctx); err != nil {
		t.Fatal(err)
	}
	url := skillRepo(t)
	callCTOSkillArchive[protocol.SkillArchiveMultiResult](t, n.d, ctx, `{"action":"add_git","url":"`+url+`"}`)
	root := strings.TrimPrefix(url, "file://")
	if err := os.WriteFile(filepath.Join(root, "grill-me", "SKILL.md"), []byte("---\ndescription: Ask harder questions.\n---\nbody\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", "edit"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.invalid")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	callCTOSkillArchive[protocol.SkillArchiveMultiResult](t, n.d, ctx, `{"action":"add_git","url":"`+url+`"}`)
	removed := callCTOSkillArchive[protocol.SkillArchiveAddResult](t, n.d, ctx, `{"action":"remove","name":"grill-me","version":2}`)
	if removed.State != "removed" || removed.Version != 2 || removed.Versions != 1 || removed.Removed != 1 {
		t.Fatalf("remove one version: %+v", removed)
	}
}

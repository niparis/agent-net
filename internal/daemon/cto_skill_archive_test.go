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
	if err := os.MkdirAll(skill, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\ndescription: Ask hard questions.\n---\nbody\n"), 0o600); err != nil {
		t.Fatal(err)
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
	if len(listed.Skills) != 1 || listed.Skills[0].Name != "grill-me" || listed.Skills[0].Tokens <= 0 {
		t.Fatalf("list: %+v", listed)
	}
	// The Desk log records the undo the owner can click.
	log, err := g.gw.Store().DeskLog(10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range log {
		if strings.Contains(entry.Text, "added grill-me") && len(entry.Options) == 2 && entry.Options[0] == protocol.OpSkillArchiveUndo {
			found = true
		}
	}
	if !found {
		t.Fatalf("no undo entry: %+v", log)
	}
	if _, err = n.d.skillArchiveOwnerUndo(ctx, added.Skills[0].Change); err != nil {
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

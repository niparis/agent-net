package daemon

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// A plugin session in a linked worktree is named after the main repository,
// not after the worktree folder. This covers a worktree under .claude/worktrees,
// under .worktrees and a sibling folder, and a plain folder keeps its name.
func TestPluginNameUsesMainRepoInWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	base, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("GIT_CEILING_DIRECTORIES", base)
	main := filepath.Join(base, "cogforge")
	os.MkdirAll(main, 0o755)
	gitIn(t, main, "init", "-q", "-b", "main")
	gitIn(t, main, "commit", "-q", "--allow-empty", "-m", "one")
	wts := []string{
		filepath.Join(main, ".claude", "worktrees", "fix-embed-token-budget"),
		filepath.Join(main, ".worktrees", "feature-x"),
		filepath.Join(base, "cogforge-sibling"),
	}
	for i, wt := range wts {
		gitIn(t, main, "worktree", "add", "-q", "-b", "b"+string(rune('a'+i)), wt)
	}
	plain := filepath.Join(base, "plain-folder")
	os.MkdirAll(plain, 0o755)

	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	nameOf := func(id, cwd, name string) string {
		lc := dialLocal(t, n.sock)
		params := map[string]any{"client": "pi", "harness": "pi", "native_id": id, "cwd": cwd, "status": "idle"}
		if name != "" {
			params["name"] = name
		}
		var r struct {
			SessionID string `json:"session_id"`
		}
		json.Unmarshal(lc.mustCall("hello", params), &r)
		var v struct {
			Agent protocol.AgentView `json:"agent"`
		}
		json.Unmarshal(lc.mustCall("get", map[string]any{"session_id": r.SessionID}), &v)
		return v.Agent.Profile.Name
	}
	for i, wt := range wts {
		id := string(rune('a' + i))
		if got := nameOf("n"+id, wt, ""); got != "cogforge" {
			t.Errorf("%s: default name %q", wt, got)
		}
		if got := nameOf("t"+id, wt, filepath.Base(wt)+": Fix the budget"); got != "cogforge: Fix the budget" {
			t.Errorf("%s: titled name %q", wt, got)
		}
		if got := nameOf("f"+id, wt, filepath.Base(wt)); got != "cogforge" {
			t.Errorf("%s: folder-only name %q", wt, got)
		}
	}
	// A name the user chose is kept.
	if got := nameOf("custom", wts[0], "reviewer"); got != "reviewer" {
		t.Errorf("custom name %q", got)
	}
	if got := nameOf("plain", plain, ""); got != "plain-folder" {
		t.Errorf("plain: %q", got)
	}
	if got := nameOf("plain2", plain, "plain-folder: Hello"); got != "plain-folder: Hello" {
		t.Errorf("plain titled: %q", got)
	}
}

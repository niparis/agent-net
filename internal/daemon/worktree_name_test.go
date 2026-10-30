package daemon

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// A session's name is the project name: the folder of the main working tree,
// so a linked worktree uses the main repository's name. A title the harness
// reports is kept for the TASK column instead (decision 0024). A folder that
// is not a repository uses its own name.
func TestPluginSessionNameIsTheProjectName(t *testing.T) {
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
	cli := cliClient(t, n)
	// Each session goes offline after its check, so the next one gets the
	// plain project name and not a number.
	sessionOf := func(id, cwd, name string) protocol.AgentView {
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
		lc.c.Close()
		eventually(t, "the session goes offline", func() bool {
			for _, a := range onlineAgents(t, cli) {
				if a.SessionID == r.SessionID {
					return false
				}
			}
			return true
		})
		return v.Agent
	}
	for i, wt := range wts {
		id := string(rune('a' + i))
		if a := sessionOf("n"+id, wt, ""); a.Profile.Name != "cogforge" || a.Title != "" {
			t.Errorf("%s: default name %q title %q", wt, a.Profile.Name, a.Title)
		}
		if a := sessionOf("t"+id, wt, filepath.Base(wt)+": Fix the budget"); a.Profile.Name != "cogforge" || a.Title != "Fix the budget" {
			t.Errorf("%s: titled name %q title %q", wt, a.Profile.Name, a.Title)
		}
		if a := sessionOf("f"+id, wt, filepath.Base(wt)); a.Profile.Name != "cogforge" || a.Title != "" {
			t.Errorf("%s: folder-only name %q title %q", wt, a.Profile.Name, a.Title)
		}
	}
	// A title the plugin reports is not a name set by hand: it goes to TASK.
	if a := sessionOf("custom", wts[0], "reviewer"); a.Profile.Name != "cogforge" || a.Title != "reviewer" {
		t.Errorf("custom name %q title %q", a.Profile.Name, a.Title)
	}
	if a := sessionOf("plain", plain, ""); a.Profile.Name != "plain-folder" {
		t.Errorf("plain: %q", a.Profile.Name)
	}
	if a := sessionOf("plain2", plain, "plain-folder: Hello"); a.Profile.Name != "plain-folder" || a.Title != "Hello" {
		t.Errorf("plain titled: name %q title %q", a.Profile.Name, a.Title)
	}
}

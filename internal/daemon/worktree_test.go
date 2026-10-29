package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// Sessions in the main tree and in a worktree are one project in `projects`.
func TestProjectsOneEntryForWorktrees(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	base, _ := filepath.EvalSymlinks(t.TempDir())
	main := filepath.Join(base, "tenebrae")
	os.MkdirAll(main, 0o755)
	gitIn(t, main, "init", "-q", "-b", "main")
	gitIn(t, main, "commit", "-q", "--allow-empty", "-m", "one")
	wt := filepath.Join(base, "agent-a76c0ffee")
	gitIn(t, main, "worktree", "add", "-q", "-b", "task", wt)

	fa := &fakeAdapter{harness: "fake"}
	n := startNode(t, g, nodeOpts{adapter: fa})
	cli := cliClient(t, n)
	a, b := fakeSession("na", "main"), fakeSession("nb", "wt")
	a.Cwd, b.Cwd = main, wt
	fa.setSessions(a, b)
	eventually(t, "both sessions on the gateway", func() bool { return len(onlineAgents(t, cli)) == 2 })
	var pr struct {
		Projects []protocol.ProjectView `json:"projects"`
	}
	if err := json.Unmarshal(cli.mustCall("projects", nil), &pr); err != nil {
		t.Fatal(err)
	}
	if len(pr.Projects) != 1 || pr.Projects[0].Name != "tenebrae" || pr.Projects[0].Path != main ||
		pr.Projects[0].Sessions != 2 || len(pr.Projects[0].Worktrees) != 1 || pr.Projects[0].Worktrees[0] != wt {
		t.Fatalf("projects: %+v", pr.Projects)
	}
}

package daemon

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// The whole path for a project: a registered checkout with a skill folder
// that only the project holds. A real gateway and node, one intake call, and
// the source line names the project and the folder.
func TestCTOSkillArchiveIntakeReadsRegisteredProjectFolder(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(t.TempDir(), "demo")
	writeSourceSkill(t, filepath.Join(root, ".claude/skills/alpha"), "---\ndescription: Alpha.\n---\nbody\n")
	gitIn(t, root, "init", "-q", "-b", "main")
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi", detect: fakeInventory, cfg: func(c *Config) { c.InventoryHome = home }})
	cli := cliClient(t, n)
	cli.mustCall("project_add", protocol.ProjectAddRequest{Paths: []string{root}})
	ctx := context.Background()
	if err := n.d.registerCTOSession(ctx); err != nil {
		t.Fatal(err)
	}
	var got protocol.SkillArchiveMultiResult
	eventually(t, "project folder read by the intake", func() bool {
		got = callCTOSkillArchive[protocol.SkillArchiveMultiResult](t, n.d, ctx, `{"action":"intake"}`)
		return len(got.Machines) == 1 && got.Machines[0].Added == 1
	})
	want := "project demo " + filepath.Join(root, ".claude/skills/alpha")
	var line string
	for _, row := range listSkillArchive(t, n.d, ctx) {
		if row.Name == "alpha" {
			line = row.Source
		}
	}
	if line != want {
		t.Fatalf("source line = %q, want %q (report %+v)", line, want, got)
	}
	if m := got.Machines[0]; m.Read != "0 user folders, 1 project folder in 1 registered project, 0 plugin folders, 0 agent-net folders" {
		t.Fatalf("machine read = %q", m.Read)
	}
}

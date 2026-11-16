package gateway

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// intakeNode enrolls one machine with an inventory report of the given items.
func intakeNode(t *testing.T, g *Gateway, host string, items []protocol.InventoryItem) string {
	t.Helper()
	id, _, err := g.store.CreateNode(host, "linux")
	if err != nil {
		t.Fatal(err)
	}
	if err = g.store.SetInventory(id, protocol.InventoryReport{State: "ok", ScannedAt: time.Now(), Items: items}); err != nil {
		t.Fatal(err)
	}
	return id
}

// A registered project's skill folders reach the archive through the
// machine's answer to node.skill_sources. Each source line names the project.
func TestSkillArchiveIntakeReadsRegisteredProjectFolders(t *testing.T) {
	g := archiveGateway(t)
	dir := t.TempDir()
	project := filepath.Join(dir, "demo")
	alpha, alphaHash := archiveSkillFolder(t, filepath.Join(project, ".claude/skills/alpha"), "---\ndescription: Alpha.\n---\nbody\n")
	beta, betaHash := archiveSkillFolder(t, filepath.Join(project, ".agents/skills/beta"), "---\ndescription: Beta.\n---\nbody\n")
	id := intakeNode(t, g, "alpha", nil)
	if _, err := g.store.AddProjects(id, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "demo", Path: project}}}); err != nil {
		t.Fatal(err)
	}
	archiveFakeNodeAnswer(t, g, id, map[string][]byte{alphaHash: archiveBundle(t, alpha), betaHash: archiveBundle(t, beta)}, &protocol.SkillSourcesResult{
		Sources: []protocol.SkillSource{
			{Name: "alpha", Hash: alphaHash, Location: alpha, Origin: "project demo", Project: "demo", State: protocol.InventoryOK},
			{Name: "beta", Hash: betaHash, Location: beta, Origin: "project demo", Project: "demo", State: protocol.InventoryOK},
		},
		ProjectsRead: []string{"demo"},
	})
	_, got := archiveIntakeJSON(t, g)
	if len(got.Machines) != 1 {
		t.Fatalf("machines: %+v", got.Machines)
	}
	m := got.Machines[0]
	if m.Found != 2 || m.Added != 2 || m.Skipped != 0 {
		t.Fatalf("machine: %+v", m)
	}
	if want := "0 user folders, 2 project folders in 1 registered project, 0 plugin folders, 0 agent-net folders"; m.Read != want {
		t.Fatalf("read = %q, want %q", m.Read, want)
	}
	lines := map[string]string{}
	for _, row := range archiveList(t, g) {
		lines[row.Name] = row.Source
		if row.SourceProject != "demo" || row.SourceKind != "project" {
			t.Fatalf("list row without its project: %+v", row)
		}
	}
	if lines["alpha"] != "project demo "+alpha || lines["beta"] != "project demo "+beta {
		t.Fatalf("source lines: %+v", lines)
	}
}

// A machine's folders with the same name are one skipped row, and it lists
// every folder it was seen in.
func TestSkillArchiveIntakeGroupsSkippedFoldersByName(t *testing.T) {
	g := archiveGateway(t)
	dir := t.TempDir()
	folder, hash := archiveSkillFolder(t, filepath.Join(dir, "fine"), "---\ndescription: Fine.\n---\nbody\n")
	id := intakeNode(t, g, "alpha", []protocol.InventoryItem{
		{Kind: "skill", Name: "fine", Hash: hash, State: "ok", Scope: "user", Location: folder},
		{Kind: "skill", Name: "graphify", State: "error", Scope: "user", Location: "/home/a/.claude/skills/graphify", Reason: "invalid skill file"},
		{Kind: "skill", Name: "graphify", State: "error", Scope: "user", Location: "/home/a/.agents/skills/graphify", Reason: "invalid skill file"},
		{Kind: "skill", Name: "grill-me", State: "error", Scope: "user", Location: "/home/a/.agents/skills/grill-me", Reason: "cannot read source"},
	})
	archiveFakeNode(t, g, id, map[string][]byte{hash: archiveBundle(t, folder)})
	_, got := archiveIntakeJSON(t, g)
	if len(got.Skipped) != 2 {
		t.Fatalf("skipped rows: %+v", got.Skipped)
	}
	rows := map[string]protocol.SkillArchiveSkipped{}
	for _, s := range got.Skipped {
		rows[s.Name] = s
	}
	graphify := rows["graphify"]
	if len(graphify.Locations) != 2 || graphify.Locations[0] != "/home/a/.agents/skills/graphify" || graphify.Locations[1] != "/home/a/.claude/skills/graphify" || graphify.Reason != "invalid skill file" {
		t.Fatalf("graphify row: %+v", graphify)
	}
	if m := got.Machines[0]; m.Found != 3 || m.Added != 1 || m.Existing != 0 || m.Skipped != 2 {
		t.Fatalf("machine: %+v", m)
	}
}

// A link to a skill that is stored already is the same skill, so its folder is
// not a second skipped row.
func TestSkillArchiveIntakeFoldsLinkedCopyOfStoredSkill(t *testing.T) {
	g := archiveGateway(t)
	dir := t.TempDir()
	folder, hash := archiveSkillFolder(t, filepath.Join(dir, "graphify"), "---\ndescription: Graph.\n---\nbody\n")
	id := intakeNode(t, g, "alpha", []protocol.InventoryItem{
		{Kind: "skill", Name: "graphify", Hash: hash, State: "ok", Scope: "user", Location: folder},
		{Kind: "skill", Name: "graphify", State: "error", Scope: "user", Location: "/home/a/.claude/skills/graphify", Reason: "skill root is a symlink: " + folder},
	})
	archiveFakeNode(t, g, id, map[string][]byte{hash: archiveBundle(t, folder)})
	_, got := archiveIntakeJSON(t, g)
	if len(got.Skipped) != 0 {
		t.Fatalf("skipped rows for a linked copy: %+v", got.Skipped)
	}
	if m := got.Machines[0]; m.Found != 1 || m.Added != 1 || m.Skipped != 0 {
		t.Fatalf("machine: %+v", m)
	}
}

// The machine row says what the machine answered with. A daemon that does not
// read registered projects says so, instead of showing zero project folders.
func TestSkillArchiveIntakeSaysWhichFoldersWereRead(t *testing.T) {
	g := archiveGateway(t)
	dir := t.TempDir()
	user, userHash := archiveSkillFolder(t, filepath.Join(dir, "user"), "---\ndescription: User.\n---\nbody\n")
	plugin, pluginHash := archiveSkillFolder(t, filepath.Join(dir, "plugin"), "---\ndescription: Plugin.\n---\nbody\n")
	id := intakeNode(t, g, "alpha", []protocol.InventoryItem{
		{Kind: "skill", Name: "user", Hash: userHash, State: "ok", Scope: "user", Location: user},
	})
	if _, err := g.store.AddProjects(id, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "demo", Path: filepath.Join(dir, "demo")}}}); err != nil {
		t.Fatal(err)
	}
	archiveFakeNodeAnswer(t, g, id, map[string][]byte{userHash: archiveBundle(t, user), pluginHash: archiveBundle(t, plugin)}, &protocol.SkillSourcesResult{
		Sources: []protocol.SkillSource{{Name: "remotion", Hash: pluginHash, Location: plugin, Origin: "plugin remotion", State: protocol.InventoryOK}},
	})
	_, got := archiveIntakeJSON(t, g)
	m := got.Machines[0]
	if want := "1 user folder, project folders not read, 1 plugin folder, 0 agent-net folders"; m.Read != want {
		t.Fatalf("read = %q, want %q", m.Read, want)
	}
	if m.Found != 2 || m.Added != 2 {
		t.Fatalf("machine: %+v", m)
	}
}

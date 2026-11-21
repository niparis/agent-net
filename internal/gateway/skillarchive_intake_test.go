package gateway

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/toolcatalog"
)

// archiveIntakeJSON runs the intake and returns the raw output the CTO tool
// would hand to the catalog schema.
func archiveIntakeJSON(t *testing.T, g *Gateway) (json.RawMessage, protocol.SkillArchiveMultiResult) {
	t.Helper()
	data, err := g.skillArchiveIntakeMachines(t.Context(), "cto")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	return raw, data
}

func archiveSkillSchema(t *testing.T) (json.RawMessage, *jsonschema.Resolved) {
	t.Helper()
	for _, tool := range toolcatalog.Tools {
		if tool.Name != "skill_archive" {
			continue
		}
		var schema jsonschema.Schema
		if err := json.Unmarshal(tool.OutputSchema, &schema); err != nil {
			t.Fatal(err)
		}
		resolved, err := schema.Resolve(nil)
		if err != nil {
			t.Fatal(err)
		}
		return tool.OutputSchema, resolved
	}
	t.Fatal("no skill_archive tool")
	return nil, nil
}

// An intake answer must pass the schema the CTO tool checks, and the schema
// must not drop any field of it.
func TestSkillArchiveIntakeOutputMatchesCatalogSchema(t *testing.T) {
	g, n := archiveFixture(t)
	dir := t.TempDir()
	folder, hash := archiveSkillFolder(t, filepath.Join(dir, "a"), "---\ndescription: First copy.\n---\nbody\n")
	archiveFakeNodeSources(t, g, n, map[string][]byte{folder: archiveBundle(t, folder)}, []protocol.SkillSource{
		{Name: "demo", Hash: hash, Location: folder, Origin: "user", State: protocol.InventoryOK},
		{Name: "linked", Location: "/x/linked", Origin: "user", State: protocol.InventoryError, Reason: "skill root is a symlink: /elsewhere"},
	})
	raw, got := archiveIntakeJSON(t, g)
	if len(got.Skills) != 1 || got.Skills[0].Source == "" {
		t.Fatalf("row without a source line: %+v", got.Skills)
	}
	schema, resolved := archiveSkillSchema(t)
	var instance any
	if err := json.Unmarshal(raw, &instance); err != nil {
		t.Fatal(err)
	}
	if err := resolved.Validate(instance); err != nil {
		t.Fatalf("intake output does not match the catalog schema: %v\n%s", err, raw)
	}
	pruned, err := toolcatalog.PruneOutput(schema, raw)
	if err != nil {
		t.Fatal(err)
	}
	var a, b any
	_ = json.Unmarshal(raw, &a)
	_ = json.Unmarshal(pruned, &b)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("the schema drops fields of the intake output:\n%s\n%s", raw, pruned)
	}
}

// Every enrolled machine gets one result, and every folder that was seen and
// not stored is listed with its reason.
func TestSkillArchiveIntakeReportsEveryMachine(t *testing.T) {
	g := archiveGateway(t)
	dir := t.TempDir()
	folderOK, hashOK := archiveSkillFolder(t, filepath.Join(dir, "ok"), "---\ndescription: Fine.\n---\nbody\n")
	folderOld, hashOld := archiveSkillFolder(t, filepath.Join(dir, "old"), "---\ndescription: Known.\n---\nold\n")
	folderGone, hashGone := archiveSkillFolder(t, filepath.Join(dir, "gone"), "---\ndescription: Gone.\n---\ngone\n")
	// The scan still lists this folder, but the machine no longer has it.
	if err := os.RemoveAll(folderGone); err != nil {
		t.Fatal(err)
	}
	if _, err := g.skillArchivePut(skillContentOf(t, "known", folderOld, hashOld), skillArchiveSource{Kind: "machine", Node: "x", Path: "/x"}, "owner", false); err != nil {
		t.Fatal(err)
	}
	node := func(host string) string {
		id, _, err := g.store.CreateNode(host, "linux")
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	alpha, beta, gamma := node("alpha"), node("beta"), node("gamma")
	archiveFakeNodeSources(t, g, alpha, map[string][]byte{folderOK: archiveBundle(t, folderOK), folderOld: archiveBundle(t, folderOld)}, []protocol.SkillSource{
		{Name: "fine", Hash: hashOK, Location: folderOK, Origin: "user", State: protocol.InventoryOK},
		{Name: "known", Hash: hashOld, Location: folderOld, Origin: "user", State: protocol.InventoryOK},
		{Name: "vanished", Hash: hashGone, Location: folderGone, Origin: "user", State: protocol.InventoryOK},
		{Name: "linked", Location: "/x/linked", Origin: "user", State: protocol.InventoryError, Reason: "skill root is a symlink: /elsewhere"},
	})
	archiveFakeNode(t, g, gamma, nil)
	_ = beta // enrolled, but no connection

	_, got := archiveIntakeJSON(t, g)
	want := map[string]protocol.SkillArchiveMachine{
		"alpha": {Result: "scanned", Found: 4, Added: 1, Existing: 1, Skipped: 2},
		"beta":  {Result: "offline"},
		"gamma": {Result: "skipped", Reason: "the machine did not list its user skill folders: plugin and agent-net skills not read: unknown frame"},
	}
	if len(got.Machines) != len(want) {
		t.Fatalf("machines: %+v", got.Machines)
	}
	for _, m := range got.Machines {
		w := want[m.Name]
		if m.Result != w.Result || m.Found != w.Found || m.Added != w.Added || m.Existing != w.Existing || m.Skipped != w.Skipped || !strings.HasPrefix(m.Reason, w.Reason) {
			t.Errorf("machine %s = %+v, want %+v", m.Name, m, w)
		}
	}
	reasons := map[string]string{}
	for _, s := range got.Skipped {
		if s.Machine != "alpha" || s.NodeID != alpha {
			t.Errorf("skip names the wrong machine: %+v", s)
		}
		reasons[s.Name] = s.Reason
	}
	if !strings.Contains(reasons["linked"], "symlink") || !strings.Contains(reasons["vanished"], "cannot safely read") {
		t.Fatalf("skip reasons: %+v", got.Skipped)
	}
}

func skillContentOf(t *testing.T, name, dir, hash string) skillArchiveContent {
	t.Helper()
	c, err := skillContentFromDir(name, dir)
	if err != nil {
		t.Fatal(err)
	}
	c.Hash = hash
	return c
}

// Plugin skills and the CTO's own skill come from the daemon's
// node.skill_sources answer, with their own source lines.
func TestSkillArchiveIntakeTakesPluginAndAgentNetSkills(t *testing.T) {
	g := archiveGateway(t)
	dir := t.TempDir()
	plugin, pluginHash := archiveSkillFolder(t, filepath.Join(dir, ".claude/plugins/cache/market/remotion/1.0.0/skills/remotion"), "---\ndescription: Video.\n---\nbody\n")
	cto, ctoHash := archiveSkillFolder(t, filepath.Join(dir, "cto/claude/.claude/skills/agent-net-setup"), "---\ndescription: Setup.\n---\nbody\n")
	id, _, err := g.store.CreateNode("alpha", "linux")
	if err != nil {
		t.Fatal(err)
	}
	archiveFakeNodeSources(t, g, id, map[string][]byte{plugin: archiveBundle(t, plugin), cto: archiveBundle(t, cto)}, []protocol.SkillSource{
		{Name: "remotion", Hash: pluginHash, Location: plugin, Origin: "plugin remotion", State: "ok"},
		{Name: "agent-net-setup", Hash: ctoHash, Location: cto, Origin: "agent-net", State: "ok"},
		{Name: "broken", Location: "/p/broken", Origin: "plugin typesafe", State: "error", Reason: "invalid skill file"},
	})
	_, got := archiveIntakeJSON(t, g)
	lines := map[string]string{}
	for _, s := range got.Skills {
		lines[s.Name] = s.Source
	}
	if lines["remotion"] != "plugin remotion" || lines["agent-net-setup"] != "agent-net" || len(got.Skills) != 2 {
		t.Fatalf("source lines: %+v", got.Skills)
	}
	if len(got.Machines) != 1 || got.Machines[0].Found != 3 || got.Machines[0].Added != 2 || got.Machines[0].Skipped != 1 {
		t.Fatalf("machine: %+v", got.Machines)
	}
	if len(got.Skipped) != 1 || got.Skipped[0].Name != "broken" || got.Skipped[0].Reason != "invalid skill file" {
		t.Fatalf("skipped: %+v", got.Skipped)
	}
	rows := archiveList(t, g)
	for _, r := range rows {
		if r.Name == "remotion" && r.Source != "plugin remotion" {
			t.Fatalf("list source line: %+v", r)
		}
	}
}

func TestSkillArchiveIntakeSkipsAMachineThatDoesNotListItsFolders(t *testing.T) {
	g, n := archiveFixture(t)
	archiveFakeNode(t, g, n, nil)
	_, got := archiveIntakeJSON(t, g)
	if len(got.Machines) != 1 || got.Machines[0].Result != "skipped" || !strings.Contains(got.Machines[0].Reason, "did not list its user skill folders") {
		t.Fatalf("machine: %+v", got.Machines)
	}
}

// The real scan of every harness's user folder and of a registered
// project's folders reaches the archive, and a second intake changes nothing.
func TestSkillArchiveIntakeTakesUserAndProjectFoldersOfEveryHarness(t *testing.T) {
	g := archiveGateway(t)
	home, project := t.TempDir(), t.TempDir()
	user := map[string]string{
		".claude/skills/u-claude":            "claude",
		".agents/skills/u-agents":            "agents",
		".config/opencode/skills/u-opencode": "opencode",
		".pi/agent/skills/u-pi":              "pi",
		".codex/skills/u-codex":              "codex",
		".gemini/config/skills/u-agy":        "agy",
	}
	repo := map[string]string{
		".claude/skills/p-claude":     "claude",
		".agents/skills/p-agents":     "agents",
		".opencode/skills/p-opencode": "opencode",
		".pi/skills/p-pi":             "pi",
	}
	bundles := map[string][]byte{}
	write := func(root string, set map[string]string) {
		for rel, body := range set {
			folder, hash := archiveSkillFolder(t, filepath.Join(root, rel), "---\ndescription: "+body+".\n---\n"+body+"\n")
			bundles[hash] = archiveBundle(t, folder)
		}
	}
	write(home, user)
	write(project, repo)
	id, _, err := g.store.CreateNode("alpha", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.store.AddProjects(id, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "repo", Path: project}}}); err != nil {
		t.Fatal(err)
	}
	// The daemon answers node.skill_sources with the user folders and the
	// folders of each registered project.
	var sources []protocol.SkillSource
	scan := func(root inventory.SkillRoot, origin string) {
		for _, it := range inventory.ScanSkillRoot(home, root).Items {
			sources = append(sources, protocol.SkillSource{Name: it.Name, Hash: it.Hash, Location: it.Location, Origin: origin, Project: root.Project, State: it.State, Reason: it.Reason})
		}
	}
	for _, root := range inventory.UserSkillRoots(home, filepath.Join(home, ".codex")) {
		scan(root, "user")
	}
	for _, root := range inventory.ProjectSkillRoots("repo", project) {
		scan(root, "project repo")
	}
	archiveFakeNodeAnswer(t, g, id, bundles, &protocol.SkillSourcesResult{Sources: sources, ProjectsRead: []string{"repo"}, UserRead: true})
	_, first := archiveIntakeJSON(t, g)
	if len(first.Skills) != len(user)+len(repo) {
		t.Fatalf("added %d skills, want %d: %+v", len(first.Skills), len(user)+len(repo), first.Skills)
	}
	for _, s := range first.Skills {
		fromUser := strings.HasPrefix(s.Source, "machine "+id+":")
		fromProject := strings.HasPrefix(s.Source, "project repo ")
		if !(fromUser || fromProject) || !strings.Contains(s.Source, string(filepath.Separator)+"skills"+string(filepath.Separator)) {
			t.Errorf("unclear source line: %+v", s)
		}
	}
	_, second := archiveIntakeJSON(t, g)
	if len(second.Skills) != 0 || second.Machines[0].Added != 0 || second.Machines[0].Existing != len(user)+len(repo) {
		t.Fatalf("second intake: %+v", second)
	}
	for _, r := range archiveList(t, g) {
		if r.Versions != 1 {
			t.Errorf("repeat made a version: %+v", r)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".claude/skills/u-claude/SKILL.md")); err != nil {
		t.Fatal("intake must not touch the source folders")
	}
}

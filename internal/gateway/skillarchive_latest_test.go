package gateway

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// followsLatest is the content hash a node following latest gets: the same
// lookup the skill folder of a session uses.
func followsLatest(t *testing.T, g *Gateway, name string) string {
	t.Helper()
	idx, err := g.store.skillArchiveIndex()
	if err != nil {
		t.Fatal(err)
	}
	m, ok := idx.version(name, 0)
	if !ok {
		t.Fatalf("%s: a node following latest gets no version", name)
	}
	return m.Hash
}

// A second machine copy of a name that intake finds is stored as a candidate.
// The version a node following latest gets does not change.
func TestSkillArchiveIntakeCopyIsCandidateNotLatest(t *testing.T) {
	g := archiveGateway(t)
	dir := t.TempDir()
	folderA, hashA := archiveSkillFolder(t, filepath.Join(dir, "a"), "---\ndescription: First copy.\n---\nbody\n")
	folderB, hashB := archiveSkillFolder(t, filepath.Join(dir, "b"), "---\ndescription: Second copy.\n---\nother\n")
	id := intakeNode(t, g, "alpha", []protocol.InventoryItem{
		{Kind: "skill", Name: "demo", Hash: hashA, State: "ok", Scope: "user", Location: folderA},
	})
	archiveFakeNode(t, g, id, map[string][]byte{hashA: archiveBundle(t, folderA), hashB: archiveBundle(t, folderB)})

	// A new name is latest as soon as intake stores it.
	first := archiveCall[protocol.SkillArchiveMultiResult](t, g, protocol.FrameSkillArchiveIntake, protocol.SkillArchiveIntake{By: "cto"})
	if len(first.Skills) != 1 || first.Skills[0].Version != 1 || first.Skills[0].Latest != 1 {
		t.Fatalf("first intake: %+v", first.Skills)
	}

	if err := g.store.SetInventory(id, protocol.InventoryReport{State: "ok", ScannedAt: time.Now(), Items: []protocol.InventoryItem{
		{Kind: "skill", Name: "demo", Hash: hashA, State: "ok", Scope: "user", Location: folderA},
		{Kind: "skill", Name: "demo", Hash: hashB, State: "ok", Scope: "user", Location: folderB},
	}}); err != nil {
		t.Fatal(err)
	}
	second := archiveCall[protocol.SkillArchiveMultiResult](t, g, protocol.FrameSkillArchiveIntake, protocol.SkillArchiveIntake{By: "cto"})
	if len(second.Skills) != 1 || second.Skills[0].State != "added" || second.Skills[0].Version != 2 || second.Skills[0].Latest != 1 {
		t.Fatalf("second copy: %+v", second.Skills)
	}
	rows := archiveList(t, g)
	if len(rows) != 1 || rows[0].Version != 1 || rows[0].Versions != 2 || len(rows[0].Candidates) != 1 || rows[0].Candidates[0] != 2 {
		t.Fatalf("list: %+v", rows)
	}
	if got := followsLatest(t, g, "demo"); got != hashA {
		t.Fatalf("a node following latest got %s, want the first copy %s", got, hashA)
	}
}

// The project copy of a skill that the user folder also holds is a candidate
// too. It is the case that made graphify version 3 the latest in the live
// archive.
func TestSkillArchiveIntakeProjectCopyIsCandidateNotLatest(t *testing.T) {
	g := archiveGateway(t)
	dir := t.TempDir()
	project := filepath.Join(dir, "wbg")
	userCopy, userHash := archiveSkillFolder(t, filepath.Join(dir, "user", "graphify"), "---\ndescription: User graphify.\n---\nbody\n")
	projectCopy, projectHash := archiveSkillFolder(t, filepath.Join(project, ".claude/skills/graphify"), "---\ndescription: Project graphify.\n---\nother\n")
	id := intakeNode(t, g, "alpha", []protocol.InventoryItem{
		{Kind: "skill", Name: "graphify", Hash: userHash, State: "ok", Scope: "user", Location: userCopy},
	})
	if _, err := g.store.AddProjects(id, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "wbg", Path: project}}}); err != nil {
		t.Fatal(err)
	}
	archiveFakeNodeAnswer(t, g, id, map[string][]byte{userHash: archiveBundle(t, userCopy), projectHash: archiveBundle(t, projectCopy)}, &protocol.SkillSourcesResult{
		Sources: []protocol.SkillSource{
			{Name: "graphify", Hash: projectHash, Location: projectCopy, Origin: "project wbg", Project: "wbg", State: protocol.InventoryOK},
		},
		ProjectsRead: []string{"wbg"},
	})
	got := archiveCall[protocol.SkillArchiveMultiResult](t, g, protocol.FrameSkillArchiveIntake, protocol.SkillArchiveIntake{By: "cto"})
	if len(got.Skills) != 2 || got.Skills[1].Version != 2 || got.Skills[1].Latest != 1 || got.Skills[1].Source != "project wbg "+projectCopy {
		t.Fatalf("intake: %+v", got.Skills)
	}
	rows := archiveList(t, g)
	if len(rows) != 1 || rows[0].Version != 1 || len(rows[0].Candidates) != 1 || rows[0].Candidates[0] != 2 {
		t.Fatalf("list: %+v", rows)
	}
	if latest := followsLatest(t, g, "graphify"); latest != userHash {
		t.Fatalf("a node following latest got %s, want the user copy %s", latest, userHash)
	}
}

// Promote makes one live version the latest. The change is recorded, so an
// undo puts the earlier latest back.
func TestSkillArchivePromoteChoosesLatestAndUndoRestoresIt(t *testing.T) {
	g := archiveGateway(t)
	url := archiveRepo(t)
	archiveCall[protocol.SkillArchiveMultiResult](t, g, protocol.FrameSkillArchiveAddGit, protocol.SkillArchiveAddGit{URL: url, By: "owner"})
	root := strings.TrimPrefix(url, "file://")
	if err := os.WriteFile(filepath.Join(root, "grill-me", "SKILL.md"), []byte("---\ndescription: Ask harder questions.\n---\nbody\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	archiveGit(t, root, "add", "-A")
	archiveGit(t, root, "commit", "-q", "-m", "edit")
	archiveCall[protocol.SkillArchiveMultiResult](t, g, protocol.FrameSkillArchiveAddGit, protocol.SkillArchiveAddGit{URL: url, By: "owner"})
	if rows := archiveList(t, g); rows[0].Version != 2 {
		t.Fatalf("explicit new version is not latest: %+v", rows)
	}

	if _, err := g.handle(t.Context(), &nodeConn{id: "archive-test"}, protocol.FrameSkillArchivePromote, []byte(`{"name":"grill-me","version":9,"by":"cto"}`)); err == nil {
		t.Fatal("promote of a version that is not in the archive was accepted")
	}

	promoted := archiveCall[protocol.SkillArchiveAddResult](t, g, protocol.FrameSkillArchivePromote, protocol.SkillArchivePromote{Name: "grill-me", Version: 1, By: "cto"})
	if promoted.State != "promoted" || promoted.Version != 1 || promoted.Latest != 1 || promoted.Change == "" {
		t.Fatalf("promote: %+v", promoted)
	}
	rows := archiveList(t, g)
	if len(rows) != 1 || rows[0].Version != 1 || len(rows[0].Candidates) != 1 || rows[0].Candidates[0] != 2 {
		t.Fatalf("list after promote: %+v", rows)
	}
	if got := archiveCall[protocol.SkillArchiveReadResult](t, g, protocol.FrameSkillArchiveRead, protocol.SkillArchiveVersion{Name: "grill-me", By: "owner"}); got.Version != 1 {
		t.Fatalf("read of the latest after promote: %+v", got)
	}

	again := archiveCall[protocol.SkillArchiveAddResult](t, g, protocol.FrameSkillArchivePromote, protocol.SkillArchivePromote{Name: "grill-me", Version: 1, By: "cto"})
	if again.State != "unchanged" || again.Change != "" {
		t.Fatalf("promote of the latest changed something: %+v", again)
	}

	undone := archiveCall[protocol.SkillArchiveAddResult](t, g, protocol.FrameSkillArchiveUndo, protocol.SkillArchiveUndo{Change: promoted.Change, By: "owner"})
	if undone.State != "reset" {
		t.Fatalf("undo promote: %+v", undone)
	}
	rows = archiveList(t, g)
	if len(rows) != 1 || rows[0].Version != 2 || len(rows[0].Candidates) != 1 || rows[0].Candidates[0] != 1 {
		t.Fatalf("list after undo promote: %+v", rows)
	}
}

// The migration after the intake release names the earlier version again
// for a project copy that was added after it: the live graphify case.
// Machine copies keep the highest-version rule, because an explicit machine
// add looks the same.
func TestSkillArchiveLatestMigrationMarksIntakeProjectCopyCandidate(t *testing.T) {
	g := archiveGateway(t)
	if _, err := g.store.db.Exec(`DELETE FROM skill_archive`); err != nil {
		t.Fatal(err)
	}
	rows := []struct {
		name, kind, hash string
		version          int
	}{
		{"graphify", "machine", "h1", 1},
		{"graphify", "machine", "h2", 2},
		{"graphify", "project", "h3", 3},
		{"demo", "machine", "h4", 1},
		{"demo", "machine", "h5", 2},
	}
	for _, r := range rows {
		if _, err := g.store.db.Exec(`INSERT INTO skill_archive(name,version,hash,content,description,tokens,source_kind,source_node,source_path,added_by,added_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?)`, r.name, r.version, r.hash, []byte("bundle"), "d", 1, r.kind, "n", "/x", "cto", 0); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := g.store.db.Exec(`DELETE FROM skill_archive_latest`); err != nil {
		t.Fatal(err)
	}
	if _, err := g.store.db.Exec(skillArchiveLatestMigration); err != nil {
		t.Fatalf("migration: %v", err)
	}
	got := map[string]protocol.ArchivedSkill{}
	for _, row := range archiveList(t, g) {
		got[row.Name] = row
	}
	if gr := got["graphify"]; gr.Version != 2 || len(gr.Candidates) != 2 || gr.Candidates[0] != 1 || gr.Candidates[1] != 3 {
		t.Fatalf("graphify after migration: %+v", gr)
	}
	if dm := got["demo"]; dm.Version != 2 || len(dm.Candidates) != 1 || dm.Candidates[0] != 1 {
		t.Fatalf("demo after migration: %+v", dm)
	}
}

// A skill with no description in its front matter is flagged in the intake
// answer and in the list, so the CTO can see it.
func TestSkillArchiveIntakeFlagsSkillWithoutDescription(t *testing.T) {
	g := archiveGateway(t)
	folder, hash := archiveSkillFolder(t, filepath.Join(t.TempDir(), "bare"), "---\nname: bare\n---\nbody\n")
	id := intakeNode(t, g, "alpha", []protocol.InventoryItem{
		{Kind: "skill", Name: "bare", Hash: hash, State: "ok", Scope: "user", Location: folder},
	})
	archiveFakeNode(t, g, id, map[string][]byte{hash: archiveBundle(t, folder)})
	got := archiveCall[protocol.SkillArchiveMultiResult](t, g, protocol.FrameSkillArchiveIntake, protocol.SkillArchiveIntake{By: "cto"})
	if len(got.Skills) != 1 || !got.Skills[0].NoDescription {
		t.Fatalf("intake: %+v", got.Skills)
	}
	rows := archiveList(t, g)
	if len(rows) != 1 || !rows[0].NoDescription {
		t.Fatalf("list: %+v", rows)
	}
}

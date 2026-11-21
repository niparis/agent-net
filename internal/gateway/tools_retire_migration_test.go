package gateway

import (
	"path/filepath"
	"testing"
)

// toolsDroppedTables are the tables the tools retire migration dropped.
var toolsDroppedTables = []string{
	"tool_identities", "tool_locations", "tool_revisions", "tool_results", "tool_audit",
	"tool_project_settings", "tool_project_seen", "tool_accepted", "plans", "skill_versions",
	"mcp_imports", "inventory_reports", "skill_archive_latest", "skill_archive_changes", "skill_list_changes",
}

// The tools retire migration (decision 0169, task 5) drops the old Tools
// rollout tables, turns skill_archive_latest into skill_archive.in_use and
// merges the two undo logs into skill_changes. This seeds the schema before it
// and checks where each fact landed.
func TestToolsRetireMigrationFoldsTheSkillTables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	s := migrateOnly(t, path, migrationVersion(t, toolsRetireMarker)-1)
	defer s.Close()
	for _, q := range []string{
		// Two skills: in "alpha" the version in use is not the newest, in
		// "beta" the newest. "gamma" has no latest row.
		`INSERT INTO skill_archive(name,version,hash,content,added_at) VALUES
			('alpha',1,'a1',x'00',1),('alpha',2,'a2',x'00',2),('alpha',3,'a3',x'00',3),
			('beta',1,'b1',x'00',1),('beta',2,'b2',x'00',2),
			('gamma',1,'g1',x'00',1)`,
		`INSERT INTO skill_archive_latest(name,version) VALUES ('alpha',2),('beta',2)`,
		`INSERT INTO skill_archive_changes(id,action,name,version,versions,restored,actor,at,undone_at,previous) VALUES
			('sc_1','add','alpha',3,'[3]',0,'cto',10,0,2),('sc_2','remove','beta',0,'[1]',1,'owner',11,12,0)`,
		`INSERT INTO skill_list_changes(id,level,scope,before,after,actor,at,undone_at) VALUES
			('sl_1','global','','[]','[{"name":"alpha"}]','cto',20,0)`,
		`INSERT INTO gateway_state(key,value,updated_at) VALUES ('skill_archive.git_intake_done','1',1),('skill_archive.git_intake_at','1',1),('skill_archive.other','keep',1)`,
		`INSERT INTO tool_identities(id,kind,name,hash) VALUES ('["skill","alpha","a1"]','skill','alpha','a1')`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	if err := replayMigrations(t, s); err != nil {
		t.Fatal(err)
	}

	inUse := map[string]int{}
	rows, err := s.db.Query(`SELECT name, version FROM skill_archive WHERE in_use=1`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var name string
		var version int
		if err := rows.Scan(&name, &version); err != nil {
			t.Fatal(err)
		}
		inUse[name] = version
	}
	rows.Close()
	if len(inUse) != 2 || inUse["alpha"] != 2 || inUse["beta"] != 2 {
		t.Fatalf("in_use = %v, want alpha 2 (not the newest) and beta 2", inUse)
	}
	if _, err := s.db.Exec(`UPDATE skill_archive SET in_use=1 WHERE name='alpha' AND version=3`); err == nil {
		t.Fatal("two versions of one name are in use")
	}

	var archive, list int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM skill_changes WHERE kind='archive'`).Scan(&archive); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM skill_changes WHERE kind='list'`).Scan(&list); err != nil {
		t.Fatal(err)
	}
	if archive != 2 || list != 1 {
		t.Fatalf("skill_changes holds %d archive and %d list rows, want 2 and 1", archive, list)
	}
	var previous, undone int64
	var action string
	if err := s.db.QueryRow(`SELECT action, previous, undone_at FROM skill_changes WHERE id='sc_2'`).Scan(&action, &previous, &undone); err != nil || action != "remove" || undone != 12 {
		t.Fatalf("archive change sc_2: %q %d %d %v", action, previous, undone, err)
	}
	var after string
	if err := s.db.QueryRow(`SELECT after FROM skill_changes WHERE id='sl_1' AND kind='list'`).Scan(&after); err != nil || after != `[{"name":"alpha"}]` {
		t.Fatalf("list change sl_1: %q %v", after, err)
	}

	var keys int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM gateway_state WHERE key LIKE 'skill_archive.%'`).Scan(&keys); err != nil || keys != 1 {
		t.Fatalf("skill_archive state keys left: %d %v", keys, err)
	}

	for _, table := range toolsDroppedTables {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("table %s is still there", table)
		}
	}
}

// Undo by id keeps working for both kinds of change, and an id of the other
// kind is refused.
func TestSkillChangesUndoWorksForBothKinds(t *testing.T) {
	g, _ := archiveFixture(t)
	dir := t.TempDir()
	folder, hash := archiveSkillFolder(t, filepath.Join(dir, "demo"), "---\ndescription: Demo.\n---\nbody\n")
	added, err := g.skillArchivePut(skillContentOf(t, "demo", folder, hash), skillArchiveSource{Kind: "machine", Node: "n", Path: folder}, "owner", false)
	if err != nil {
		t.Fatal(err)
	}
	set := setSkills(t, g.store, "global", "", []string{"demo"}, nil, nil)
	if set.Change == "" {
		t.Fatalf("set: %+v", set)
	}
	if _, err := g.skillArchiveUndo(set.Change, "owner"); err == nil {
		t.Fatal("an archive undo took a list change")
	}
	if _, err := g.store.SkillListUndo(added.Change); err == nil {
		t.Fatal("a list undo took an archive change")
	}
	if _, err := g.store.SkillListUndo(set.Change); err != nil {
		t.Fatalf("undo of the list change: %v", err)
	}
	if _, err := g.skillArchiveUndo(added.Change, "owner"); err != nil {
		t.Fatalf("undo of the add: %v", err)
	}
}

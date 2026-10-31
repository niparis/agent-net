package gateway

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func seedIdentity(t *testing.T, s *Store, kind, name, hash string) string {
	t.Helper()
	if _, err := s.db.Exec(`INSERT OR IGNORE INTO tool_identities(id,kind,name,hash) VALUES(json_array(?,?,?),?,?,?)`, kind, name, hash, kind, name, hash); err != nil {
		t.Fatal(err)
	}
	var id string
	if err := s.db.QueryRow(`SELECT id FROM tool_identities WHERE kind=? AND name=? AND hash=?`, kind, name, hash).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestSkillVersionsGetStableNumbersPerName(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(filepath.Join(dir, "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	repo, err := openSkillStore(dir, s)
	if err != nil {
		t.Fatal(err)
	}
	a1 := seedIdentity(t, s, "skill", "alpha", "sha:1")
	a2 := seedIdentity(t, s, "skill", "alpha", "sha:2")
	b1 := seedIdentity(t, s, "skill", "beta", "sha:3")
	v1, err := repo.put(a1, "sha:1", []byte("one"), "n", "p")
	if err != nil {
		t.Fatal(err)
	}
	v2, err := repo.put(a2, "sha:2", []byte("two"), "n", "p")
	if err != nil {
		t.Fatal(err)
	}
	vb, err := repo.put(b1, "sha:3", []byte("three"), "n", "p")
	if err != nil {
		t.Fatal(err)
	}
	again, err := repo.put(a1, "sha:1", []byte("one"), "n", "p")
	if err != nil {
		t.Fatal(err)
	}
	if v1.Number != 1 || v2.Number != 2 || vb.Number != 1 || again.Number != 1 {
		t.Fatalf("numbers %d %d %d %d, want 1 2 1 1", v1.Number, v2.Number, vb.Number, again.Number)
	}
}

func TestToolsItemsExposeVersionNumbers(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(filepath.Join(dir, "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	repo, err := openSkillStore(dir, s)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"sha:1", "sha:2"} {
		id := seedIdentity(t, s, "skill", "alpha", h)
		if _, err = repo.put(id, h, []byte(h), "n", "p"); err != nil {
			t.Fatal(err)
		}
	}
	// A copy somewhere with a hash that was never imported.
	if err = s.SetInventory("n_a", protocol.InventoryReport{State: protocol.InventoryOK, Items: []protocol.InventoryItem{
		{Harness: "claude", Kind: "skill", Scope: "user", Location: "/h/alpha", Name: "alpha", Hash: "sha:2", State: "ok"},
		{Harness: "claude", Kind: "skill", Scope: "user", Location: "/h/alpha2", Name: "alpha", Hash: "sha:edited", State: "ok"},
	}}); err != nil {
		t.Fatal(err)
	}
	items, err := s.ToolsItems(protocol.ToolsFilter{}, func(string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, it := range items {
		got[it.Hash] = it.VersionNo
		for _, l := range it.Locations {
			if l.VersionNo != it.VersionNo {
				t.Errorf("copy of %s has version %d, item has %d", it.Hash, l.VersionNo, it.VersionNo)
			}
		}
	}
	if got["sha:1"] != 1 || got["sha:2"] != 2 || got["sha:edited"] != 0 {
		t.Fatalf("version numbers %v", got)
	}
}

func TestSkillNumberMigrationBackfillsByImportTime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gw.db")
	idx := -1
	for i, m := range migrations {
		if strings.Contains(m, "skill_versions ADD COLUMN number") {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatal("no migration adds skill_versions.number")
	}
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TABLE schema_version (version INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < idx; i++ {
		if _, err = db.Exec(migrations[i]); err != nil {
			t.Fatalf("migration %d: %v", i+1, err)
		}
		if _, err = db.Exec(`INSERT INTO schema_version(version) VALUES(?)`, i+1); err != nil {
			t.Fatal(err)
		}
	}
	// Imported out of key order: gamma z first, then gamma a, plus one other skill.
	rows := []struct {
		name, hash string
		at         int64
	}{{"gamma", "z", 100}, {"gamma", "a", 200}, {"delta", "q", 50}}
	for _, r := range rows {
		id := `["skill","` + r.name + `","` + r.hash + `"]`
		if _, err = db.Exec(`INSERT INTO tool_identities(id,kind,name,hash) VALUES(?,?,?,?)`, id, "skill", r.name, r.hash); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(`INSERT INTO skill_versions(item,hash,commit_id,node_id,location,imported_at) VALUES(?,?,?,?,?,?)`, id, r.hash, "c", "n", "l", r.at); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got := map[string]int{}
	rs, err := s.db.Query(`SELECT hash,number FROM skill_versions`)
	if err != nil {
		t.Fatal(err)
	}
	for rs.Next() {
		var h string
		var n int
		if err = rs.Scan(&h, &n); err != nil {
			t.Fatal(err)
		}
		got[h] = n
	}
	rs.Close()
	if got["z"] != 1 || got["a"] != 2 || got["q"] != 1 {
		t.Fatalf("backfill %v, want z=1 a=2 q=1", got)
	}
}

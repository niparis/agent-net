package gateway

import (
	"path/filepath"
	"testing"
)

func TestSkillStoreCreatesPrivateRepositoryAndDeduplicates(t *testing.T) {
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
	a, err := repo.put("item", "hash", []byte("fixture bundle"), "node", "path")
	if err != nil {
		t.Fatal(err)
	}
	b, err := repo.put("item", "hash", []byte("fixture bundle"), "node", "path")
	if err != nil {
		t.Fatal(err)
	}
	if a.Commit == "" || a.Commit != b.Commit {
		t.Fatalf("dedup: %+v %+v", a, b)
	}
}

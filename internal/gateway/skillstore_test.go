package gateway

import (
	"os"
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

func TestGatewayCreatesPrivateSkillRepository(t *testing.T) {
	dir := t.TempDir()
	g, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	info, err := os.Stat(filepath.Join(dir, "skills.git"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0700 {
		t.Fatalf("repository permissions: %v", info.Mode())
	}
	if _, err = os.Stat(filepath.Join(dir, "skills.git", "HEAD")); err != nil {
		t.Fatal(err)
	}
}

package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestWorkerWorktreeSkills(t *testing.T) {
	d := commitTestDaemon(t)
	repo := newRepo(t, "skills")
	var entries []skillManifestEntry
	for _, root := range []string{".claude/skills", ".agents/skills"} {
		p := filepath.Join(repo, root, "owned")
		if err := os.MkdirAll(p, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "SKILL.md"), []byte("skill"), 0640); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, skillManifestEntry{Path: p, Owned: true})
	}
	p := filepath.Join(repo, ".agents/skills/manual")
	if err := os.MkdirAll(p, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "SKILL.md"), []byte("manual"), 0644); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(entries)
	if err := os.WriteFile(filepath.Join(d.cfg.StateDir, "skill-manifest.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	gitWithEnv(t, repo, nil, "config", "core.excludesfile", "/dev/null")
	exclude := filepath.Join(repo, ".git/info/exclude")
	if err := os.WriteFile(exclude, []byte("/.claude/skills/owned/\n/.agents/skills/owned/\n"), 0644); err != nil {
		t.Fatal(err)
	}
	w := &worker{id: protocol.NewID("w"), path: repo}
	if err := d.createWorktree(context.Background(), w); err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		rel, _ := filepath.Rel(repo, e.Path)
		if _, err := os.Stat(filepath.Join(w.worktree, rel, "SKILL.md")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(w.worktree, ".agents/skills/manual")); !os.IsNotExist(err) {
		t.Fatal("unignored skill copied")
	}
	if s := gitWithEnv(t, w.worktree, nil, "status", "--porcelain"); s != "" {
		t.Fatalf("dirty: %s", s)
	}
	gitWithEnv(t, repo, nil, "worktree", "remove", w.worktree)
}

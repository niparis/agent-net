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
	ignored := filepath.Join(repo, ".agents/skills/ignored")
	if err := os.MkdirAll(ignored, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ignored, "SKILL.md"), []byte("ignored"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(ignored, "SKILL.md"), filepath.Join(entries[0].Path, "link")); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(entries)
	if err := os.WriteFile(filepath.Join(d.cfg.StateDir, "skill-manifest.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	gitWithEnv(t, repo, nil, "config", "core.excludesfile", "/dev/null")
	exclude := filepath.Join(repo, ".git/info/exclude")
	if err := os.WriteFile(exclude, []byte("/.claude/skills/owned/\n/.agents/skills/owned/\n/.agents/skills/ignored/\n"), 0644); err != nil {
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
	if info, err := os.Stat(filepath.Join(w.worktree, ".agents/skills/ignored/SKILL.md")); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("ignored skill or mode: %v %v", info, err)
	}
	if _, err := os.Lstat(filepath.Join(w.worktree, ".claude/skills/owned/link")); !os.IsNotExist(err) {
		t.Fatal("symlink copied")
	}
	if _, err := os.Stat(filepath.Join(w.worktree, ".agents/skills/manual")); !os.IsNotExist(err) {
		t.Fatal("unignored skill copied")
	}
	if s := gitWithEnv(t, w.worktree, nil, "status", "--porcelain"); s != "" {
		t.Fatalf("dirty: %s", s)
	}
	gitWithEnv(t, repo, nil, "worktree", "remove", w.worktree)
}

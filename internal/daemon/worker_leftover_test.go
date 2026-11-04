package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLeftoverFiltersBuildOutputs(t *testing.T) {
	for _, text := range []bool{false, true} {
		t.Run(map[bool]string{false: "all excluded", true: "text kept"}[text], func(t *testing.T) {
			repo := newRepo(t, "leftovers")
			base := gitOutT(t, repo, "rev-parse", "HEAD")
			for name, data := range map[string][]byte{"large.dat": make([]byte, 5000001), "binary": []byte("\x7fELFpayload"), "agent-net": []byte("build")} {
				if err := os.WriteFile(filepath.Join(repo, name), data, 0644); err != nil {
					t.Fatal(err)
				}
			}
			if text {
				os.WriteFile(filepath.Join(repo, "keep.txt"), []byte("hello"), 0644)
			}
			d := commitTestDaemon(t)
			w := &worker{id: "w_leftover_test", worktree: repo}
			saved := d.saveWorkerChanges(w, "save", "test", false)
			if saved != text {
				t.Fatalf("saved=%v want %v", saved, text)
			}
			if !text && gitOutT(t, repo, "rev-parse", "HEAD") != base {
				t.Fatal("empty commit")
			}
			if text && gitOutT(t, repo, "diff-tree", "--no-commit-id", "--name-only", "-r", "HEAD") != "keep.txt" {
				t.Fatal("build outputs committed")
			}
			if !strings.Contains(w.excludedLeftovers, "large.dat 5 MB") || !strings.Contains(w.excludedLeftovers, "binary") {
				t.Fatalf("missing report: %s", w.excludedLeftovers)
			}
		})
	}
}

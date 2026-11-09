package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestFileOverlapHintNamesAnotherWorkersChange(t *testing.T) {
	candidates := []overlapCandidate{{
		change: protocol.RecentWorkerChange{ChangeID: "ch_1", WorkerSession: "s_other", Title: "Fix the parser", MergedAt: 1760000000, MergedCommit: "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0"},
		files:  []string{"internal/parser.go"},
	}}
	line := fileOverlapHint("s_me", []string{"internal/parser.go", "README.md"}, candidates)
	for _, want := range []string{"ch_1", "s_other", "merged 2025-10-09", "agent_worker_rate defect_found_later on s_other", "fixes=ch_1"} {
		if !strings.Contains(line, want) {
			t.Errorf("hint %q does not contain %q", line, want)
		}
	}
}

func TestFileOverlapHintSkipsOwnChangeAndNoOverlap(t *testing.T) {
	candidates := []overlapCandidate{
		{change: protocol.RecentWorkerChange{ChangeID: "ch_own", WorkerSession: "s_me", Title: "My earlier work", MergedCommit: "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0"},
			files: []string{"README.md"}},
		{change: protocol.RecentWorkerChange{ChangeID: "ch_other", WorkerSession: "s_other", Title: "Other work", MergedCommit: "b1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0"},
			files: []string{"other.go"}},
	}
	if got := fileOverlapHint("s_me", []string{"README.md"}, candidates); got != "" {
		t.Errorf("files that touch nothing by another worker should get no hint: %q", got)
	}
}

func TestChangedFilesBetween(t *testing.T) {
	repo := newRepo(t, "hintrepo")
	base := gitOutT(t, repo, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "b.txt"), []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "a.txt", "b.txt")
	gitIn(t, repo, "commit", "-q", "-m", "two files")
	head := gitOutT(t, repo, "rev-parse", "HEAD")
	files, err := changedFilesBetween(context.Background(), repo, base, head)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(files, ",")
	if got != "a.txt,b.txt" {
		t.Fatalf("changed files = %q, want a.txt,b.txt", got)
	}
}

func TestCommitFileSet(t *testing.T) {
	repo := newRepo(t, "hintcommit")
	commit := gitOutT(t, repo, "rev-parse", "HEAD")
	files, err := commitFileSet(context.Background(), repo, commit)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0] != "README.md" {
		t.Fatalf("commit files = %v, want README.md", files)
	}
}

package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A worker that forked from an unmerged release train reports only its own
// file: the notice measures from the commit the worker started from, not from
// the merge-base with the default branch, which would list the whole train and
// push the notice past the body limit.
func TestNoticeDiffStatOnAStackedBranchNamesOnlyItsOwnFile(t *testing.T) {
	repo := newRepo(t, "stacked-notice")

	// The train is large enough that its stat alone exceeds the body limit.
	gitIn(t, repo, "checkout", "-q", "-b", "train")
	trainDir := filepath.Join(repo, "train")
	if err := os.MkdirAll(trainDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1000; i++ {
		name := fmt.Sprintf("%03d-%s-train.txt", i, strings.Repeat("x", 90))
		if err := os.WriteFile(filepath.Join(trainDir, name), []byte("train\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitIn(t, repo, "add", "train")
	gitIn(t, repo, "commit", "-q", "-m", "the release train")
	train := gitHead(t, repo, "HEAD")
	gitIn(t, repo, "checkout", "-q", "main")

	wt := filepath.Join(filepath.Dir(repo), "stacked-worker")
	gitIn(t, repo, "worktree", "add", "-q", "-b", "agent/work", wt, train)
	if err := os.WriteFile(filepath.Join(wt, "own.txt"), []byte("the worker's work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, wt, "add", "own.txt")
	gitIn(t, wt, "commit", "-q", "-m", "the worker's change")

	d := commitTestDaemon(t)
	w := &worker{id: "w_stacked", worktree: wt, branch: "agent/work", base: train,
		project: "stacked-notice", sessionID: "s_stacked", startedAt: time.Now()}
	n := d.notice(w, protocol.StatusDone, "", workerStats{})
	if !strings.Contains(n.DiffStat, "own.txt") {
		t.Fatalf("the notice's diff stat does not name the worker's file:\n%s", n.DiffStat)
	}
	if strings.Contains(n.DiffStat, "-train.txt") || strings.Contains(n.DiffStat, "1001 files changed") {
		t.Errorf("the notice's diff stat counts the unmerged train:\n%s", n.DiffStat)
	}
	body := protocol.FormatWorkerNotice(n, "done")
	if len(body) > protocol.MaxBodyBytes {
		t.Errorf("the notice body is %d bytes, limit %d", len(body), protocol.MaxBodyBytes)
	}
}

// The notice's diff stat is bounded like the delivery one: whole lines only,
// with a tail that says how many files were left out.
func TestNoticeDiffStatIsBoundedAtALineBoundary(t *testing.T) {
	const files = 900
	var stat strings.Builder
	for i := 0; i < files; i++ {
		fmt.Fprintf(&stat, " pkg/file-%04d-with-a-longer-name.txt | 2 +-\n", i)
	}
	stat.WriteString(" 900 files changed, 900 insertions(+)\n")

	got := noticeDiffStat(stat.String())
	if len(got) > protocol.MaxDiffStatBytes {
		t.Fatalf("bounded diff stat is %d bytes, limit %d", len(got), protocol.MaxDiffStatBytes)
	}
	lines := strings.Split(got, "\n")
	if len(lines) < 2 {
		t.Fatalf("bounded diff stat has no file lines and tail:\n%s", got)
	}
	for _, line := range lines[:len(lines)-1] {
		if !strings.Contains(line, "|") {
			t.Errorf("cut a line in the middle: %q", line)
		}
	}
	var omitted int
	if _, err := fmt.Sscanf(lines[len(lines)-1], "... and %d more files", &omitted); err != nil {
		t.Fatalf("the tail does not say how many files were left out: %q", lines[len(lines)-1])
	}
	if kept := len(lines) - 1; kept+omitted != files {
		t.Errorf("kept %d files and named %d left out, want %d in all", kept, omitted, files)
	}
	if strings.Contains(got, "files changed") {
		t.Errorf("the summary line counts files that are not listed:\n%s", got)
	}
}

// A worker with no recorded base still gets a diff stat: it measures from the
// merge-base with the default branch.
func TestNoticeDiffStatWithoutARecordedBaseUsesTheDefaultBranch(t *testing.T) {
	repo := newRepo(t, "no-base-notice")
	gitIn(t, repo, "checkout", "-q", "-b", "agent/no-base")
	if err := os.WriteFile(filepath.Join(repo, "own.txt"), []byte("the worker's work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "own.txt")
	gitIn(t, repo, "commit", "-q", "-m", "the worker's change")

	d := commitTestDaemon(t)
	w := &worker{id: "w_no_base", worktree: repo, branch: "agent/no-base"}
	stat, changed := d.worktreeFacts(w)
	if !changed || !strings.Contains(stat, "own.txt") {
		t.Fatalf("the notice's diff stat has no worker file: changed=%v\n%s", changed, stat)
	}
	if strings.Contains(stat, "README.md") {
		t.Errorf("the notice's diff stat counts the default branch's own files:\n%s", stat)
	}
}

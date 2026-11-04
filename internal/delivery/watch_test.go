package delivery

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testRepo is a temporary git repository driven through the git binary.
type testRepo struct {
	t   *testing.T
	dir string
	n   int
}

func newTestRepo(t *testing.T, initialBranch string) *testRepo {
	t.Helper()
	r := &testRepo{t: t, dir: t.TempDir()}
	r.git("init", "-q", "-b", initialBranch)
	return r
}

func (r *testRepo) git(args ...string) string {
	r.t.Helper()
	full := append([]string{
		"-c", "user.name=Test Author",
		"-c", "user.email=author@example.test",
		"-c", "commit.gpgsign=false",
		"-c", "core.hooksPath=/dev/null",
	}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = r.dir
	cmd.Env = append(os.Environ(),
		"LC_ALL=C",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_DATE=2026-11-04T10:00:00+0000",
		"GIT_COMMITTER_DATE=2026-11-04T10:00:00+0000",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// commit adds a file and commits it with the message, returns the new SHA.
func (r *testRepo) commit(message string) string {
	r.t.Helper()
	r.n++
	name := filepath.Join(r.dir, "file"+strings.Repeat("x", r.n)+".txt")
	if err := os.WriteFile(name, []byte(message), 0o644); err != nil {
		r.t.Fatal(err)
	}
	r.git("add", "-A")
	r.git("commit", "-q", "-m", message)
	return r.git("rev-parse", "HEAD")
}

func shas(cs []Commit) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.SHA
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestNewCommitsLinearOldestFirst(t *testing.T) {
	r := newTestRepo(t, "main")
	a := r.commit("a")
	b := r.commit("b")
	c := r.commit("c")
	d := r.commit("d")

	res, err := NewCommits(context.Background(), r.dir, "main", a)
	if err != nil {
		t.Fatal(err)
	}
	if res.Rewritten {
		t.Fatal("Rewritten must be false for a linear history")
	}
	if got, want := shas(res.Commits), []string{b, c, d}; !equal(got, want) {
		t.Fatalf("commits = %v, want %v", got, want)
	}
	first := res.Commits[0]
	if first.Subject != "b" {
		t.Errorf("subject = %q", first.Subject)
	}
	if first.Author != "Test Author <author@example.test>" {
		t.Errorf("author = %q", first.Author)
	}
	if first.Committer != "Test Author <author@example.test>" {
		t.Errorf("committer = %q", first.Committer)
	}
	if len(first.Parents) != 1 || first.Parents[0] != a {
		t.Errorf("parents = %v, want [%s]", first.Parents, a)
	}
	want := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	if !first.Time.Equal(want) {
		t.Errorf("time = %v, want %v", first.Time, want)
	}
	if first.IsMerge() {
		t.Error("linear commit reported as merge")
	}
}

func TestNewCommitsNothingNew(t *testing.T) {
	r := newTestRepo(t, "main")
	r.commit("a")
	tip := r.commit("b")

	res, err := NewCommits(context.Background(), r.dir, "main", tip)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Commits) != 0 || res.Rewritten {
		t.Fatalf("got %v rewritten=%v, want nothing", shas(res.Commits), res.Rewritten)
	}
}

func TestNewCommitsMergeIsFirstParentOnly(t *testing.T) {
	r := newTestRepo(t, "main")
	a := r.commit("a")
	r.git("checkout", "-q", "-b", "feature")
	f1 := r.commit("f1")
	f2 := r.commit("f2")
	r.git("checkout", "-q", "main")
	m1 := r.commit("m1")
	r.git("merge", "-q", "--no-ff", "-m", "merge feature", "feature")
	merge := r.git("rev-parse", "HEAD")

	res, err := NewCommits(context.Background(), r.dir, "main", a)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := shas(res.Commits), []string{m1, merge}; !equal(got, want) {
		t.Fatalf("commits = %v, want %v (side-branch commits must not appear)", got, want)
	}
	mc := res.Commits[1]
	if !mc.IsMerge() {
		t.Fatal("merge commit not reported as merge")
	}
	if len(mc.Parents) != 2 || mc.Parents[0] != m1 || mc.Parents[1] != f2 {
		t.Fatalf("parents = %v, want [%s %s]", mc.Parents, m1, f2)
	}
	heads := MergedHeads(mc)
	if len(heads) != 1 || heads[0] != f2 {
		t.Fatalf("MergedHeads = %v, want [%s]", heads, f2)
	}
	for _, c := range res.Commits {
		if c.SHA == f1 || c.SHA == f2 {
			t.Fatalf("side-branch commit %s leaked into the result", c.SHA)
		}
	}
	if got := MergedHeads(res.Commits[0]); len(got) != 0 {
		t.Fatalf("MergedHeads of a non-merge = %v, want none", got)
	}
}

func TestNewCommitsOctopusMergedHeads(t *testing.T) {
	r := newTestRepo(t, "main")
	a := r.commit("a")
	r.git("checkout", "-q", "-b", "one")
	one := r.commit("one")
	r.git("checkout", "-q", "main")
	r.git("checkout", "-q", "-b", "two")
	two := r.commit("two")
	r.git("checkout", "-q", "main")
	r.git("merge", "-q", "--no-ff", "-m", "octopus", "one", "two")

	res, err := NewCommits(context.Background(), r.dir, "main", a)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Commits) != 1 {
		t.Fatalf("commits = %v, want only the octopus merge", shas(res.Commits))
	}
	if got, want := MergedHeads(res.Commits[0]), []string{one, two}; !equal(got, want) {
		t.Fatalf("MergedHeads = %v, want %v", got, want)
	}
}

func TestNewCommitsFastForwardOfWholeBranch(t *testing.T) {
	r := newTestRepo(t, "main")
	a := r.commit("a")
	r.git("checkout", "-q", "-b", "feature")
	f1 := r.commit("f1")
	f2 := r.commit("f2")
	f3 := r.commit("f3")
	r.git("checkout", "-q", "main")
	r.git("merge", "-q", "--ff-only", "feature")

	res, err := NewCommits(context.Background(), r.dir, "main", a)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := shas(res.Commits), []string{f1, f2, f3}; !equal(got, want) {
		t.Fatalf("commits = %v, want %v", got, want)
	}
	if res.Rewritten {
		t.Fatal("a fast-forward is not a rewrite")
	}
}

func TestNewCommitsEmptySinceReturnsOnlyTip(t *testing.T) {
	r := newTestRepo(t, "main")
	r.commit("a")
	r.commit("b")
	tip := r.commit("c")

	res, err := NewCommits(context.Background(), r.dir, "main", "")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := shas(res.Commits), []string{tip}; !equal(got, want) {
		t.Fatalf("commits = %v, want only the tip %v", got, want)
	}
	if res.Rewritten {
		t.Fatal("first run is not a rewrite")
	}
}

func TestNewCommitsRewrittenHistory(t *testing.T) {
	r := newTestRepo(t, "main")
	r.commit("a")
	old := r.commit("b")
	r.git("reset", "-q", "--hard", "HEAD~1")
	r.commit("b2")
	tip := r.commit("c")

	res, err := NewCommits(context.Background(), r.dir, "main", old)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Rewritten {
		t.Fatal("Rewritten must be true when since is not an ancestor")
	}
	if got, want := shas(res.Commits), []string{tip}; !equal(got, want) {
		t.Fatalf("commits = %v, want only the tip %v", got, want)
	}
}

func TestNewCommitsUnknownSinceIsRewritten(t *testing.T) {
	r := newTestRepo(t, "main")
	r.commit("a")
	tip := r.commit("b")

	res, err := NewCommits(context.Background(), r.dir, "main", strings.Repeat("ab", 20))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Rewritten {
		t.Fatal("a since commit that no longer exists means rewritten history")
	}
	if got, want := shas(res.Commits), []string{tip}; !equal(got, want) {
		t.Fatalf("commits = %v, want only the tip %v", got, want)
	}
}

func TestNewCommitsParsesTrailers(t *testing.T) {
	r := newTestRepo(t, "main")
	a := r.commit("a")
	msg := "Add the thing\n\nA body paragraph.\nSecond line.\n\n" +
		"Agent-Net-Worker: w_123\n" +
		"Agent-Net-Model: claude-sonnet-5-5\n" +
		"Agent-Net-Harness: claude\n" +
		"Agent-Net-Difficulty: medium\n" +
		"Agent-Net-Caller: s_abc\n"
	b := r.commit(msg)
	c := r.commit("plain commit with no trailers")

	res, err := NewCommits(context.Background(), r.dir, "main", a)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := shas(res.Commits), []string{b, c}; !equal(got, want) {
		t.Fatalf("commits = %v, want %v", got, want)
	}
	withTrailers := res.Commits[0]
	if withTrailers.Subject != "Add the thing" {
		t.Errorf("subject = %q", withTrailers.Subject)
	}
	want := map[string]string{
		"Agent-Net-Worker":     "w_123",
		"Agent-Net-Model":      "claude-sonnet-5-5",
		"Agent-Net-Harness":    "claude",
		"Agent-Net-Difficulty": "medium",
		"Agent-Net-Caller":     "s_abc",
	}
	if len(withTrailers.Trailers) != len(want) {
		t.Fatalf("trailers = %v, want %v", withTrailers.Trailers, want)
	}
	for k, v := range want {
		if withTrailers.Trailers[k] != v {
			t.Errorf("trailer %s = %q, want %q", k, withTrailers.Trailers[k], v)
		}
	}
	if got := res.Commits[1].Trailers; len(got) != 0 {
		t.Errorf("plain commit trailers = %v, want none", got)
	}
}

func TestNewCommitsTrailerNotInBodyText(t *testing.T) {
	r := newTestRepo(t, "main")
	a := r.commit("a")
	// A "trailer-looking" line in the middle of the body is not a trailer.
	r.commit("Subject\n\nAgent-Net-Worker: w_fake\n\nThe rest of the body.\n")

	res, err := NewCommits(context.Background(), r.dir, "main", a)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Commits[0].Trailers; len(got) != 0 {
		t.Fatalf("trailers = %v, want none (line is not in the trailer block)", got)
	}
}

func TestDefaultBranchOriginHeadWins(t *testing.T) {
	r := newTestRepo(t, "main")
	tip := r.commit("a")
	r.git("branch", "develop")
	r.git("update-ref", "refs/remotes/origin/develop", tip)
	r.git("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/develop")

	got, err := DefaultBranch(context.Background(), r.dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != "develop" {
		t.Fatalf("DefaultBranch = %q, want develop (origin/HEAD beats the main fallback)", got)
	}
}

func TestDefaultBranchOriginHeadWithSlashInName(t *testing.T) {
	r := newTestRepo(t, "main")
	tip := r.commit("a")
	r.git("update-ref", "refs/remotes/origin/release/2026", tip)
	r.git("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/release/2026")

	got, err := DefaultBranch(context.Background(), r.dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != "release/2026" {
		t.Fatalf("DefaultBranch = %q, want release/2026", got)
	}
}

func TestDefaultBranchFallsBackToMain(t *testing.T) {
	r := newTestRepo(t, "main")
	r.commit("a")
	r.git("checkout", "-q", "-b", "topic")

	got, err := DefaultBranch(context.Background(), r.dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != "main" {
		t.Fatalf("DefaultBranch = %q, want main", got)
	}
}

func TestDefaultBranchFallsBackToMaster(t *testing.T) {
	r := newTestRepo(t, "master")
	r.commit("a")
	r.git("checkout", "-q", "-b", "topic")

	got, err := DefaultBranch(context.Background(), r.dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != "master" {
		t.Fatalf("DefaultBranch = %q, want master", got)
	}
}

func TestDefaultBranchFallsBackToCurrentBranch(t *testing.T) {
	r := newTestRepo(t, "trunk")
	r.commit("a")

	got, err := DefaultBranch(context.Background(), r.dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != "trunk" {
		t.Fatalf("DefaultBranch = %q, want trunk", got)
	}
}

func TestDefaultBranchDetachedHeadIsAnError(t *testing.T) {
	r := newTestRepo(t, "trunk")
	tip := r.commit("a")
	r.git("checkout", "-q", "--detach", tip)

	_, err := DefaultBranch(context.Background(), r.dir)
	if err == nil {
		t.Fatal("want an error when no default branch can be found")
	}
	if !strings.Contains(err.Error(), "default branch") {
		t.Fatalf("error should say what failed, got: %v", err)
	}
}

func TestNotARepoIsAnError(t *testing.T) {
	dir := t.TempDir()
	if _, err := DefaultBranch(context.Background(), dir); err == nil {
		t.Error("DefaultBranch: want an error outside a repository")
	}
	if _, err := NewCommits(context.Background(), dir, "main", ""); err == nil {
		t.Error("NewCommits: want an error outside a repository")
	}
}

func TestNewCommitsUnknownBranchIsAnError(t *testing.T) {
	r := newTestRepo(t, "main")
	r.commit("a")
	if _, err := NewCommits(context.Background(), r.dir, "nope", ""); err == nil {
		t.Fatal("want an error for a branch that does not exist")
	}
}

func TestNewCommitsRejectsOptionLikeArguments(t *testing.T) {
	r := newTestRepo(t, "main")
	a := r.commit("a")
	if _, err := NewCommits(context.Background(), r.dir, "--all", ""); err == nil {
		t.Error("branch starting with a dash must be refused")
	}
	if _, err := NewCommits(context.Background(), r.dir, "main", "--all"); err == nil {
		t.Error("since starting with a dash must be refused")
	}
	if _, err := NewCommits(context.Background(), r.dir, "main", a[:12]); err == nil {
		t.Error("an abbreviated since must be refused")
	}
}

func TestHooksAreNeverRun(t *testing.T) {
	r := newTestRepo(t, "main")
	a := r.commit("a")
	r.commit("b")

	marker := filepath.Join(t.TempDir(), "hook-ran")
	hooks := filepath.Join(r.dir, ".git", "hooks")
	script := "#!/bin/sh\ntouch " + marker + "\n"
	for _, name := range []string{"post-checkout", "post-merge", "reference-transaction", "post-rewrite", "pre-commit"} {
		if err := os.WriteFile(filepath.Join(hooks, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := DefaultBranch(context.Background(), r.dir); err != nil {
		t.Fatal(err)
	}
	if _, err := NewCommits(context.Background(), r.dir, "main", a); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a git hook ran")
	}
}

func TestCancelledContextStopsGit(t *testing.T) {
	r := newTestRepo(t, "main")
	r.commit("a")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewCommits(ctx, r.dir, "main", ""); err == nil {
		t.Fatal("want an error from a cancelled context")
	}
}

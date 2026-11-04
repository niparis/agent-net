package delivery

import (
	"context"
	"errors"
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
	// watcher is a second repository whose remote "origin" is dir. It is
	// created by the first watch call.
	watcher string
}

func newTestRepo(t *testing.T, initialBranch string) *testRepo {
	t.Helper()
	r := &testRepo{t: t, dir: t.TempDir()}
	r.git("init", "-q", "-b", initialBranch)
	return r
}

func (r *testRepo) git(args ...string) string {
	r.t.Helper()
	return r.gitIn(r.dir, args...)
}

// gitIn runs git in dir with the test identity and no hooks.
func (r *testRepo) gitIn(dir string, args ...string) string {
	r.t.Helper()
	full := append([]string{
		"-c", "user.name=Test Author",
		"-c", "user.email=author@example.test",
		"-c", "commit.gpgsign=false",
		"-c", "core.hooksPath=/dev/null",
	}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
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

// watch runs NewCommits for the main branch of r, as seen from the watcher
// repository through a fetch.
func (r *testRepo) watch(since string) (WatchResult, error) {
	r.t.Helper()
	return NewCommits(context.Background(), r.watchReq(since))
}

func (r *testRepo) watchReq(since string) WatchRequest {
	r.t.Helper()
	if r.watcher == "" {
		r.watcher = r.t.TempDir()
		r.gitIn(r.watcher, "init", "-q", "-b", "trunk")
		r.gitIn(r.watcher, "remote", "add", "origin", r.dir)
	}
	return WatchRequest{RepoDir: r.watcher, Remote: "origin", RemoteURL: r.dir, Branch: "main", Since: since}
}

// rawCommit writes a commit object exactly as given, even a malformed one.
func (r *testRepo) rawCommit(body string) string {
	r.t.Helper()
	cmd := exec.Command("git", "hash-object", "-t", "commit", "-w", "--literally", "--stdin")
	cmd.Dir = r.dir
	cmd.Stdin = strings.NewReader(body)
	out, err := cmd.Output()
	if err != nil {
		r.t.Fatalf("hash-object: %v", err)
	}
	return strings.TrimSpace(string(out))
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

	res, err := r.watch(a)
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

	res, err := r.watch(tip)
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

	res, err := r.watch(a)
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

	res, err := r.watch(a)
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

	res, err := r.watch(a)
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

	res, err := r.watch("")
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

	res, err := r.watch(old)
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

	res, err := r.watch(strings.Repeat("ab", 20))
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

	res, err := r.watch(a)
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

	res, err := r.watch(a)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Commits[0].Trailers; len(got) != 0 {
		t.Fatalf("trailers = %v, want none (line is not in the trailer block)", got)
	}
}

func TestNotARepoIsAnError(t *testing.T) {
	dir := t.TempDir()
	if _, err := NewCommits(context.Background(), WatchRequest{RepoDir: dir, Remote: "origin", Branch: "main"}); err == nil {
		t.Error("NewCommits: want an error outside a repository")
	}
}

func TestNewCommitsUnknownBranchIsAnError(t *testing.T) {
	r := newTestRepo(t, "main")
	r.commit("a")
	req := r.watchReq("")
	req.Branch = "nope"
	if _, err := NewCommits(context.Background(), req); err == nil {
		t.Fatal("want an error for a branch that does not exist")
	}
}

func TestNewCommitsRejectsBadArguments(t *testing.T) {
	r := newTestRepo(t, "main")
	a := r.commit("a")
	cases := map[string]func(q *WatchRequest){
		"option branch":     func(q *WatchRequest) { q.Branch = "--all" },
		"bad branch":        func(q *WatchRequest) { q.Branch = "a..b" },
		"no branch":         func(q *WatchRequest) { q.Branch = "" },
		"option remote":     func(q *WatchRequest) { q.Remote = "--upload-pack=touch x" },
		"no remote":         func(q *WatchRequest) { q.Remote = "" },
		"option since":      func(q *WatchRequest) { q.Since = "--all" },
		"abbreviated since": func(q *WatchRequest) { q.Since = a[:12] },
		"branch name since": func(q *WatchRequest) { q.Since = "main" },
		"no repo":           func(q *WatchRequest) { q.RepoDir = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			req := r.watchReq(a)
			mutate(&req)
			if _, err := NewCommits(context.Background(), req); err == nil {
				t.Fatal("want an error")
			}
		})
	}
}

// S1: the watcher fetches, so it sees what landed on the remote even when
// the local repository was not updated.
func TestNewCommitsFetchesFromRemote(t *testing.T) {
	r := newTestRepo(t, "main")
	a := r.commit("a")
	res, err := r.watch("")
	if err != nil {
		t.Fatal(err)
	}
	if got := shas(res.Commits); !equal(got, []string{a}) {
		t.Fatalf("first run = %v, want the tip %s", got, a)
	}
	b := r.commit("b")
	c := r.commit("c")
	res, err = r.watch(a)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := shas(res.Commits), []string{b, c}; !equal(got, want) {
		t.Fatalf("commits = %v, want %v", got, want)
	}
}

// S1: the remote-tracking ref is named in full, so a local branch or tag
// with a shadowing name changes nothing.
func TestNewCommitsWatchesRemoteTrackingRef(t *testing.T) {
	r := newTestRepo(t, "main")
	a := r.commit("a")
	if _, err := r.watch(""); err != nil {
		t.Fatal(err)
	}
	b := r.commit("b")
	for _, ref := range []string{"refs/heads/main", "refs/tags/main", "refs/tags/origin/main", "refs/heads/origin/main"} {
		r.gitIn(r.watcher, "update-ref", ref, a)
	}
	res, err := r.watch(a)
	if err != nil {
		t.Fatal(err)
	}
	if got := shas(res.Commits); !equal(got, []string{b}) {
		t.Fatalf("commits = %v, want [%s]", got, b)
	}
}

// S1: fields are read from the raw commit object, so control characters in
// a name or a subject cannot shift them.
func TestNewCommitsOddCharactersParse(t *testing.T) {
	r := newTestRepo(t, "main")
	a := r.commit("a")
	tree := r.git("rev-parse", "HEAD^{tree}")
	odd := r.rawCommit("tree " + tree + "\nparent " + a +
		"\nauthor Odd\x1fName\x00x <o@example.test> 1793790000 +0000" +
		"\ncommitter C\x1eD <c@example.test> 1793790001 +0100" +
		"\n\nsubject \x1e and \x1f inside\nsecond subject line\n\nbody\n\nAgent-Net-Worker: w_odd\x1fx\n")
	r.git("update-ref", "refs/heads/main", odd)

	res, err := r.watch(a)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Commits) != 1 {
		t.Fatalf("commits = %v", shas(res.Commits))
	}
	c := res.Commits[0]
	if c.ParseError != "" {
		t.Fatalf("ParseError = %q", c.ParseError)
	}
	if c.SHA != odd || len(c.Parents) != 1 || c.Parents[0] != a {
		t.Fatalf("commit = %+v", c)
	}
	if c.Author != "Odd\x1fName\x00x <o@example.test>" || c.Committer != "C\x1eD <c@example.test>" {
		t.Fatalf("author %q, committer %q", c.Author, c.Committer)
	}
	if c.Subject != "subject \x1e and \x1f inside second subject line" {
		t.Fatalf("subject = %q", c.Subject)
	}
	if !c.Time.Equal(time.Unix(1793790001, 0)) || c.Time.Location() != time.UTC {
		t.Fatalf("time = %v", c.Time)
	}
	if got := c.Trailer("agent-net-worker"); got != "w_odd\x1fx" {
		t.Fatalf("trailer = %q (all %v)", got, c.Trailers)
	}
}

// S1: a commit the watcher cannot read is reported as a skip (no parents,
// no trailers, ParseError set), never as an error that stops the watcher.
func TestNewCommitsUnparseableCommitIsSkip(t *testing.T) {
	r := newTestRepo(t, "main")
	a := r.commit("a")
	tree := r.git("rev-parse", "HEAD^{tree}")
	bad := r.rawCommit("tree " + tree + "\nparent " + a +
		"\nauthor A <a@example.test> 1793790000 +0000\ncommitter C <c@example.test> notatime +0000" +
		"\n\nbad\n\nAgent-Net-Worker: w_bad\n")
	good := r.rawCommit("tree " + tree + "\nparent " + bad +
		"\nauthor A <a@example.test> 1793790000 +0000\ncommitter C <c@example.test> 1793790000 +0000\n\ngood\n")
	r.git("update-ref", "refs/heads/main", good)

	res, err := r.watch(a)
	if err != nil {
		t.Fatalf("an unparseable commit stopped the watcher: %v", err)
	}
	if got, want := shas(res.Commits), []string{bad, good}; !equal(got, want) {
		t.Fatalf("commits = %v, want %v", got, want)
	}
	b := res.Commits[0]
	if b.ParseError == "" || len(b.Parents) != 0 || len(b.Trailers) != 0 || b.Trailer("Agent-Net-Worker") != "" {
		t.Fatalf("bad commit = %+v, want a skip with nothing that looks like evidence", b)
	}
	if res.Commits[1].ParseError != "" || res.Commits[1].Subject != "good" {
		t.Fatalf("good commit = %+v", res.Commits[1])
	}
}

func TestParseTrailers(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		want map[string]string
	}{
		{"subject only", "Key: value\n", map[string]string{}},
		{"plain block", "S\n\nbody\n\nA-B: 1\nC: 2\n", map[string]string{"A-B": "1", "C": "2"}},
		{"first value wins", "S\n\nA: 1\nA: 2\n", map[string]string{"A": "1"}},
		{"continuation is unfolded", "S\n\nA: one\n  two\nB: x\n", map[string]string{"A": "one two", "B": "x"}},
		{"space before colon", "S\n\nKey : v\n", map[string]string{"Key": "v"}},
		{"not the last paragraph", "S\n\nA: 1\n\nprose\n", map[string]string{}},
		{"mixed block without git trailer", "S\n\nA: 1\nprose line\n", map[string]string{}},
		{"mixed block with Signed-off-by", "S\n\nprose line\nA: 1\nSigned-off-by: X <x@example.test>\n",
			map[string]string{"A": "1", "Signed-off-by": "X <x@example.test>"}},
		{"trailing blank lines", "S\n\nA: 1\n\n\n", map[string]string{"A": "1"}},
		{"comment lines skipped", "S\n\nA: 1\n# note\nB: 2\n", map[string]string{"A": "1", "B": "2"}},
		{"bad key", "S\n\nA_B: 1\n", map[string]string{}},
		{"patch divider", "S\n\nA: 1\n---\nB: 2\n", map[string]string{"A": "1"}},
		{"crlf", "S\r\n\r\nA: 1\r\n", map[string]string{"A": "1"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseTrailers(c.msg)
			if len(got) != len(c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
			for k, v := range c.want {
				if got[k] != v {
					t.Fatalf("got %v, want %v", got, c.want)
				}
			}
		})
	}
}

func TestCommitTrailerIgnoresCase(t *testing.T) {
	c := Commit{Trailers: map[string]string{"Agent-Net-Worker": "w_1"}}
	for _, k := range []string{"Agent-Net-Worker", "agent-net-worker", "AGENT-NET-WORKER"} {
		if got := c.Trailer(k); got != "w_1" {
			t.Errorf("Trailer(%q) = %q", k, got)
		}
	}
	if got := c.Trailer("Agent-Net-Model"); got != "" {
		t.Errorf("missing trailer = %q", got)
	}
}

// S14: the watcher fetches, and a fetch would run the reference-transaction
// hook. The control proves the hook is live; the watcher must not run it.
func TestNewCommitsNeverRunsHooks(t *testing.T) {
	r := newTestRepo(t, "main")
	a := r.commit("a")
	if _, err := r.watch(""); err != nil {
		t.Fatal(err)
	}
	r.commit("b")
	marker := filepath.Join(t.TempDir(), "hook-ran")
	hooks := filepath.Join(r.watcher, ".git", "hooks")
	script := "#!/bin/sh\ntouch " + marker + "\n"
	if err := os.WriteFile(filepath.Join(hooks, "reference-transaction"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	control := exec.Command("git", "fetch", "-q", "origin", "+refs/heads/main:refs/control/main")
	control.Dir = r.watcher
	if out, err := control.CombinedOutput(); err != nil {
		t.Fatalf("control fetch: %v\n%s", err, out)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("control: a plain fetch did not run the hook, so this test proves nothing")
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}

	res, err := r.watch(a)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Commits) != 1 {
		t.Fatalf("commits = %v: the watcher's fetch did not update the ref", shas(res.Commits))
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
	if _, err := NewCommits(ctx, r.watchReq("")); err == nil {
		t.Fatal("want an error from a cancelled context")
	}
}

func TestNewCommitsRequiresAndPinsRemoteURL(t *testing.T) {
	r := newTestRepo(t, "main")
	r.commit("a")
	req := r.watchReq("")
	req.RemoteURL = ""
	if _, err := NewCommits(context.Background(), req); err == nil {
		t.Fatal("want an error without RemoteURL")
	}
	// The watcher's own pushurl is redirected: refused, nothing fetched.
	req = r.watchReq("")
	r.gitIn(r.watcher, "config", "remote.origin.pushurl", t.TempDir())
	if _, err := NewCommits(context.Background(), req); !errors.Is(err, ErrRemoteURL) {
		t.Fatalf("err = %v, want ErrRemoteURL", err)
	}
}

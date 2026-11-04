package delivery

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// B1: a replace ref (refs/replace is shared by every worktree) makes git show
// other content for a reviewed commit ID. Merge, RunStep and NewCommits must
// refuse to work in such a repository.

func TestMergeRefusesReplaceRef(t *testing.T) {
	f := newMergeFx(t)
	head := f.branch("feat", f.base, "a.txt", "reviewed\n")
	evil := f.branch("evil", f.base, "a.txt", "not reviewed\n")
	mergeGit(t, f.repo, "replace", head, evil)
	f.advanceOrigin("o.txt", "o\n")
	before := f.originMain()

	_, err := Merge(context.Background(), f.req("feat", head, MergeModeCommit))
	if !errors.Is(err, ErrAlteredHistory) {
		t.Fatalf("err = %v, want ErrAlteredHistory", err)
	}
	if got := f.originMain(); got != before {
		t.Fatalf("origin main moved to %s", got)
	}
}

func TestRunStepRefusesReplaceRef(t *testing.T) {
	f := newRunFx(t)
	good := filepath.Join(t.TempDir(), "good")
	evilMarker := filepath.Join(t.TempDir(), "evil")
	s := f.script("release.sh", "touch "+good+"\n")
	reviewed := f.head
	// A second commit with the same parent and other content.
	runWriteFile(t, f.dir, "scripts/release.sh", "#!/bin/sh\ntouch "+evilMarker+"\n")
	runTestGit(t, f.dir, "commit", "-q", "-am", "evil")
	evil := runTestGit(t, f.dir, "rev-parse", "HEAD")
	runTestGit(t, f.dir, "reset", "-q", "--soft", reviewed)
	runTestGit(t, f.dir, "replace", reviewed, evil)

	cmd := f.cmd(s)
	cmd.Commit = reviewed
	if _, err := RunStep(context.Background(), cmd); !errors.Is(err, ErrAlteredHistory) {
		t.Fatalf("err = %v, want ErrAlteredHistory", err)
	}
	for _, m := range []string{good, evilMarker} {
		if _, err := os.Stat(m); err == nil {
			t.Fatalf("a command ran: %s", m)
		}
	}
}

func TestNewCommitsRefusesReplaceRef(t *testing.T) {
	r := newTestRepo(t, "main")
	a := r.commit("a")
	b := r.commit("plain")
	r.git("checkout", "-q", "--detach", a)
	fake := r.commit("fake\n\nAgent-Net-Worker: w_fake\n")
	r.git("checkout", "-q", "-b", "fake")
	r.git("checkout", "-q", "main")
	if _, err := r.watch(a); err != nil {
		t.Fatal(err)
	}
	r.gitIn(r.watcher, "fetch", "-q", "origin", "+refs/heads/fake:refs/remotes/origin/fake")
	r.gitIn(r.watcher, "replace", b, fake)

	res, err := r.watch(a)
	if !errors.Is(err, ErrAlteredHistory) {
		t.Fatalf("err = %v, want ErrAlteredHistory (result %+v)", err, res)
	}
}

// Every git call ignores replace refs, even before the refusal.
func TestGitIgnoresReplaceRefs(t *testing.T) {
	r := newTestRepo(t, "main")
	a := r.commit("a")
	b := r.commit("real")
	r.git("checkout", "-q", "--detach", a)
	fake := r.commit("fake")
	r.git("checkout", "-q", "main")
	r.git("replace", b, fake)
	if got := r.git("log", "-1", "--format=%s", b); got != "fake" {
		t.Fatalf("control: plain git shows %q, the replace ref is not active", got)
	}
	got, err := gitIn{dir: r.dir}.run(context.Background(), "cat-file", "commit", b)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(got, "\nreal") {
		t.Fatalf("git showed the replacement:\n%s", got)
	}
}

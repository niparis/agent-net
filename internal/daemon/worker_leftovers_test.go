package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/repoinfo"
)

// The hand-back record paths of the daemon, without a socket: a state
// database, the repo resolver and a fake gateway for the delivery frames. A
// frame the fake does not answer fails as when the gateway is down.
func handbackRecordDaemon(t *testing.T) (*Daemon, *fakeDeliveryGateway) {
	t.Helper()
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	st, err := openState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.close() })
	d := commitTestDaemon(t)
	f := newFakeDeliveryGateway()
	f.answer(protocol.FrameDeliveryNeedsPush, func(json.RawMessage) (any, error) { return protocol.DeliveryNeedsPushResult{}, nil })
	d.st, d.repo, d.cfg.testGatewayCall = st, repoinfo.New(), f.call
	return d, f
}

// leftoverWorker is a worker on branch feat/x of deliveryWorktree, which holds
// one commit beyond main.
func leftoverWorker(t *testing.T) (w *worker, repo, wt string) {
	t.Helper()
	repo, wt = deliveryWorktree(t)
	w = &worker{id: "w_leftovers", worktree: wt, branch: "feat/x", base: gitHead(t, repo, "main"), owner: "s_owner",
		sessionID: "s_worker", project: "demo", title: "Add a", difficultyUsed: "normal"}
	return w, repo, wt
}

// withOrigin gives the repository a bare origin remote.
func withOrigin(t *testing.T, repo string) {
	t.Helper()
	origin := filepath.Join(t.TempDir(), "origin.git")
	gitIn(t, filepath.Dir(origin), "init", "-q", "--bare", origin)
	gitIn(t, repo, "remote", "add", "origin", origin)
	gitIn(t, repo, "push", "-q", "origin", "main")
}

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, data := range files {
		file := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// submitted is the one change.submit the fake gateway got.
func submitted(t *testing.T, f *fakeDeliveryGateway) protocol.ChangeSubmitRequest {
	t.Helper()
	frames := f.frames(protocol.FrameChangeSubmit)
	var q protocol.ChangeSubmitRequest
	if len(frames) != 1 || json.Unmarshal(frames[len(frames)-1], &q) != nil {
		t.Fatalf("change.submit frames: %s", frames)
	}
	return q
}

var handbackDone = protocol.WorkerHandbackRequest{Report: "done", Status: protocol.StatusDone}

// handBack runs the worker's hand-back through the daemon and returns the
// notice it sent. With no gateway link the notice waits in the outbox.
func handBack(t *testing.T, d *Daemon, w *worker, req protocol.WorkerHandbackRequest) string {
	t.Helper()
	if _, err := d.handbackFlow(context.Background(), w, req, "", "", "", false, nil); err != nil {
		t.Fatalf("hand-back: %v", err)
	}
	queued, err := d.st.outbox()
	if err != nil || len(queued) != 1 {
		t.Fatalf("outbox: %d messages, err %v", len(queued), err)
	}
	return queued[0].msg.Body
}

// On 2026-11-25 a change recorded as its head a commit the daemon made of an
// empty .claude/scheduled_tasks.json, a file Claude Code writes for itself.
// Files a harness makes for itself are not the worker's work: the hand-back
// does not commit them, the notice does not list them, and they do not make
// the change "uncommitted".
func TestHarnessFilesAreNotLeftovers(t *testing.T) {
	w, _, wt := leftoverWorker(t)
	head := gitHead(t, wt, "HEAD")
	writeFiles(t, wt, map[string]string{
		".claude/scheduled_tasks.json":      "",
		".claude/scheduled_tasks.lock":      "4242",
		".claude/worktrees/agent-1/a.txt":   "a\n",
		"sub/.claude/scheduled_tasks.json":  "",
		".opencode/placeholder":             "",
		".codex/empty":                      "",
		".claude/checkpoints/one/state.bin": "x",
	})
	d := commitTestDaemon(t)

	if committed := d.commitAtHandback(w, handbackDone); committed != "" {
		t.Errorf("the hand-back committed harness files as %s", committed)
	}
	if got := gitHead(t, wt, "HEAD"); got != head {
		t.Errorf("HEAD moved to %s:\n%s", got, gitOutT(t, wt, "show", "--stat", "HEAD"))
	}
	if files := untrackedWorkFiles(context.Background(), wt); len(files) != 0 {
		t.Errorf("the notice lists harness files as untracked: %v", files)
	}
	if dirty, err := branchUncommitted(context.Background(), wt, "feat/x"); err != nil || dirty {
		t.Errorf("harness files make the change uncommitted: dirty=%t err=%v", dirty, err)
	}
}

// Worker notices listed tracked PNG files as "not committed (build output or
// large file)" while the worktree held no change to them. Only files that
// differ from the worker's own head are leftovers, and only those are listed.
func TestLeftoverListNamesOnlyChangedFiles(t *testing.T) {
	w, _, wt := leftoverWorker(t)
	png := "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"
	writeFiles(t, wt, map[string]string{"docs/img/a.png": png, "docs/img/b.png": png})
	gitIn(t, wt, "add", "docs")
	gitIn(t, wt, "commit", "-q", "-m", "images")
	writeFiles(t, wt, map[string]string{"docs/img/b.png": png + "\x00changed", "dirty.txt": "dirty\n"})
	d := commitTestDaemon(t)

	if d.commitAtHandback(w, handbackDone) == "" {
		t.Fatal("the leftover was not committed")
	}
	if got := w.excludedLeftovers; got != "docs/img/b.png 0 MB" {
		t.Errorf("not committed list %q, want only the changed docs/img/b.png", got)
	}
	writeFiles(t, wt, map[string]string{"more.txt": "more\n"})
	if got := commitWorkerEndWIP(context.Background(), wt, "feat/x", "stopped"); got.SHA == "" || got.Skipped != "docs/img/b.png 0 MB" {
		t.Errorf("WIP commit %+v, want only the changed docs/img/b.png skipped", got)
	}
}

// The worker pushed its head for a PR and left a real file uncommitted. The
// daemon keeps the file safe in a commit off the branch, so the branch and
// the change stay at the PR head. Before, the daemon committed the file on
// the branch, and the change recorded a head that was not on GitHub.
func TestPushedHeadStaysTheChangeHead(t *testing.T) {
	w, repo, wt := leftoverWorker(t)
	withOrigin(t, repo)
	gitIn(t, wt, "push", "-q", "origin", "HEAD:refs/heads/fix/pr")
	head := gitHead(t, wt, "HEAD")
	writeFiles(t, wt, map[string]string{"notes.txt": "notes\n"})
	d, f := handbackRecordDaemon(t)

	notice := handBack(t, d, w, handbackDone)
	if got := gitHead(t, wt, "feat/x"); got != head {
		t.Fatalf("the branch moved from the pushed head %s to %s", head, got)
	}
	ref := "refs/agent-net/leftovers/" + w.id
	if files := gitOutT(t, wt, "diff-tree", "--no-commit-id", "--name-only", "-r", ref); files != "notes.txt" {
		t.Errorf("kept files %q, want notes.txt", files)
	}
	if parent := gitHead(t, wt, ref+"^"); parent != head {
		t.Errorf("the kept commit's parent is %s, want the head %s", parent, head)
	}
	if _, err := os.Stat(filepath.Join(wt, "notes.txt")); err != nil {
		t.Errorf("the leftover left the worktree: %v", err)
	}
	if q := submitted(t, f); q.Branch != "feat/x" || q.HeadCommit != head || q.Uncommitted {
		t.Errorf("submit branch=%q head=%q uncommitted=%t, want feat/x at %s, committed", q.Branch, q.HeadCommit, q.Uncommitted, head)
	}
	for _, want := range []string{
		"the daemon kept your leftover changes as " + gitOutT(t, wt, "rev-parse", "--short", ref) + " in " + ref + ", not on the branch",
		"change: ch_1\nthe change records the head you pushed (origin/fix/pr)\n",
	} {
		if !strings.Contains(notice, want) {
			t.Errorf("notice lacks %q:\n%s", want, notice)
		}
	}
	if strings.Contains(notice, "the daemon committed your leftover changes") {
		t.Errorf("notice says the daemon committed on the branch:\n%s", notice)
	}
}

// On 2026-11-19 a worker's work was a PR on another branch, and its worktree
// was back on the start branch with 37 files left. The daemon committed them
// and recorded that commit as the head. The change now records the PR head on
// its branch, and the files are kept off both branches.
func TestPushedWorkOnAnotherBranchIsTheChange(t *testing.T) {
	w, repo, wt := leftoverWorker(t)
	withOrigin(t, repo)
	w.base = gitHead(t, wt, "HEAD") // the start branch holds no work
	gitIn(t, wt, "checkout", "-q", "-b", "fix/pr")
	writeFiles(t, wt, map[string]string{"fix.txt": "fix\n"})
	gitIn(t, wt, "add", "fix.txt")
	gitIn(t, wt, "commit", "-q", "-m", "fix")
	pr := gitHead(t, wt, "HEAD")
	gitIn(t, wt, "push", "-q", "origin", "fix/pr")
	gitIn(t, wt, "checkout", "-q", "feat/x")
	writeFiles(t, wt, map[string]string{"scratch/one.txt": "1\n", "scratch/two.txt": "2\n"})
	d, f := handbackRecordDaemon(t)

	notice := handBack(t, d, w, handbackDone)
	if got := gitHead(t, wt, "feat/x"); got != w.base {
		t.Errorf("the start branch moved to %s", got)
	}
	if q := submitted(t, f); q.Branch != "fix/pr" || q.HeadCommit != pr || q.Uncommitted {
		t.Errorf("submit branch=%q head=%q uncommitted=%t, want fix/pr at %s", q.Branch, q.HeadCommit, q.Uncommitted, pr)
	}
	want := "change: ch_1\nrecorded branch: fix/pr\nrecorded head: " + pr[:7] + "\nthe change records the head you pushed (origin/fix/pr)\n"
	if !strings.Contains(notice, want) || !strings.Contains(notice, "kept your leftover changes as ") {
		t.Errorf("notice lacks %q and the kept line:\n%s", want, notice)
	}
}

// A report that names the head claims it, as a push does.
func TestReportNamedHeadStaysTheChangeHead(t *testing.T) {
	w, _, wt := leftoverWorker(t)
	head := gitHead(t, wt, "HEAD")
	writeFiles(t, wt, map[string]string{"notes.txt": "notes\n"})
	d, f := handbackRecordDaemon(t)
	req := protocol.WorkerHandbackRequest{Report: "The work is commit " + head[:9] + " on feat/x.", Status: protocol.StatusDone}

	if committed := d.commitAtHandback(w, req); committed != "" {
		t.Errorf("the daemon committed on the named head as %s", committed)
	}
	rec := d.submitHandbackChange(context.Background(), w, nil, req.Report)
	if rec.changeID == "" || rec.pick.claimed != "named" {
		t.Fatalf("record %+v", rec)
	}
	if q := submitted(t, f); q.HeadCommit != head {
		t.Errorf("submit head %s, want the named %s", q.HeadCommit, head)
	}
}

// A remote branch at the head that the worker did not push claims nothing: a
// continued worker's uncommitted edits still go on the branch and into the
// change, as before. The daemon pushed the branch for a remote step owner, or
// the worker's branch is at the tip of main after a pull.
func TestPushesNotByTheWorkerClaimNothing(t *testing.T) {
	cases := map[string]func(t *testing.T, repo, wt string){
		"the daemon pushed the branch": func(t *testing.T, repo, wt string) {
			gitIn(t, wt, "push", "-q", "origin", "feat/x")
			markDaemonPush(context.Background(), wt, "feat/x", gitHead(t, wt, "HEAD"))
		},
		"the branch is at the tip of main": func(t *testing.T, repo, wt string) {
			gitIn(t, wt, "push", "-q", "origin", "feat/x:main")
			gitIn(t, repo, "fetch", "-q", "origin")
		},
	}
	for name, push := range cases {
		t.Run(name, func(t *testing.T) {
			w, repo, wt := leftoverWorker(t)
			withOrigin(t, repo)
			push(t, repo, wt)
			writeFiles(t, wt, map[string]string{"edit.txt": "edit\n"})
			d := commitTestDaemon(t)

			committed := d.commitAtHandback(w, handbackDone)
			if committed == "" || committed != gitOutT(t, wt, "rev-parse", "--short", "feat/x") {
				t.Errorf("committed %q, want the edit committed on the branch", committed)
			}
		})
	}
}

// A hand-back made while the gateway link is down was sent to the outbox
// without its change record, so the change stayed on its old head. It is now
// saved, and the next connect records the change at the head the worktree
// has then.
func TestHandbackWhileGatewayDownRecordsTheHeadWhenSent(t *testing.T) {
	w, _, wt := leftoverWorker(t)
	d, f := handbackRecordDaemon(t)
	d.workers = map[string]*worker{w.id: w}
	submit := f.answers[protocol.FrameChangeSubmit]
	f.answer(protocol.FrameChangeSubmit, func(json.RawMessage) (any, error) { return nil, errNotConnected })

	tail := &handbackTail{req: handbackDone, sessionID: w.sessionID, complete: true}
	if _, err := d.finishHandback(context.Background(), w, tail); !errors.Is(err, errHandbackDeferred) {
		t.Fatalf("hand-back while the gateway is down: %v", err)
	}
	saved, ok := d.st.pendingHandback(w.id)
	if !ok {
		t.Fatal("the hand-back was not saved for the next connect")
	}
	if !w.handbackPending {
		t.Error("the worker may get nudges while its hand-back waits")
	}

	// The worker commits once more before the link is back.
	writeFiles(t, wt, map[string]string{"late.txt": "late\n"})
	gitIn(t, wt, "add", "late.txt")
	gitIn(t, wt, "commit", "-q", "-m", "late")
	f.answer(protocol.FrameChangeSubmit, submit)
	f.mu.Lock()
	delete(f.sent, protocol.FrameChangeSubmit)
	f.mu.Unlock()
	d.replayHandback(w.id, saved)

	if q := submitted(t, f); q.HeadCommit != gitHead(t, wt, "HEAD") {
		t.Errorf("the change records %s, want the head %s", q.HeadCommit, gitHead(t, wt, "HEAD"))
	}
}

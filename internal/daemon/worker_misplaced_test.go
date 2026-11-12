package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// gitClean runs git without the commit hook and trailer variables of the
// runner's own worker environment, so a commit made here carries only the
// trailer the test writes.
func gitClean(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	for _, item := range os.Environ() {
		if strings.HasPrefix(item, "GIT_CONFIG_") || strings.HasPrefix(item, "AGENT_NET_TRAILER_") {
			continue
		}
		cmd.Env = append(cmd.Env, item)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// A worker that commits in the worktree of the worker it was based on leaves
// its own branch empty. Its hand-back says where the commits went, so the
// caller is not left with a silent empty change.
func TestHandbackNoticeNamesMisplacedCommits(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)

	const base = "agent-net/w_other"
	gitOutT(t, repo, "branch", base, "main")
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1",
		Task: "FAKE:ignore wait for the hand-back", Isolation: protocol.IsolationWorktree, Base: base})
	if res.Branch == base {
		t.Fatalf("the worker took the base branch as its own: %s", res.Branch)
	}
	if res.Task == nil || !strings.Contains(res.Task.Body, "already starts at the head of "+base) {
		t.Fatalf("the brief does not say the worktree starts at %s: %+v", base, res.Task)
	}

	// The worker whose branch this is commits in its own worktree, with the
	// trailer that names this worker: that is what a worker in the wrong
	// worktree leaves behind.
	other := filepath.Join(t.TempDir(), "other")
	gitOutT(t, repo, "worktree", "add", other, base)
	if err := os.WriteFile(filepath.Join(other, "other.txt"), []byte("other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOutT(t, other, "add", "other.txt")
	gitClean(t, other, "commit", "-m", "work for the other task\n\nAgent-Net-Worker: "+res.SessionID)

	// The worker hands back with nothing on its own branch.
	var sent protocol.MessageSent
	mustJSON(t, w.cli.mustCall("owner_send", protocol.OwnerSendRequest{To: res.SessionID, Body: "FAKE:handback finish now"}), &sent)
	n := w.waitNotice(t, 1)[0]
	want := "committed on " + base + " instead of its own branch " + res.Branch
	if !strings.Contains(n.Body, want) {
		t.Fatalf("the notice does not say %q:\n%s", want, n.Body)
	}
}

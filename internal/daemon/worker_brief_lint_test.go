package daemon

import (
	"context"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/gateway"
	"github.com/niparis/agent-net/internal/protocol"
)

// gitHeadOf returns the commit ID at HEAD of a test repository.
func gitHeadOf(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

// flagChecks returns the check names of the flags, in order.
func flagChecks(flags []protocol.BriefFlag) []string {
	var out []string
	for _, f := range flags {
		out = append(out, f.Check)
	}
	return out
}

// A brief with two problems starts anyway. The flags come back on the start,
// with the commit checked by the machine's own git.
func TestWorkerBriefFlagsReachTheCallerAndDoNotStopTheStart(t *testing.T) {
	g := startGatewayWith(t, t.TempDir(), "", 0, func(opts *gateway.Options) { opts.TestNoBriefChecks = false })
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	req := protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1",
		Problem: "The parser crashes on an empty file.",
		Done:    []string{"Screenshot the parser output at 1280x800 and attach it."},
		Task: "Use the same fixture as in the brief above.\n" +
			"The crash first showed at commit 0123456789abcdef0123456789abcdef01234567.\n" +
			"The fix is at commit " + gitHeadOf(t, repo) + "."}

	want := []string{protocol.BriefCheckCommit, protocol.BriefCheckSandbox}
	started := w.mustStart(t, req)
	if started.Status != "started" || started.SessionID == "" {
		t.Fatalf("a flagged brief did not start: %+v", started)
	}
	if !reflect.DeepEqual(flagChecks(started.BriefFlags), want) {
		t.Fatalf("start flags = %+v, want checks %v", started.BriefFlags, want)
	}
}

// The machine resolves the commit IDs a brief names, so the gateway can flag
// one that is not in the project repository.
func TestWorkerBriefRepoResolvesCommitIDs(t *testing.T) {
	repo := newRepo(t, "shop")
	head := gitHeadOf(t, repo)
	const missing = "0123456789abcdef0123456789abcdef01234567"
	d := &Daemon{}
	got, err := d.workerBriefRepo(context.Background(), protocol.WorkerBriefRepoRequest{Path: repo, Commits: []string{head, missing}})
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]bool{head: true, missing: false}; !reflect.DeepEqual(got.Commits, want) {
		t.Fatalf("commits = %v, want %v", got.Commits, want)
	}
}

// A held start gets no judge and no gateway, so its flags are the text checks
// alone. They reach the caller in the start result and in the note it reads.
func TestOverrideHeldStartReturnsBriefFlags(t *testing.T) {
	_, _, pi, _ := overrideNode(t)
	req := overrideRequest()
	req.Task = "Read /home/nicolas/PLAN.md first."
	res := startFrom(t, pi, req)
	if res.Status != "accepted" || res.SelectionPath != protocol.SelectionHeld {
		t.Fatalf("a held start: %+v", res)
	}
	if !reflect.DeepEqual(flagChecks(res.BriefFlags), []string{protocol.BriefCheckPath}) {
		t.Fatalf("held flags = %+v", res.BriefFlags)
	}
	if !strings.Contains(res.Note, "brief flags") || !strings.Contains(res.Note, "outside") {
		t.Fatalf("held note does not name the flag: %q", res.Note)
	}
}

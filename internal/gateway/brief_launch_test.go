package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// briefStartedOn tracks a start of one brief on a harness, the way a real start
// does, and binds it to a worker session.
func briefStartedOn(t *testing.T, g *Gateway, node, owner, hash, harness string) string {
	t.Helper()
	ref := protocol.NewID("q")
	if err := g.store.trackWorkerStart(ref, node, owner, "alpha", harness, "", nil); err != nil {
		t.Fatal(err)
	}
	if err := g.store.trackBriefLaunch(ref, hash); err != nil {
		t.Fatal(err)
	}
	sid, err := g.store.UpsertSession(node, protocol.SessionInfo{Harness: harness, NativeID: ref, Worker: &protocol.WorkerInfo{ID: "w_" + ref}})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.store.startSession(node, ref, sid); err != nil {
		t.Fatal(err)
	}
	return sid
}

func stopWorker(t *testing.T, g *Gateway, node, sid, harness string, failure string, ev *protocol.WorkerEvidence) {
	t.Helper()
	reason := protocol.StopRunFailed
	raw, _ := json.Marshal(protocol.WorkerStopped{SessionID: sid, Harness: harness, Model: "m", Reason: reason, Failure: failure, Evidence: ev})
	if _, err := g.handleWorker(context.Background(), &nodeConn{id: node}, "worker.stopped", raw); err != nil {
		t.Fatal(err)
	}
}

// A run that ends without trying the task marks its brief for that harness.
func TestWorkerStoppedNotAttemptedMarksBriefHarness(t *testing.T) {
	denial := "Antigravity " + protocol.ToolDenialMarker + ": ReadFile path=/tmp/in.md"
	for _, tc := range []struct {
		name    string
		failure string
		ev      *protocol.WorkerEvidence
		want    bool
	}{
		{"tool denial", denial, nil, true},
		{"early quit with no change", "", &protocol.WorkerEvidence{EarlyQuit: true, RunSeconds: 14}, true},
		{"early quit with a change", "", &protocol.WorkerEvidence{EarlyQuit: true, FilesChanged: 1, LinesChanged: 6}, false},
		{"other failure", "the model returned an error", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g, node, owner, _, _ := setupFloorGateway(t)
			hash := protocol.BriefHash("alpha", protocol.WorkerStartRequest{Problem: "write the plan", Task: "docs"})
			sid := briefStartedOn(t, g, node, owner, hash, "agy")
			stopWorker(t, g, node, sid, "agy", tc.failure, tc.ev)
			got, err := g.store.briefFailedHarnesses(hash)
			if err != nil {
				t.Fatal(err)
			}
			if got["agy"] != tc.want {
				t.Fatalf("failed harnesses %v, want agy=%v", got, tc.want)
			}
		})
	}
}

func TestBriefFailedHarnessesScope(t *testing.T) {
	g, node, owner, _, c := setupFloorGateway(t)
	hash := protocol.BriefHash("alpha", protocol.WorkerStartRequest{Problem: "write the plan"})
	other := protocol.BriefHash("alpha", protocol.WorkerStartRequest{Problem: "something else"})
	sid := briefStartedOn(t, g, node, owner, hash, "agy")
	stopWorker(t, g, node, sid, "agy", "", &protocol.WorkerEvidence{EarlyQuit: true})
	if got, _ := g.store.briefFailedHarnesses(other); len(got) != 0 {
		t.Fatalf("another brief is affected: %v", got)
	}
	if got, _ := g.store.briefFailedHarnesses(hash); !got["agy"] {
		t.Fatalf("same brief not marked: %v", got)
	}
	c.t = c.t.Add(briefLaunchWindow + time.Minute)
	if got, _ := g.store.briefFailedHarnesses(hash); len(got) != 0 {
		t.Fatalf("exclusion outlived the window: %v", got)
	}
}

func TestBriefHashIgnoresCaseSpacingAndCaller(t *testing.T) {
	a := protocol.BriefHash("alpha", protocol.WorkerStartRequest{Problem: "Write  the plan", Task: "docs"})
	b := protocol.BriefHash("alpha", protocol.WorkerStartRequest{Problem: "write the plan\n", Task: "DOCS"})
	if a == "" || a != b {
		t.Fatalf("hashes %q %q", a, b)
	}
	if a == protocol.BriefHash("beta", protocol.WorkerStartRequest{Problem: "write the plan", Task: "docs"}) {
		t.Fatal("another project shares the hash")
	}
}

// A retry of the same brief starts on another harness, whatever the model, and
// the why note names the harness that was left out.
func TestWorkerStartLeavesOutHarnessThatFailedBrief(t *testing.T) {
	g, node, owner, _, _ := setupFloorGateway(t)
	nc := g.conns[node]
	req := protocol.WorkerStartRequest{Project: "alpha", Problem: "write the plan", Task: "docs", Model: "auto"}
	start := func() protocol.WorkerStartAnswer {
		t.Helper()
		ans, err := g.workerStart(context.Background(), nc, protocol.WorkerStartFrame{Owner: owner, Level: "hard", WorkerStartRequest: req}, true)
		if err != nil {
			t.Fatalf("workerStart: %v", err)
		}
		return ans
	}
	first := start()
	if first.Harness == "" || strings.Contains(first.ModelNote, "failed to launch this brief") {
		t.Fatalf("first pick %q: %s", first.Harness, first.ModelNote)
	}
	hash := protocol.BriefHash("alpha", req)
	sid := briefStartedOn(t, g, node, owner, hash, first.Harness)
	stopWorker(t, g, node, sid, first.Harness, "Antigravity "+protocol.ToolDenialMarker+": ReadFile", nil)
	retry := start()
	if retry.Harness == first.Harness {
		t.Fatalf("retry went to %s again: %s", retry.Harness, retry.ModelNote)
	}
	if want := "left out: " + first.Harness + " (failed to launch this brief)"; !strings.Contains(retry.ModelNote, want) {
		t.Fatalf("note %q lacks %q", retry.ModelNote, want)
	}
	// Another brief on the same project is not affected.
	req.Problem = "a different task"
	if other := start(); strings.Contains(other.ModelNote, "failed to launch this brief") {
		t.Fatalf("another brief was affected: %s", other.ModelNote)
	}
}

// When every candidate's harness failed this brief, the start still runs on the
// picker's best choice and says so.
func TestFilterBriefFailedHarnessesNeverBlocks(t *testing.T) {
	cands := []protocol.WorkerModelCandidate{{Harness: "agy", Model: "a"}, {Harness: "agy", Model: "b"}}
	got, note := filterBriefFailedHarnesses(cands, map[string]bool{"agy": true}, nil)
	if len(got) != 2 || !strings.Contains(note, "every harness failed to launch this brief") {
		t.Fatalf("got %d candidates, note %q", len(got), note)
	}
	cands = append(cands, protocol.WorkerModelCandidate{Harness: "claude", Model: "c"})
	got, note = filterBriefFailedHarnesses(cands, map[string]bool{"agy": true}, nil)
	if len(got) != 1 || got[0].Harness != "claude" || note != "left out: agy (failed to launch this brief)" {
		t.Fatalf("got %v, note %q", got, note)
	}
}

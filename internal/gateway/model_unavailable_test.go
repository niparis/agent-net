package gateway

import (
	"reflect"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// unavailableNode is a new machine whose OpenCode lists only its free models,
// as one with no provider login does.
func unavailableNode() (*protocol.NodeView, []protocol.WorkerProfile) {
	n := &protocol.NodeView{NodeID: "n-new", Hostname: "newmac", Harnesses: []protocol.HarnessInfo{{
		Name: "opencode", SignedIn: "yes", Models: []string{"opencode/free-one"},
	}}}
	profiles := []protocol.WorkerProfile{
		{Name: "deepseek alibaba", Harness: "opencode", Model: "alibaba-token-plan/deepseek-v4.1-flash-1017"},
		{Name: "free one", Harness: "opencode", Model: "opencode/free-one"},
	}
	return n, profiles
}

// bothProviders lets both profiles' providers run workers.
var bothProviders = protocol.WorkerProviders{Providers: []string{"opencode", "alibaba-token-plan"}}

func candidateNames(c []protocol.WorkerModelCandidate) []string {
	var out []string
	for _, x := range c {
		out = append(out, x.Name)
	}
	return out
}

func TestCandidatesLeaveOutModelsTheMachineDoesNotList(t *testing.T) {
	s, _ := newTestStore(t)
	g := &Gateway{store: s}
	n, profiles := unavailableNode()
	roster := newNotStartedRoster()
	candidates, skipped, _, err := g.workerModelCandidatesWith(&candidateFacts{}, roster, n, profiles, bothProviders, nil, "", false, protocol.ProtectedProviders{})
	if err != nil {
		t.Fatal(err)
	}
	if got := candidateNames(candidates); !reflect.DeepEqual(got, []string{"free one"}) {
		t.Fatalf("candidates = %v, want only the listed model", got)
	}
	want := "left out, not available on newmac: deepseek alibaba"
	if !reflect.DeepEqual(skipped, []string{want}) {
		t.Fatalf("notes = %q, want %q", skipped, want)
	}
	if !strings.Contains(roster.format(), "deepseek alibaba") || !strings.Contains(roster.format(), "left out, not available on newmac") {
		t.Fatalf("roster = %q", roster.format())
	}
}

func TestCandidatesLeaveOutAModelThatFailedAtLaunchUntilTheNextInventory(t *testing.T) {
	s, _ := newTestStore(t)
	g := &Gateway{store: s}
	n, profiles := unavailableNode()
	n.Harnesses[0].Models = append(n.Harnesses[0].Models, "alibaba-token-plan/deepseek-v4.1-flash-1017")
	s.markModelUnavailable(n.NodeID, "opencode", "alibaba-token-plan/deepseek-v4.1-flash-1017:high", "Model unavailable: alibaba-token-plan/deepseek-v4.1-flash-1017")
	candidates, _, _, err := g.workerModelCandidates(n, profiles, bothProviders, nil, "", false, protocol.ProtectedProviders{})
	if err != nil {
		t.Fatal(err)
	}
	if got := candidateNames(candidates); !reflect.DeepEqual(got, []string{"free one"}) {
		t.Fatalf("candidates after the mark = %v", got)
	}
	s.clearModelsUnavailable(n.NodeID)
	candidates, _, _, err = g.workerModelCandidates(n, profiles, bothProviders, nil, "", false, protocol.ProtectedProviders{})
	if err != nil {
		t.Fatal(err)
	}
	if got := candidateNames(candidates); len(got) != 2 {
		t.Fatalf("candidates after a new inventory = %v, want both", got)
	}
}

func TestModelUnavailableReportMarksTheModelAndTracksANewStart(t *testing.T) {
	s, _ := newTestStore(t)
	g := &Gateway{store: s}
	if err := s.trackWorkerStart("q-old", "n-new", "s-owner", "demo", "opencode", protocol.WorkersReadOnly, []string{"tests pass"}); err != nil {
		t.Fatal(err)
	}
	nc := &nodeConn{id: "n-new"}
	if _, err := g.workerModelUnavailable(nc, protocol.WorkerModelUnavailable{Ref: "q-old", Owner: "s-other", Harness: "opencode", Model: "a/b"}); err == nil {
		t.Fatal("a report for another owner's start must be refused")
	}
	answer, err := g.workerModelUnavailable(nc, protocol.WorkerModelUnavailable{Ref: "q-old", Owner: "s-owner", Project: "demo",
		Harness: "opencode", Model: "alibaba-token-plan/deepseek-v4.1-flash-1017", Failure: "Model unavailable: alibaba-token-plan/deepseek-v4.1-flash-1017"})
	if err != nil {
		t.Fatal(err)
	}
	if answer.Ref == "" || answer.Ref == "q-old" || !protocol.IsQueueID(answer.Ref) {
		t.Fatalf("new start = %q", answer.Ref)
	}
	if _, ok := s.modelUnavailable("n-new", "opencode", "alibaba-token-plan/deepseek-v4.1-flash-1017"); !ok {
		t.Fatal("the model is not marked unavailable on the machine")
	}
	project, perms, done, err := s.startFacts(answer.Ref)
	if err != nil || project != "demo" || perms != protocol.WorkersReadOnly || !reflect.DeepEqual(done, []string{"tests pass"}) {
		t.Fatalf("new start = %q %q %v %v", project, perms, done, err)
	}
	if err := s.verifyRecheckStart("n-new", answer.Ref, "s-owner"); err != nil {
		t.Fatalf("the queue's recheck of the new start: %v", err)
	}
}

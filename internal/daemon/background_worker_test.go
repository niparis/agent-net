package daemon

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestBackgroundWorkerProtectedAndReadOnly(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startCodexNodeWithModels(t, g, shortSet, []string{"gpt-fake"})
	repo := newRepo(t, "shop")
	w.register(t, repo)
	score := 23.3
	w.cli.mustCall(protocol.OpScoresSet, protocol.ScoreSetRequest{Model: "gpt-fake", Score: &score})
	w.cli.mustCall(protocol.OpWorkerProfileAdd, protocol.WorkerProfileRequest{Harness: "codex", Model: "gpt-fake"})
	w.cli.mustCall(protocol.OpProtectedProvidersSet, protocol.ProtectedProviders{Providers: []string{"openai"}, Reason: "reserved"})
	frame := protocol.WorkerStartFrame{Owner: w.ownerID(), RequireReadOnly: true, WorkerStartRequest: protocol.WorkerStartRequest{Project: "shop", Isolation: protocol.IsolationNone, Task: "phrase these words"}, Level: "trivial"}
	if _, err := w.d.call(context.Background(), "worker.start", frame); err == nil {
		t.Fatal("protected provider was used")
	}
	if w.d.workerCount() != 0 {
		t.Fatal("protected worker started")
	}
	w.cli.mustCall(protocol.OpProtectedProvidersSet, protocol.ProtectedProviders{Providers: []string{}, Reason: "test"})
	raw, err := w.d.call(context.Background(), "worker.start", frame)
	if err != nil {
		t.Fatal(err)
	}
	var out protocol.WorkerStartAnswer
	if err = json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.Model != "gpt-fake" || out.Permissions != protocol.WorkersReadOnly || out.SessionID == "" {
		t.Fatalf("selection: %+v", out)
	}
}

func TestBackgroundWorkerRespectsNoNetworkPolicy(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startCodexNodeWithModels(t, g, shortSet, []string{"gpt-fake"})
	repo := newRepo(t, "shop")
	w.register(t, repo)
	setWorkersLevel(t, w, repo, protocol.WorkersNoNetwork)
	score := 23.3
	w.cli.mustCall(protocol.OpScoresSet, protocol.ScoreSetRequest{Model: "gpt-fake", Score: &score})
	w.cli.mustCall(protocol.OpWorkerProfileAdd, protocol.WorkerProfileRequest{Harness: "codex", Model: "gpt-fake"})
	frame := protocol.WorkerStartFrame{Owner: w.ownerID(), RequireReadOnly: true, WorkerStartRequest: protocol.WorkerStartRequest{Project: "shop", Isolation: protocol.IsolationNone, Task: "phrase these words"}, Level: "trivial"}
	if _, err := w.d.call(context.Background(), "worker.start", frame); err == nil {
		t.Fatal("background worker widened a no-network policy")
	}
	if w.d.workerCount() != 0 {
		t.Fatal("worker started")
	}
}

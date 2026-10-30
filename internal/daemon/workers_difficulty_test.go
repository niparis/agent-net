package daemon

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestWorkerAutomaticDifficulty(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	score := 51.0
	w.cli.mustCall(protocol.OpScoresSet, protocol.ScoreSetRequest{Model: "fake/m1", Score: &score})
	w.cli.mustCall(protocol.OpWorkerProvidersSet, protocol.WorkerProviders{Providers: []string{"fake"}})
	res, err := w.start(protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "auto", Difficulty: "hard", Task: "x"})
	if err != nil || res.Model != "fake/m1" || !strings.Contains(res.ModelNote, "hard ->") {
		t.Fatalf("choice: %+v, %v", res, err)
	}
}

func TestWorkerUnscoredFallback(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	res, err := w.start(protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "x"})
	if err != nil || !strings.Contains(res.ModelNote, "no scored models") {
		t.Fatalf("fallback: %+v %v", res, err)
	}
	_, err = w.start(protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Difficulty: "hard", Task: "x"})
	if err == nil || !strings.Contains(err.Error(), "no scored models") {
		t.Fatalf("difficulty: %v", err)
	}
}

func TestWorkerAutomaticChoosesHarness(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	score := 51.0
	w.cli.mustCall(protocol.OpScoresSet, protocol.ScoreSetRequest{Model: "fake/m1", Score: &score})
	w.cli.mustCall(protocol.OpWorkerProvidersSet, protocol.WorkerProviders{Providers: []string{"fake"}})
	res, err := w.start(protocol.WorkerStartRequest{Project: "shop", Model: "auto", Difficulty: "hard", Task: "x"})
	if err != nil || res.Model != "fake/m1" || res.Harness != "opencode" {
		t.Fatalf("choice: %+v, %v", res, err)
	}
}

func TestWorkerAutomaticRejectsUnapprovedProvider(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	w.register(t, newRepo(t, "shop"))
	score := 51.0
	w.cli.mustCall(protocol.OpScoresSet, protocol.ScoreSetRequest{Model: "fake/m1", Score: &score})
	_, err := w.start(protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "x"})
	if err == nil || !strings.Contains(err.Error(), "worker provider") {
		t.Fatalf("excluded provider: %v", err)
	}
	res, err := w.start(protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1", Task: "x"})
	if err != nil || res.Model != "fake/m1" {
		t.Fatalf("explicit model: %+v %v", res, err)
	}
}

func TestWorkerAutomaticPrefersRecentSuccess(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	w.register(t, newRepo(t, "shop"))
	score := 51.0
	for _, m := range []string{"fake/m1", "fake/m2"} {
		w.cli.mustCall(protocol.OpScoresSet, protocol.ScoreSetRequest{Model: m, Score: &score})
	}
	w.cli.mustCall(protocol.OpWorkerProvidersSet, protocol.WorkerProviders{Providers: []string{"fake"}})
	if err := g.gw.Store().RecordModelSuccess(w.node.d.NodeID(), "opencode", "fake/m2"); err != nil {
		t.Fatal(err)
	}
	res, err := w.start(protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "auto", Difficulty: "hard", Task: "x"})
	if err != nil || res.Model != "fake/m2" {
		t.Fatalf("choice: %+v %v", res, err)
	}
}

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

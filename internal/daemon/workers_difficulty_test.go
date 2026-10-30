package daemon

import (
	"encoding/json"
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
	w.cli.mustCall(protocol.OpWorkerProfileAdd, protocol.WorkerProfileRequest{Harness: "opencode", Model: "fake/m1"})
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
	if err != nil || !strings.Contains(res.ModelNote, "no registered workers") {
		t.Fatalf("fallback: %+v %v", res, err)
	}
	res, err = w.start(protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "auto", Difficulty: "hard", Task: "x"})
	if err != nil || !strings.Contains(res.ModelNote, "no registered workers") {
		t.Fatalf("difficulty: %+v %v", res, err)
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
	w.cli.mustCall(protocol.OpWorkerProfileAdd, protocol.WorkerProfileRequest{Harness: "opencode", Model: "fake/m1"})
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
	w.cli.mustCall(protocol.OpWorkerProfileAdd, protocol.WorkerProfileRequest{Harness: "opencode", Model: "fake/m1"})
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
	for _, m := range []string{"fake/m1", "fake/m2"} {
		w.cli.mustCall(protocol.OpWorkerProfileAdd, protocol.WorkerProfileRequest{Harness: "opencode", Model: m})
	}
	if err := g.gw.Store().RecordModelSuccess(w.node.d.NodeID(), "opencode", "fake/m2"); err != nil {
		t.Fatal(err)
	}
	res, err := w.start(protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "auto", Difficulty: "hard", Task: "x"})
	if err != nil || res.Model != "fake/m2" {
		t.Fatalf("choice: %+v %v", res, err)
	}
}

func TestWorkerProfilesAgentManagement(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	pi, id := helloPi(t, w.node, "profiles-pi", repo, true)
	raw, err := pi.call(protocol.OpWorkerProfileAdd, protocol.WorkerProfileRequest{Harness: "opencode", Model: "m1", Effort: "high"})
	if err != nil {
		t.Fatal(err)
	}
	var p protocol.WorkerProfile
	if err = json.Unmarshal(raw, &p); err != nil || p.Model != "fake/m1" || p.AddedBy != id || p.Effort != "high" {
		t.Fatalf("profile: %s %v", raw, err)
	}
	raw = pi.mustCall(protocol.OpWorkerProfiles, nil)
	var profiles []protocol.WorkerProfile
	if err = json.Unmarshal(raw, &profiles); err != nil || len(profiles) != 1 {
		t.Fatalf("list %s %v", raw, err)
	}
	pi.mustCall(protocol.OpWorkerProfileRemove, protocol.WorkerProfileRequest{Name: p.Name})
	if _, err = pi.call(protocol.OpWorkerProfileAdd, protocol.WorkerProfileRequest{Harness: "opencode", Model: "missing-model"}); err == nil {
		t.Fatal("unknown model accepted")
	}
}

func TestWorkerAutomaticUsesOnlyRegisteredProfiles(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	w.register(t, newRepo(t, "shop"))
	score := 51.0
	for _, m := range []string{"fake/m1", "fake/m2"} {
		w.cli.mustCall(protocol.OpScoresSet, protocol.ScoreSetRequest{Model: m, Score: &score})
	}
	w.cli.mustCall(protocol.OpWorkerProvidersSet, protocol.WorkerProviders{Providers: []string{"fake"}})
	w.cli.mustCall(protocol.OpWorkerProfileAdd, protocol.WorkerProfileRequest{Harness: "opencode", Model: "fake/m2"})
	res, err := w.start(protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "auto", Difficulty: "hard", Task: "x"})
	if err != nil || res.Model != "fake/m2" {
		t.Fatalf("unregistered model selected: %+v %v", res, err)
	}
}

func TestWorkerRegisteredOpenCodeEffort(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	score := 51.0
	w.cli.mustCall(protocol.OpScoresSet, protocol.ScoreSetRequest{Model: "fake/m1", Score: &score})
	w.cli.mustCall(protocol.OpWorkerProvidersSet, protocol.WorkerProviders{Providers: []string{"fake"}})
	w.cli.mustCall(protocol.OpWorkerProfileAdd, protocol.WorkerProfileRequest{Harness: "opencode", Model: "fake/m1", Effort: "high"})
	res, err := w.start(protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "auto", Difficulty: "hard", Task: "x"})
	if err != nil {
		t.Fatal(err)
	}
	rec := readFake(t, repo)
	model, _ := rec.SessionBody["model"].(map[string]any)
	if res.Model != "fake/m1:high" || model["id"] != "m1" || model["variant"] != "high" {
		t.Fatalf("result %+v model %v", res, model)
	}
}

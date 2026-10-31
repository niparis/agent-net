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

func TestWorkerAutomaticSkipsStaleProfileWithReason(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	w.register(t, newRepo(t, "shop"))
	if err := g.gw.Store().AddWorkerProfile(protocol.WorkerProfile{Name: "terra pi", Harness: "opencode", Model: "fake/deleted", AddedBy: w.ownerID()}); err != nil {
		t.Fatal(err)
	}
	score := 51.0
	w.cli.mustCall(protocol.OpScoresSet, protocol.ScoreSetRequest{Model: "fake/m1", Score: &score})
	w.cli.mustCall(protocol.OpWorkerProvidersSet, protocol.WorkerProviders{Providers: []string{"fake"}})
	w.cli.mustCall(protocol.OpWorkerProfileAdd, protocol.WorkerProfileRequest{Harness: "opencode", Model: "fake/m1"})

	res, err := w.start(protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "auto", Task: "x"})
	if err != nil || res.Model != "fake/m1" {
		t.Fatalf("choice: %+v, %v", res, err)
	}
	for _, want := range []string{"stale profile", "terra pi", "fake/deleted", "not listed"} {
		if !strings.Contains(res.ModelNote, want) {
			t.Errorf("model note %q does not explain stale profile %q", res.ModelNote, want)
		}
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
	replacedRaw, err := pi.call(protocol.OpWorkerProfileAdd, protocol.WorkerProfileRequest{Name: p.Name, Harness: "opencode", Model: "m2", Effort: "low"})
	if err != nil {
		t.Fatal(err)
	}
	var replaced protocol.WorkerProfile
	if err = json.Unmarshal(replacedRaw, &replaced); err != nil || replaced.Name != p.Name || replaced.Model != "fake/m2" || replaced.Effort != "low" {
		t.Fatalf("replacement: %s %v", replacedRaw, err)
	}
	raw = pi.mustCall(protocol.OpWorkerProfiles, nil)
	var profiles []protocol.WorkerProfile
	if err = json.Unmarshal(raw, &profiles); err != nil || len(profiles) != 1 || profiles[0].Name != p.Name || profiles[0].Model != "fake/m2" {
		t.Fatalf("list %s %v", raw, err)
	}
	if events, _ := g.gw.Store().Events("worker_profile.add"); len(events) != 2 {
		t.Errorf("replacement should be audited as an add: %v", events)
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

func TestWorkerProfilesResolveUsingTrackRecord(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	w.register(t, newRepo(t, "shop"))
	if err := g.gw.Store().RecordModelSuccess(w.node.d.NodeID(), "opencode", "fake/m2"); err != nil {
		t.Fatal(err)
	}
	raw, err := w.cli.call(protocol.OpWorkerProfileAdd, protocol.WorkerProfileRequest{Harness: "opencode", Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	var profile protocol.WorkerProfile
	if json.Unmarshal(raw, &profile) != nil || profile.Model != "fake/m2" {
		t.Fatalf("profile: %s", raw)
	}
}

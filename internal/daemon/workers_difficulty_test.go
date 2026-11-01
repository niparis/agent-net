package daemon

import (
	"encoding/json"
	"os"
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

func TestWorkerAutomaticProfileResolvesProviderPerMachine(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	startPiWithModels := func(host string, models []string) *workerNode {
		dir := t.TempDir()
		t.Setenv(envFakePi, "1")
		t.Setenv("PI_CODING_AGENT_SESSION_DIR", dir)
		return startWorkerNodeWithDetect(t, g, shortSet, inventoryWithModels("pi", models), func(c *Config) {
			c.Hostname = host
			c.HarnessPaths = map[string]string{"pi": os.Args[0]}
			c.PiSessionsDir = dir
		})
	}

	mac := startPiWithModels("mac", []string{"openai-codex/gpt-6.1-terra"})
	research := startPiWithModels("research", []string{"openai/gpt-6.1-terra"})
	mac.register(t, newRepo(t, "shop"))
	repo := newRepo(t, "shop")
	research.register(t, repo)
	if err := g.gw.Store().AddWorkerProfile(protocol.WorkerProfile{
		Name: "terra pi", Harness: "pi", Model: "openai-codex/gpt-6.1-terra", AddedBy: research.ownerID(),
	}); err != nil {
		t.Fatal(err)
	}
	score := 42.3
	research.cli.mustCall(protocol.OpScoresSet, protocol.ScoreSetRequest{Model: "openai/gpt-6.1-terra", Score: &score})
	research.cli.mustCall(protocol.OpWorkerProvidersSet, protocol.WorkerProviders{Providers: []string{"codex"}})

	res, err := research.start(protocol.WorkerStartRequest{Project: "shop", Machine: "research", Harness: "pi", Model: "auto", Task: "x", Isolation: protocol.IsolationWorktree})
	if err != nil {
		t.Fatalf("automatic profile start: %v", err)
	}
	if res.Model != "openai/gpt-6.1-terra" {
		t.Fatalf("started model %q, want machine's id openai/gpt-6.1-terra", res.Model)
	}
	if !strings.Contains(res.ModelNote, "terra pi: openai-codex/gpt-6.1-terra resolved to openai/gpt-6.1-terra on research") {
		t.Fatalf("profile resolution missing from start result: %q", res.ModelNote)
	}
	explicit, err := research.start(protocol.WorkerStartRequest{
		Project: "shop", Machine: "research", Harness: "pi", Model: "openai-codex/gpt-6.1-terra", Task: "x", Isolation: protocol.IsolationWorktree,
	})
	if err != nil || explicit.Model != "openai/gpt-6.1-terra" || !strings.Contains(explicit.ModelNote, "terra pi: openai-codex/gpt-6.1-terra resolved to openai/gpt-6.1-terra on research") {
		t.Fatalf("explicit profile model: %+v, %v", explicit, err)
	}
}

func TestWorkerAutomaticSkipsStaleProfileWithReason(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	w.register(t, newRepo(t, "shop"))
	if err := g.gw.Store().AddWorkerProfile(protocol.WorkerProfile{Name: "terra pi", Harness: "opencode", Model: "fake/deleted", AddedBy: w.ownerID()}); err != nil {
		t.Fatal(err)
	}
	score := 42.3
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
	w.register(t, newRepo(t, "shop"))
	for _, level := range []string{"normal", "hard"} {
		_, err := w.start(protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Difficulty: level, Task: "x"})
		if err == nil || !strings.Contains(err.Error(), "no registered workers") || !strings.Contains(err.Error(), "model explicitly") {
			t.Fatalf("empty roster at %s: %v", level, err)
		}
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

func TestWorkerExplicitProtectedProviderNote(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	w.register(t, newRepo(t, "shop"))
	w.cli.mustCall(protocol.OpProtectedProvidersSet, protocol.ProtectedProviders{Providers: []string{"fake"}, Reason: "orchestrator"})
	res, err := w.start(protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1", Task: "x"})
	if err != nil || !strings.Contains(res.ModelNote, "protected provider (fake)") {
		t.Fatalf("explicit: %+v %v", res, err)
	}
}

func TestWorkerTouchesDifficulty(t *testing.T) {
	for _, tc := range []struct {
		name            string
		touches         []string
		wantModel, note string
	}{
		{"db", []string{"db"}, "fake/m2", "touches db: chose as hard"},
		{"ui", []string{"ui"}, "fake/m1", ""},
		{"other", []string{"other"}, "fake/m1", ""},
		{"docs", []string{"docs"}, "fake/m1", "touches docs/tests only: chose as simple"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := startGateway(t, t.TempDir(), "")
			w := startWorkerNode(t, g, shortSet, nil)
			w.register(t, newRepo(t, "shop"))
			low, high := 32.5, 42.3
			w.cli.mustCall(protocol.OpScoresSet, protocol.ScoreSetRequest{Model: "fake/m1", Score: &low})
			w.cli.mustCall(protocol.OpScoresSet, protocol.ScoreSetRequest{Model: "fake/m2", Score: &high})
			w.cli.mustCall(protocol.OpWorkerProvidersSet, protocol.WorkerProviders{Providers: []string{"fake"}})
			for _, m := range []string{"fake/m1", "fake/m2"} {
				w.cli.mustCall(protocol.OpWorkerProfileAdd, protocol.WorkerProfileRequest{Harness: "opencode", Model: m})
			}
			res, err := w.start(protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Difficulty: "normal", Touches: tc.touches, Task: "x"})
			if err != nil || res.Model != tc.wantModel || (tc.note != "" && !strings.Contains(res.ModelNote, tc.note)) || (tc.note == "" && strings.Contains(res.ModelNote, "touches")) {
				t.Fatalf("choice: %+v, %v", res, err)
			}
			row := reworkRow(t, w, res.SessionID)
			used := "normal"
			if tc.name == "db" {
				used = "hard"
			} else if tc.name == "docs" {
				used = "simple"
			}
			if row.DifficultyAsked != "normal" || row.DifficultyUsed != used || row.Touches != strings.Join(tc.touches, ",") {
				t.Fatalf("worker record: %+v", row)
			}

		})
	}
}

func TestWorkerStartMaximumLevelRule(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	w.register(t, newRepo(t, "shop"))
	w.cli.mustCall(protocol.OpWorkerProvidersSet, protocol.WorkerProviders{Providers: []string{"fake"}})
	score := 51.0
	w.cli.mustCall(protocol.OpScoresSet, protocol.ScoreSetRequest{Model: "fake/m1", Score: &score})
	w.cli.mustCall(protocol.OpWorkerProfileAdd, protocol.WorkerProfileRequest{Name: "strong", Harness: "opencode", Model: "fake/m1"})
	_, err := w.start(protocol.WorkerStartRequest{Project: "shop", Difficulty: "normal", Task: "x"})
	if err == nil || !strings.Contains(err.Error(), "very_complex") || !strings.Contains(err.Error(), "model explicitly") {
		t.Fatalf("missing actionable eligibility error: %v", err)
	}
	res, err := w.start(protocol.WorkerStartRequest{Project: "shop", Model: "fake/m1", Difficulty: "normal", Task: "x"})
	if err != nil || res.Model != "fake/m1" || !strings.Contains(res.ModelNote, "explicit model overrides") {
		t.Fatalf("explicit override: %+v %v", res, err)
	}
	score = 32.5
	w.cli.mustCall(protocol.OpScoresSet, protocol.ScoreSetRequest{Model: "fake/m2", Score: &score})
	w.cli.mustCall(protocol.OpWorkerProfileAdd, protocol.WorkerProfileRequest{Name: "adequate", Harness: "opencode", Model: "fake/m2"})
	res, err = w.start(protocol.WorkerStartRequest{Project: "shop", Difficulty: "normal", Task: "x"})
	if err != nil || res.Model != "fake/m2" || !strings.Contains(res.ModelNote, "strong excluded: its max is very_complex") || !strings.Contains(res.ModelNote, "one level") {
		t.Fatalf("automatic exclusion: %+v %v", res, err)
	}
}

package daemon

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// withDifficulty turns the difficulty judge on for a test daemon. Test daemons
// skip it by default, because their fake harnesses cannot answer it.
func withDifficulty(next func(*Config)) func(*Config) {
	return func(c *Config) {
		c.testNoDifficulty = false
		if c.HarnessPaths == nil {
			c.HarnessPaths = make(map[string]string)
		}
		c.HarnessPaths["codex"] = os.Args[0]
		if next != nil {
			next(c)
		}
	}
}

const normalJudgeReply = `{"reasoning":3,"scope":2,"coupling":2,"uncertainty":2,"verification":2,"risk":3,` +
	`"confidence":"high","reasons":{"reasoning":"logic","scope":"local","coupling":"isolated",` +
	`"uncertainty":"known","verification":"test","risk":"safe"}}`

func setJudgeScores(t *testing.T, w *workerNode, scores map[string]float64) {
	t.Helper()
	w.cli.mustCall(protocol.OpWorkerProvidersSet, protocol.WorkerProviders{Providers: []string{"fake"}})
	for model, score := range scores {
		s := score
		w.cli.mustCall(protocol.OpScoresSet, protocol.ScoreSetRequest{Model: model, Score: &s})
		w.cli.mustCall(protocol.OpWorkerProfileAdd, protocol.WorkerProfileRequest{Harness: "opencode", Model: model})
	}
}

func storedAssessment(t *testing.T, g *testGateway, session string) *protocol.WorkerAssessment {
	t.Helper()
	var got *protocol.WorkerAssessment
	eventually(t, "the assessment is stored", func() bool {
		a, ok, err := g.gw.Store().Agent(session)
		if err != nil || !ok || a.Worker == nil || a.Worker.Assessment == nil {
			return false
		}
		got = a.Worker.Assessment
		return true
	})
	return got
}

func TestWorkerJudgeLabelDecidesLevel(t *testing.T) {
	setupShadowJudge(t, shadowReply, false)
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, withDifficulty(nil))
	w.register(t, newRepo(t, "shop"))
	setJudgeScores(t, w, map[string]float64{"fake/m1": 12, "fake/m2": 51})

	res, err := w.start(protocol.WorkerStartRequest{Project: "shop", Model: "auto", Difficulty: "very_complex", Touches: []string{"db"}, Task: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Model != "fake/m1" {
		t.Fatalf("chose %q at the judge's trivial label, want the trivial profile fake/m1", res.Model)
	}
	if !strings.HasPrefix(res.ModelNote, "difficulty R2 S1 C1 U1 V1 K1 · D 1.20 · trivial -> ") {
		t.Fatalf("start note does not begin with the assessment: %q", res.ModelNote)
	}
	if strings.Contains(res.ModelNote, "touches") {
		t.Fatalf("touches changed the note: %q", res.ModelNote)
	}
	row := reworkRow(t, w, res.SessionID)
	if row.DifficultyAsked != "trivial" || row.DifficultyUsed != "trivial" {
		t.Fatalf("worker record kept the caller's level: %+v", row)
	}
	a := storedAssessment(t, g, res.SessionID)
	if a.Label != "trivial" || a.PickerLabel != "trivial" || a.Error != "" {
		t.Fatalf("assessment: %+v", a)
	}
}

func TestWorkerJudgeFailureStartsNormal(t *testing.T) {
	setupShadowJudge(t, "not json", false)
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, withDifficulty(nil))
	w.register(t, newRepo(t, "shop"))
	setJudgeScores(t, w, map[string]float64{"fake/m1": 32.5, "fake/m2": 51})

	res, err := w.start(protocol.WorkerStartRequest{Project: "shop", Model: "auto", Difficulty: "very_complex", Touches: []string{"db"}, Task: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Model != "fake/m1" {
		t.Fatalf("fallback chose %q, want the normal profile fake/m1", res.Model)
	}
	if !strings.HasPrefix(res.ModelNote, "difficulty: no assessment (") || !strings.Contains(res.ModelNote, "started at normal") {
		t.Fatalf("fallback note: %q", res.ModelNote)
	}
	row := reworkRow(t, w, res.SessionID)
	if row.DifficultyAsked != "normal" || row.DifficultyUsed != "normal" {
		t.Fatalf("worker record: %+v", row)
	}
	a := storedAssessment(t, g, res.SessionID)
	if a.Label != "normal" || a.PickerLabel != "normal" || !strings.Contains(a.Error, "parse") {
		t.Fatalf("assessment: %+v", a)
	}
}

func TestWorkerExplicitModelRecordsAssessment(t *testing.T) {
	setupShadowJudge(t, shadowReply, false)
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, withDifficulty(nil))
	w.register(t, newRepo(t, "shop"))
	setJudgeScores(t, w, map[string]float64{"fake/m1": 51})

	res, err := w.start(protocol.WorkerStartRequest{Project: "shop", Model: "fake/m1", Difficulty: "very_complex", Touches: []string{"db"}, Task: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Model != "fake/m1" {
		t.Fatalf("explicit model changed to %q", res.Model)
	}
	if !strings.HasPrefix(res.ModelNote, "difficulty R2 S1 C1 U1 V1 K1 · D 1.20 · trivial") {
		t.Fatalf("explicit start note: %q", res.ModelNote)
	}
	a := storedAssessment(t, g, res.SessionID)
	if a.Label != "trivial" || a.PickerLabel != "trivial" {
		t.Fatalf("explicit start assessment: %+v", a)
	}
	row := reworkRow(t, w, res.SessionID)
	if row.DifficultyUsed != "trivial" {
		t.Fatalf("explicit start record: %+v", row)
	}
}

func TestWorkerQueuedStartKeepsJudgeLabel(t *testing.T) {
	marker, _ := setupShadowJudge(t, shadowReply, false)
	g := startGateway(t, t.TempDir(), "")
	fl := &fakeLoad{l: cpuBusy()}
	w := startWorkerNode(t, g, queueSet, withDifficulty(withLoad(fl)))
	w.register(t, newRepo(t, "shop"))
	setJudgeScores(t, w, map[string]float64{"fake/m1": 12})
	pi, _ := helloPi(t, w.node, "caller", "/work/caller", true)

	res := startFrom(t, pi, protocol.WorkerStartRequest{Model: "auto", Project: "shop", Harness: "opencode", Difficulty: "very_complex", Touches: []string{"db"}, Task: "queued"})
	if res.Status != "queued" {
		t.Fatalf("not queued: %+v", res)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("the judge did not run before the start was queued")
	}
	if !strings.HasPrefix(res.ModelNote, "difficulty R2 S1 C1 U1 V1 K1 · D 1.20 · trivial -> ") {
		t.Fatalf("queued note: %q", res.ModelNote)
	}
	entries, err := w.d.st.queue()
	if err != nil || len(entries) != 1 || entries[0].Assessment == nil || entries[0].Assessment.Label != "trivial" {
		t.Fatalf("the queue did not keep the judge label: %+v %v", entries, err)
	}
	fl.set(roomyLoad())
	eventually(t, "the queued worker keeps the judge label", func() bool {
		for _, a := range w.agents(t, true) {
			if a.Worker != nil && a.Worker.DifficultyUsed == "trivial" && a.Worker.Assessment != nil && a.Worker.Assessment.Label == "trivial" {
				return true
			}
		}
		return false
	})
}

func TestWorkerTouchesDoNotChangeLevel(t *testing.T) {
	for _, touches := range [][]string{{"db"}, {"hot-path"}, {"docs", "tests"}} {
		t.Run(strings.Join(touches, "+"), func(t *testing.T) {
			setupShadowJudge(t, normalJudgeReply, false)
			g := startGateway(t, t.TempDir(), "")
			w := startWorkerNode(t, g, shortSet, withDifficulty(nil))
			w.register(t, newRepo(t, "shop"))
			setJudgeScores(t, w, map[string]float64{"fake/m1": 32.5, "fake/m2": 51})

			res, err := w.start(protocol.WorkerStartRequest{Project: "shop", Model: "auto", Difficulty: "very_complex", Touches: touches, Task: "x"})
			if err != nil {
				t.Fatal(err)
			}
			if res.Model != "fake/m1" || !strings.Contains(res.ModelNote, "normal -> ") || strings.Contains(res.ModelNote, "touches") {
				t.Fatalf("touches %v changed the level: %+v", touches, res)
			}
			if row := reworkRow(t, w, res.SessionID); row.DifficultyUsed != "normal" {
				t.Fatalf("touches %v changed the record: %+v", touches, row)
			}
		})
	}
}

func TestWorkerContinueKeepsJudgeLabelUnlessBriefChanges(t *testing.T) {
	setupShadowJudge(t, difficultyScoresJSON, false)
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, withDifficulty(nil))
	w.register(t, newRepo(t, "shop"))

	first := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1", Task: "FAKE:handback first"})
	if a := storedAssessment(t, g, first.SessionID); a.Label != "hard" {
		t.Fatalf("first label: %+v", a)
	}
	eventually(t, "the first worker ended and was reported", func() bool {
		rows, _ := w.d.st.workers()
		return w.d.workerCount() == 0 && len(rows) == 1 && rows[0].Reported && rows[0].Resumable
	})
	// A changed judge reply would give trivial. A continue without extra work
	// must keep the old hard label; one with extra work must assess again.
	reply := os.Getenv(envFakeOpenCodeReply)
	if err := os.WriteFile(reply, []byte(shadowReply), 0600); err != nil {
		t.Fatal(err)
	}
	startContinue := func(extra string) protocol.WorkerStartResult {
		raw, err := w.cli.call("worker_start", protocol.WorkerStartRequest{Continue: first.SessionID, Task: extra})
		if err != nil {
			t.Fatal(err)
		}
		var res protocol.WorkerStartResult
		if err := json.Unmarshal(raw, &res); err != nil || res.SessionID == "" {
			t.Fatalf("continue: %s %v", raw, err)
		}
		return res
	}
	kept := startContinue("")
	if a := storedAssessment(t, g, kept.SessionID); a.Label != "hard" {
		t.Fatalf("continue without new work reassessed the brief: %+v", a)
	}
	changed := startContinue("also update the docs")
	if a := storedAssessment(t, g, changed.SessionID); a.Label != "trivial" {
		t.Fatalf("continue with new work kept the old label: %+v", a)
	}
}

func TestWorkerStartWithoutTouches(t *testing.T) {
	setupShadowJudge(t, normalJudgeReply, false)
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, withDifficulty(nil))
	w.register(t, newRepo(t, "shop"))
	setJudgeScores(t, w, map[string]float64{"fake/m1": 32.5})
	pi, _ := helloPi(t, w.node, "caller", "/work/caller", true)

	res := startFrom(t, pi, protocol.WorkerStartRequest{Model: "auto", Project: "shop", Harness: "opencode", Task: "no areas"})
	if res.Status == "queued" || res.SessionID == "" {
		t.Fatalf("start without touches: %+v", res)
	}
}

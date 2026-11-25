package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coder/websocket"
	"github.com/niparis/agent-net/internal/protocol"
)

// A continue of an ended worker meets the picker's rules of today at the level
// stored with the worker (decision 0189). These tests continue one ended
// worker against a fake machine that answers worker.continue as a daemon does,
// and read what the machine was asked to run.

const (
	opusModel     = "claude-opus-5-5:high"
	terraModel    = "gpt-6.1-terra:high"
	deepseekModel = "deepseek/deepseek-v4.1-flash:high"
)

// continueFixture is the floor gateway with a fake machine. Each
// worker.continue the machine gets is sent on frames and answered with a new
// session that runs what it was asked for. A worker.launch makes the worker's
// session, as the machine's report does, and is answered with it.
type continueFixture struct {
	g      *Gateway
	nc     *nodeConn
	owner  string
	agent  string
	clock  *clock
	frames chan protocol.WorkerContinue
}

func continueGateway(t *testing.T) continueFixture {
	t.Helper()
	g, node, owner, agent, c := setupFloorGateway(t)
	g.opts.TestNoBriefChecks = true
	setOwnerScores(t, g.store, nil)
	accepted := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ws, err := websocket.Accept(w, r, nil); err == nil {
			accepted <- ws
		}
	}))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	client, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	nc := &nodeConn{id: node, ws: <-accepted, dead: make(chan struct{}), cancel: cancel}
	setTestConn(t, g, node, nc)
	frames := make(chan protocol.WorkerContinue, 4)
	go func() {
		for {
			_, raw, e := client.Read(ctx)
			if e != nil {
				return
			}
			var h protocol.Header
			if json.Unmarshal(raw, &h) != nil {
				continue
			}
			if h.Type == "worker.launch" {
				var l protocol.WorkerLaunch
				_ = json.Unmarshal(raw, &l)
				id, err := g.store.UpsertSession(node, protocol.SessionInfo{Harness: l.Harness, NativeID: "ses_" + l.StartRef, Model: l.Model,
					Worker: &protocol.WorkerInfo{ID: "w_" + l.StartRef, Owner: l.Owner, Project: l.Project, RunGeneration: 1}})
				b, _ := protocol.EncodeResult(h.ID, protocol.WorkerLaunched{SessionID: id}, err)
				var reply protocol.Header
				_ = json.Unmarshal(b, &reply)
				nc.answer(reply)
				continue
			}
			if h.Type != "worker.continue" {
				continue
			}
			var f protocol.WorkerContinue
			_ = json.Unmarshal(raw, &f)
			frames <- f
			a := protocol.WorkerAssessment{StartedAt: 2, AssessedAt: 2}
			if f.Assessment != nil {
				a = *f.Assessment
			}
			a.RunID = "k_continue"
			b, _ := protocol.EncodeResult(h.ID, protocol.WorkerContinued{WorkerLaunched: protocol.WorkerLaunched{SessionID: "s_continued"},
				Project: "alpha", Harness: f.Harness, Model: f.Model, Assessment: &a}, nil)
			var reply protocol.Header
			_ = json.Unmarshal(b, &reply)
			nc.answer(reply)
		}
	}()
	return continueFixture{g: g, nc: nc, owner: owner, agent: agent, clock: c, frames: frames}
}

// endedWorker is an ended worker of the owner that ran harness and model, and
// was judged at level.
func (cf continueFixture) endedWorker(t *testing.T, harness, model, level string) string {
	t.Helper()
	g, nc, owner := cf.g, cf.nc, cf.owner
	worker, err := g.store.UpsertSession(nc.id, protocol.SessionInfo{Harness: harness, NativeID: "ses_old", Model: model,
		Worker: &protocol.WorkerInfo{ID: "w_old", Owner: owner, Project: "alpha", RunGeneration: 1}})
	if err != nil {
		t.Fatal(err)
	}
	cf.endWorker(t, worker, harness, model, level)
	return worker
}

// startedWorker starts a worker through the gateway for caller with the
// request's selection fields, as the caller's daemon sends it, then ends it as
// a worker judged at level.
func (cf continueFixture) startedWorker(t *testing.T, caller string, req protocol.WorkerStartRequest, level string) (string, protocol.WorkerStartAnswer) {
	t.Helper()
	req.Project, req.Task = "alpha", "implement feature x"
	ans, err := cf.g.workerStart(t.Context(), cf.nc, protocol.WorkerStartFrame{Owner: caller, Level: level, WorkerStartRequest: req})
	if err != nil || ans.SessionID == "" {
		t.Fatalf("first start: %+v %v", ans, err)
	}
	cf.endWorker(t, ans.SessionID, ans.Harness, ans.Model, level)
	return ans.SessionID, ans
}

// endWorker records the judge's level and one run for a worker session, and
// ends it.
func (cf continueFixture) endWorker(t *testing.T, worker, harness, model, level string) {
	t.Helper()
	g, nc := cf.g, cf.nc
	if err := g.store.RecordWorkerAssessment(protocol.WorkerAssessment{RunID: "r_" + worker, SessionID: worker, StartedAt: 1, AssessedAt: 1,
		Label: level, D: protocol.PickerD(nil, level), Confidence: "high"}); err != nil {
		t.Fatal(err)
	}
	if err := g.store.RecordWorkerRun(protocol.WorkerRun{NodeID: nc.id, SessionID: worker, Harness: harness, Model: model,
		Outcome: protocol.OutcomePartial, StartedAt: 1, EndedAt: 2}); err != nil {
		t.Fatal(err)
	}
	if err := g.store.SetWorkerSessionEnded(nc.id, worker, 1); err != nil {
		t.Fatal(err)
	}
}

// continueWorker continues the worker and returns the answer and what the
// machine was asked to run.
func (cf continueFixture) continueWorker(t *testing.T, worker, model string) (protocol.WorkerStartAnswer, protocol.WorkerContinue, error) {
	t.Helper()
	return cf.continueAs(t, cf.g, cf.owner, worker, model)
}

// continueAs continues the worker through g for caller.
func (cf continueFixture) continueAs(t *testing.T, g *Gateway, caller, worker, model string) (protocol.WorkerStartAnswer, protocol.WorkerContinue, error) {
	t.Helper()
	ans, err := g.workerStart(t.Context(), cf.nc, protocol.WorkerStartFrame{Owner: caller,
		WorkerStartRequest: protocol.WorkerStartRequest{Continue: worker, Model: model}})
	if err != nil {
		return ans, protocol.WorkerContinue{}, err
	}
	return ans, <-cf.frames, nil
}

// setScores sets the owner's scores of the four profiles, then the extra ones.
func (cf continueFixture) setScores(t *testing.T, extra ...protocol.ModelScore) {
	t.Helper()
	scores := []protocol.ModelScore{{Match: "claude-opus-5-5", Score: 55, Source: "owner"}, {Match: "gpt-6.1-terra", Score: 52, Source: "owner"},
		{Match: "deepseek/deepseek-v4.1-flash", Score: 45, Source: "owner"}, {Match: "opencode-go/deepseek-v4.1-flash", Score: 45, Source: "owner"}}
	setOwnerScores(t, cf.g.store, append(extra, scores...))
}

// underFloor puts Anthropic under a floor of 40 percent.
func (cf continueFixture) underFloor(t *testing.T) {
	t.Helper()
	g := cf.g
	if err := g.store.setOwner(workerFloorsKey, protocol.WorkerFloors{Floors: map[string]int{"anthropic": 40}}); err != nil {
		t.Fatal(err)
	}
	now := g.store.now()
	g.usage.set(cf.nc.id, now, protocol.UsageReport{Quotas: &protocol.Quotas{GeneratedAt: now.Unix(), Providers: []protocol.QuotaProvider{
		{Provider: "claude", Windows: []protocol.QuotaWindow{{ID: "5h", PercentUsed: 70}}}}}})
}

// A continue of a worker whose model the owner paused runs on the picker's
// choice at the stored level, and the note names the old model, the new one
// and why. The pick's rows are recorded with the new run.
func TestContinueMovesAPausedModel(t *testing.T) {
	cf := continueGateway(t)
	cf.setScores(t, pausedScore("claude-opus-5-5"))
	worker := cf.endedWorker(t, "claude", opusModel, "hard")
	ans, f, err := cf.continueWorker(t, worker, "")
	if err != nil {
		t.Fatalf("continue: %v", err)
	}
	if f.Harness == "" || f.Harness == "claude" || f.Model == "" {
		t.Fatalf("the machine was asked to run %q %q", f.Harness, f.Model)
	}
	want := "continue moved from claude " + opusModel + " to " + f.Harness + " " + f.Model + " at hard (old model: paused by the owner)"
	if !strings.Contains(ans.ModelNote, want) {
		t.Fatalf("note %q lacks %q", ans.ModelNote, want)
	}
	if f.Assessment == nil || len(f.Assessment.PickRows) == 0 || !strings.Contains(f.Assessment.PickerNote, want) {
		t.Fatalf("the start does not carry the pick: %+v", f.Assessment)
	}
	recorded, err := cf.g.store.WorkerAssessment("s_continued")
	if err != nil || recorded == nil || !strings.Contains(recorded.PickerNote, want) {
		t.Fatalf("recorded pick: %+v %v", recorded, err)
	}
	var rows int
	if err := cf.g.store.db.QueryRow(`SELECT COUNT(*) FROM pick_candidates WHERE run_id=?`, recorded.RunID).Scan(&rows); err != nil || rows == 0 {
		t.Fatalf("pick rows of the continued run: %d %v", rows, err)
	}
}

// A model whose provider is under its floor no longer qualifies: the continue
// moves to a model that can run.
func TestContinueMovesAModelUnderItsFloor(t *testing.T) {
	cf := continueGateway(t)
	cf.setScores(t)
	cf.underFloor(t)
	worker := cf.endedWorker(t, "claude", opusModel, "normal")
	ans, f, err := cf.continueWorker(t, worker, "")
	if err != nil {
		t.Fatalf("continue: %v", err)
	}
	if f.Harness == "" || f.Harness == "claude" {
		t.Fatalf("the machine was asked to run %q %q; note %q", f.Harness, f.Model, ans.ModelNote)
	}
	if !strings.Contains(ans.ModelNote, "continue moved from claude "+opusModel) || !strings.Contains(ans.ModelNote, "floor") {
		t.Fatalf("note: %q", ans.ModelNote)
	}
}

// A model whose benchmark is below the stored level no longer qualifies.
func TestContinueMovesAModelBelowTheLevel(t *testing.T) {
	cf := continueGateway(t)
	cf.setScores(t)
	worker := cf.endedWorker(t, "opencode", deepseekModel, "very_complex")
	ans, f, err := cf.continueWorker(t, worker, "")
	if err != nil {
		t.Fatalf("continue: %v", err)
	}
	if f.Harness == "" || f.Model == deepseekModel {
		t.Fatalf("the machine was asked to run %q %q; note %q", f.Harness, f.Model, ans.ModelNote)
	}
	if !strings.Contains(ans.ModelNote, "continue moved from opencode "+deepseekModel+" to "+f.Harness+" "+f.Model+" at very_complex") {
		t.Fatalf("note: %q", ans.ModelNote)
	}
}

// A protected provider is saved for the orchestrator: a continue moves off it
// when another model can run.
func TestContinueMovesAProtectedModel(t *testing.T) {
	cf := continueGateway(t)
	cf.setScores(t)
	if err := cf.g.store.setOwner(protectedProvidersKey, protocol.ProtectedProviders{Providers: []string{"anthropic"}}); err != nil {
		t.Fatal(err)
	}
	worker := cf.endedWorker(t, "claude", opusModel, "normal")
	ans, f, err := cf.continueWorker(t, worker, "")
	if err != nil {
		t.Fatalf("continue: %v", err)
	}
	if f.Harness == "" || f.Harness == "claude" || !strings.Contains(ans.ModelNote, "protected provider (anthropic)") {
		t.Fatalf("the machine was asked to run %q %q; note %q", f.Harness, f.Model, ans.ModelNote)
	}
}

// A model that still qualifies stays: the machine continues on its own model
// and the note says nothing about a move.
func TestContinueKeepsAModelThatQualifies(t *testing.T) {
	cf := continueGateway(t)
	cf.setScores(t)
	worker := cf.endedWorker(t, "codex", terraModel, "normal")
	ans, f, err := cf.continueWorker(t, worker, "")
	if err != nil {
		t.Fatalf("continue: %v", err)
	}
	if f.Harness != "" || f.Model != "" || strings.Contains(ans.ModelNote, "continue ") {
		t.Fatalf("the machine was asked to run %q %q; note %q", f.Harness, f.Model, ans.ModelNote)
	}
}

// When no other model qualifies, a model that may still run stays, and the
// note says why.
func TestContinueKeepsTheOldModelWhenNoOtherQualifies(t *testing.T) {
	cf := continueGateway(t)
	cf.setScores(t, pausedScore("gpt-6.1-terra"), pausedScore("deepseek/deepseek-v4.1-flash"), pausedScore("opencode-go/deepseek-v4.1-flash"))
	cf.underFloor(t)
	worker := cf.endedWorker(t, "claude", opusModel, "normal")
	ans, f, err := cf.continueWorker(t, worker, "")
	if err != nil {
		t.Fatalf("continue: %v", err)
	}
	if f.Harness != "" || f.Model != "" {
		t.Fatalf("the machine was asked to run %q %q", f.Harness, f.Model)
	}
	if want := "continue kept claude " + opusModel + ": no other model qualifies at normal (old model: "; !strings.Contains(ans.ModelNote, want) {
		t.Fatalf("note %q lacks %q", ans.ModelNote, want)
	}
}

// A paused model with no other model that qualifies is refused as before, and
// the refusal says that no other model qualifies.
func TestContinueOfAPausedModelWithNoOtherIsRefused(t *testing.T) {
	cf := continueGateway(t)
	cf.setScores(t, pausedScore("claude-opus-5-5"), pausedScore("gpt-6.1-terra"), pausedScore("deepseek/deepseek-v4.1-flash"), pausedScore("opencode-go/deepseek-v4.1-flash"))
	worker := cf.endedWorker(t, "claude", opusModel, "hard")
	_, _, err := cf.continueWorker(t, worker, "")
	if err == nil || !strings.Contains(err.Error(), "paused by the owner") || !strings.Contains(err.Error(), "no other model qualifies at hard") {
		t.Fatalf("continue: %v", err)
	}
}

// The owner's continue that names a model keeps it, even a paused one.
func TestContinueNamingAModelKeepsIt(t *testing.T) {
	cf := continueGateway(t)
	cf.setScores(t, pausedScore("claude-opus-5-5"))
	worker := cf.endedWorker(t, "claude", opusModel, "hard")
	ans, f, err := cf.continueWorker(t, worker, opusModel)
	if err != nil {
		t.Fatalf("continue: %v", err)
	}
	if f.Model != opusModel || strings.Contains(ans.ModelNote, "continue moved") {
		t.Fatalf("the machine was asked to run %q %q; note %q", f.Harness, f.Model, ans.ModelNote)
	}
}

// restartedGateway is a new gateway on the same database, as after a restart,
// with the fake machine connected to it.
func (cf continueFixture) restartedGateway(t *testing.T) *Gateway {
	t.Helper()
	g, err := New(Options{DataDir: cf.g.opts.DataDir, TempFolders: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.Close() })
	g.store.now = cf.clock.now
	g.opts.TestNoBriefChecks = true
	setTestConn(t, g, cf.nc.id, cf.nc)
	return g
}

// The model the owner named at a worker's first start holds for a continue
// that names none, across a gateway restart, even after he paused it: the
// re-pick of decision 0189 does not move it (decision 0191).
func TestContinueKeepsTheModelTheOwnerNamed(t *testing.T) {
	cf := continueGateway(t)
	cf.setScores(t)
	worker, first := cf.startedWorker(t, cf.owner, protocol.WorkerStartRequest{Model: opusModel}, "hard")
	if first.Harness != "claude" || first.Model != opusModel {
		t.Fatalf("first start ran %q %q", first.Harness, first.Model)
	}
	cf.setScores(t, pausedScore("claude-opus-5-5"))
	g := cf.restartedGateway(t)
	ans, f, err := cf.continueAs(t, g, cf.owner, worker, "")
	if err != nil {
		t.Fatalf("continue: %v", err)
	}
	if f.Harness != "claude" || f.Model != opusModel || strings.Contains(ans.ModelNote, "continue moved") {
		t.Fatalf("the machine was asked to run %q %q; note %q", f.Harness, f.Model, ans.ModelNote)
	}
	if want := "kept claude " + opusModel + ", which the owner named at the worker's first start"; !strings.Contains(ans.ModelNote, want) {
		t.Fatalf("note %q lacks %q", ans.ModelNote, want)
	}
}

// The owner's choice still meets the gate: a continue of a model whose quota
// is used up is refused, and the refusal says why and that his choice was
// kept.
func TestContinueOfTheOwnersModelWithNoQuotaIsRefused(t *testing.T) {
	cf := continueGateway(t)
	cf.setScores(t)
	worker, _ := cf.startedWorker(t, cf.owner, protocol.WorkerStartRequest{Model: opusModel}, "normal")
	now := cf.g.store.now()
	cf.g.usage.set(cf.nc.id, now, protocol.UsageReport{Quotas: &protocol.Quotas{GeneratedAt: now.Unix(), Providers: []protocol.QuotaProvider{
		{Provider: "claude", Windows: []protocol.QuotaWindow{{ID: "5h", PercentUsed: 100, Exhausted: true}}}}}})
	_, _, err := cf.continueWorker(t, worker, "")
	if err == nil || !strings.Contains(err.Error(), "5h") || !strings.Contains(err.Error(), "which the owner named") {
		t.Fatalf("continue: %v", err)
	}
}

// A harness the owner named alone holds too: the continue re-picks inside it,
// so a model he paused moves to another model of the same harness.
func TestContinueKeepsTheHarnessTheOwnerNamed(t *testing.T) {
	cf := continueGateway(t)
	cf.setScores(t)
	worker, first := cf.startedWorker(t, cf.owner, protocol.WorkerStartRequest{Harness: "opencode"}, "normal")
	if first.Harness != "opencode" {
		t.Fatalf("first start ran %q %q", first.Harness, first.Model)
	}
	cf.setScores(t, pausedScore(strings.TrimSuffix(first.Model, ":high")))
	ans, f, err := cf.continueWorker(t, worker, "")
	if err != nil {
		t.Fatalf("continue: %v", err)
	}
	if f.Harness != "opencode" || f.Model == "" || f.Model == first.Model {
		t.Fatalf("the machine was asked to run %q %q after %q; note %q", f.Harness, f.Model, first.Model, ans.ModelNote)
	}
	if !strings.Contains(ans.ModelNote, "kept harness opencode, which the owner named") {
		t.Fatalf("note: %q", ans.ModelNote)
	}
	// The re-pick's roster held only that harness.
	var others int
	if err := cf.g.store.db.QueryRow(`SELECT COUNT(*) FROM pick_candidates WHERE run_id='k_continue' AND harness<>'opencode'`).Scan(&others); err != nil || others != 0 {
		t.Fatalf("%d rows of other harnesses in the re-pick: %v", others, err)
	}
}

// A model an agent named, not the owner, is not held: the continue re-picks
// as decision 0189 says.
func TestContinueOfAnAgentsNamedModelRepicks(t *testing.T) {
	cf := continueGateway(t)
	cf.setScores(t)
	worker, _ := cf.startedWorker(t, cf.agent, protocol.WorkerStartRequest{Model: opusModel}, "hard")
	cf.setScores(t, pausedScore("claude-opus-5-5"))
	ans, f, err := cf.continueAs(t, cf.g, cf.agent, worker, "")
	if err != nil {
		t.Fatalf("continue: %v", err)
	}
	if f.Harness == "" || f.Harness == "claude" || !strings.Contains(ans.ModelNote, "continue moved from claude "+opusModel) {
		t.Fatalf("the machine was asked to run %q %q; note %q", f.Harness, f.Model, ans.ModelNote)
	}
}

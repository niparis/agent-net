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
// session that runs what it was asked for.
type continueFixture struct {
	g      *Gateway
	nc     *nodeConn
	owner  string
	frames chan protocol.WorkerContinue
}

func continueGateway(t *testing.T) continueFixture {
	t.Helper()
	g, node, owner, _, _ := setupFloorGateway(t)
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
			if json.Unmarshal(raw, &h) != nil || h.Type != "worker.continue" {
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
	return continueFixture{g: g, nc: nc, owner: owner, frames: frames}
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
	if err := g.store.RecordWorkerAssessment(protocol.WorkerAssessment{RunID: "r_old", SessionID: worker, StartedAt: 1, AssessedAt: 1,
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
	return worker
}

// continueWorker continues the worker and returns the answer and what the
// machine was asked to run.
func (cf continueFixture) continueWorker(t *testing.T, worker, model string) (protocol.WorkerStartAnswer, protocol.WorkerContinue, error) {
	t.Helper()
	ans, err := cf.g.workerStart(t.Context(), cf.nc, protocol.WorkerStartFrame{Owner: cf.owner,
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

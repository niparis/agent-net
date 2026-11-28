package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/model"
	"github.com/niparis/agent-net/internal/protocol"
)

// An owner score of exactly 0 pauses a model (decision 0188). These tests ask
// each path that can choose a model and check that none chooses a paused one.

// pausedScore is the owner's score that pauses a model.
func pausedScore(match string) protocol.ModelScore {
	return protocol.ModelScore{Match: match, Score: 0, Source: "owner"}
}

// pausedPicks gives the picker table the tasks and asks the one choice path
// for n automatic picks between the model "paused", which the owner paused,
// and the model "active". The rotation is the stored one, so the exploration
// credit and the draws move as in real starts. It returns how often each model
// was chosen, the last answer and the picker note of a pick of "paused".
func pausedPicks(t *testing.T, tasks []protocol.PickerTask, level string, d float64, active []protocol.ModelScore, n int) (map[string]int, ChooseResult, string) {
	t.Helper()
	g, node := roleGateway(t)
	g.store.picker.mu.Lock()
	g.store.picker.table = protocol.BuildPickerTable(tasks, protocol.DefaultPickerSettings())
	g.store.picker.mu.Unlock()
	view := &protocol.NodeView{NodeID: node, Hostname: "role-box", Harnesses: []protocol.HarnessInfo{{Name: "pi", SignedIn: "yes"}}}
	candidates := []protocol.WorkerModelCandidate{{Name: "paused", Harness: "pi", Model: "paused"}, {Name: "active", Harness: "pi", Model: "active"}}
	scores := append([]protocol.ModelScore{pausedScore("paused")}, active...)
	counts := map[string]int{}
	var last ChooseResult
	pausedNote := ""
	for i := 0; i < n; i++ {
		got, err := g.Choose(ChooseRequest{Purpose: model.PurposeWorkerAutomatic, Node: view, Candidates: candidates,
			Difficulty: level, D: d, Scores: scores, Persist: true})
		if err != nil {
			t.Fatalf("pick %d: %v", i, err)
		}
		counts[got.Choice.Model]++
		if got.Choice.Model == "paused" {
			pausedNote = got.Choice.PickNote
		}
		last = got
	}
	return counts, last, pausedNote
}

// pickerTasks is n finished tasks of one model at difficulty d, each with the
// same score.
func pickerTasks(key string, d, score float64, n int) []protocol.PickerTask {
	out := make([]protocol.PickerTask, n)
	for i := range out {
		out[i] = protocol.PickerTask{Key: key, D: d, Score: score}
	}
	return out
}

// trivialD is a difficulty in the trivial bucket, where a score of 0 meets
// the minimum, so the benchmark alone allows a paused model.
func trivialD() float64 { return protocol.PickerD(nil, "trivial") }

// The cell rule allows a model by its record whatever its score. A paused
// model with the best record is still never chosen, and the pick's rows and
// note say why. The active model has no score at all: a missing score is not
// a pause.
func TestPausedModelWithTheBestRecordIsNotChosen(t *testing.T) {
	tasks := append(pickerTasks("paused", 2.5, 1, 15), pickerTasks("active", 2.5, .6, 15)...)
	counts, last, note := pausedPicks(t, tasks, "normal", 2.5, nil, 20)
	if counts["paused"] != 0 || counts["active"] != 20 {
		t.Fatalf("picks %v; a pick of the paused model said: %s", counts, note)
	}
	found := false
	for _, r := range last.Pick {
		if r.Model == "paused" && r.LeftOutReason == "paused by the owner" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no pick row says the paused model is paused by the owner: %+v", last.Pick)
	}
	if !strings.Contains(last.Note, "paused by the owner") {
		t.Fatalf("the note does not say the model is paused by the owner: %q", last.Note)
	}
}

// A model with a thin cell is open to exploration. A paused model is not
// explored.
func TestPausedModelIsNotExplored(t *testing.T) {
	tasks := pickerTasks("active", trivialD(), 1, 15)
	counts, _, note := pausedPicks(t, tasks, "trivial", trivialD(), []protocol.ModelScore{{Match: "active", Score: 50, Source: "owner"}}, 30)
	if counts["paused"] != 0 {
		t.Fatalf("picks %v; a pick of the paused model said: %s", counts, note)
	}
}

// With no record at all, the pick draws from the models the benchmark
// allows. A paused model is not in that draw.
func TestPausedModelIsNotInTheAllowedDraw(t *testing.T) {
	counts, _, note := pausedPicks(t, nil, "trivial", trivialD(), []protocol.ModelScore{{Match: "active", Score: 10, Source: "owner"}}, 20)
	if counts["paused"] != 0 {
		t.Fatalf("picks %v; a pick of the paused model said: %s", counts, note)
	}
}

// pausedStartGateway is the floor gateway with the owner's scores: Opus is
// paused, the other profiles are not.
func pausedStartGateway(t *testing.T) (*Gateway, *nodeConn, string, string) {
	t.Helper()
	g, node, owner, agent, _ := setupFloorGateway(t)
	setOwnerScores(t, g.store, []protocol.ModelScore{pausedScore("claude-opus-5-5"),
		{Match: "gpt-6.1-terra", Score: 52, Source: "owner"}, {Match: "deepseek/deepseek-v4.1-flash", Score: 45, Source: "owner"},
		{Match: "opencode-go/deepseek-v4.1-flash", Score: 45, Source: "owner"}})
	g.mu.Lock()
	nc := g.conns[node]
	g.mu.Unlock()
	return g, nc, owner, agent
}

// A new automatic start never chooses a paused model, even the one with the
// best record at the start's difficulty, and its note says why.
func TestAutomaticStartLeavesOutAPausedModel(t *testing.T) {
	g, nc, owner, _ := pausedStartGateway(t)
	g.store.picker.mu.Lock()
	g.store.picker.table = protocol.BuildPickerTable(pickerTasks("claude-opus-5-5:high", protocol.PickerD(nil, "hard"), 1, 15), protocol.DefaultPickerSettings())
	g.store.picker.mu.Unlock()
	ans, err := g.workerStart(context.Background(), nc, protocol.WorkerStartFrame{Owner: owner, Level: "hard",
		WorkerStartRequest: protocol.WorkerStartRequest{Project: "alpha", Task: "test", Model: "auto"}}, true)
	if err != nil {
		t.Fatal(err)
	}
	if ans.Harness == "claude" || !strings.Contains(ans.ModelNote, "skipped opus: paused by the owner") {
		t.Fatalf("start chose %s %s; note: %s", ans.Harness, ans.Model, ans.ModelNote)
	}
}

// An agent that names a paused model is refused with the reason, and told what
// the picker would choose instead.
func TestAgentStartNamingAPausedModelIsRefused(t *testing.T) {
	g, nc, _, agent := pausedStartGateway(t)
	_, err := g.workerStart(context.Background(), nc, protocol.WorkerStartFrame{Owner: agent, Level: "hard",
		WorkerStartRequest: protocol.WorkerStartRequest{Project: "alpha", Task: "test", Harness: "claude", Model: "claude-opus-5-5"}}, true)
	if err == nil || !strings.Contains(err.Error(), "paused by the owner") || !strings.Contains(err.Error(), "try ") {
		t.Fatalf("start of a paused model: %v", err)
	}
}

// The owner's own start that names a paused model goes ahead, as before the
// pause existed: he chose it.
func TestOwnerStartNamingAPausedModelGoesAhead(t *testing.T) {
	g, nc, owner, _ := pausedStartGateway(t)
	ans, err := g.workerStart(context.Background(), nc, protocol.WorkerStartFrame{Owner: owner, Level: "hard",
		WorkerStartRequest: protocol.WorkerStartRequest{Project: "alpha", Task: "test", Harness: "claude", Model: "claude-opus-5-5"}}, true)
	if err != nil || ans.Model != "claude-opus-5-5" {
		t.Fatalf("the owner's start of a paused model: %+v %v", ans, err)
	}
}

// endedPausedWorker is an ended OpenCode worker of the owner session whose
// model the owner paused. OpenCode does not tell the session's model, so the
// session row has none: only the run the machine reported names it.
func endedPausedWorker(t *testing.T, g *Gateway, nc *nodeConn, owner string) string {
	t.Helper()
	worker, err := g.store.UpsertSession(nc.id, protocol.SessionInfo{Harness: "opencode", NativeID: "ses_paused",
		Worker: &protocol.WorkerInfo{ID: "w_paused", Owner: owner, Project: "alpha", RunGeneration: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.store.RecordWorkerRun(protocol.WorkerRun{NodeID: nc.id, SessionID: worker, Harness: "opencode", Model: "deepseek/deepseek-v4.1-flash",
		Outcome: protocol.OutcomePartial, StartedAt: 1, EndedAt: 2}); err != nil {
		t.Fatal(err)
	}
	if err := g.store.SetWorkerSessionEnded(nc.id, worker, 1); err != nil {
		t.Fatal(err)
	}
	setOwnerScores(t, g.store, []protocol.ModelScore{pausedScore("deepseek/deepseek-v4.1-flash")})
	return worker
}

// A continue of a worker whose model is paused does not start it again on that
// model. It is refused with the reason, before the machine is asked, so the
// machine keeps the worker, its worktree and its branch as they are. A continue
// that names another model, or the paused one by the owner himself, goes ahead.
func TestContinueOfAPausedWorkerIsRefused(t *testing.T) {
	g, nc, owner, _ := pausedStartGateway(t)
	worker := endedPausedWorker(t, g, nc, owner)
	start := func(harness, model string) error {
		_, err := g.workerStart(context.Background(), nc, protocol.WorkerStartFrame{Owner: owner,
			WorkerStartRequest: protocol.WorkerStartRequest{Continue: worker, Harness: harness, Model: model}}, true)
		return err
	}
	if err := start("", ""); err == nil || !strings.Contains(err.Error(), "paused by the owner") {
		t.Fatalf("continue on the paused model: %v", err)
	}
	if err := start("codex", "gpt-6.1-terra"); err != nil {
		t.Fatalf("continue on another model: %v", err)
	}
	if err := start("", "deepseek/deepseek-v4.1-flash"); err != nil {
		t.Fatalf("the owner's continue that names the paused model: %v", err)
	}
}

// A message to an ended worker whose model is paused does not start it again.
// The sender is told why, and the machine is not asked.
func TestMessageToAPausedWorkerDoesNotRestartIt(t *testing.T) {
	g, node, owner, worker, received, _ := quotaResumeSetup(t)
	setOwnerScores(t, g.store, []protocol.ModelScore{pausedScore("gpt-5.5")})
	for len(received) > 0 {
		<-received
	}
	err := quotaResumeSend(t, g, node, owner, worker)
	if err == nil || !strings.Contains(err.Error(), "paused by the owner") {
		t.Fatalf("message to a paused worker: %v", err)
	}
	// The refusal comes before the start is handed to the machine, so no
	// worker.resume can follow it.
	for len(received) > 0 {
		var h protocol.Header
		if json.Unmarshal(<-received, &h) == nil && h.Type == "worker.resume" {
			t.Fatal("the machine was asked to start the paused worker again")
		}
	}
}

// A role list entry whose model is paused is skipped for the next entry, with
// the reason in the note.
func TestRoleSkipsAPausedModel(t *testing.T) {
	g, node := roleGateway(t)
	setOwnerScores(t, g.store, []protocol.ModelScore{pausedScore("claude-opus-5-5")})
	setRoleList(t, g, protocol.RoleModelsKey(protocol.RoleChief),
		protocol.RoleModel{Harness: "claude", Model: "claude-opus-5-5"}, protocol.RoleModel{Harness: "codex", Model: "gpt-6.1-sol"})
	got, err := g.pickRole(node, protocol.RolePickRequest{Role: protocol.RoleChief})
	if err != nil || got.Harness != "codex" || !strings.Contains(got.Note, "paused by the owner") {
		t.Fatalf("role pick: %+v %v", got, err)
	}
}

// An agent's start that names a paused model in the owner's own words, which
// its daemon found, goes ahead: the owner chose it.
func TestOwnerWordsStartNamingAPausedModelGoesAhead(t *testing.T) {
	g, nc, _, agent := pausedStartGateway(t)
	ans, err := g.workerStart(context.Background(), nc, protocol.WorkerStartFrame{Owner: agent, Level: "hard", OwnerSelected: true,
		WorkerStartRequest: protocol.WorkerStartRequest{Project: "alpha", Task: "test", Harness: "claude", Model: "claude-opus-5-5"}}, true)
	if err != nil || ans.Model != "claude-opus-5-5" {
		t.Fatalf("the owner's words naming a paused model: %+v %v", ans, err)
	}
}

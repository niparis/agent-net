package daemon

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// A Codex worker model may carry a reasoning effort after a colon, like Pi's
// :thinking. The model name alone goes to thread/start, the effort goes to
// every turn/start (also after thread/resume), and the worker shows the full
// "model:effort".
func TestCodexWorkerReasoningEffort(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startCodexNode(t, g, shortSet)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	owner := w.ownerID()

	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "codex", Model: "gpt-fake:high", Task: "say ok"})
	if res.Model != "gpt-fake:high" {
		t.Errorf("the start result model is %q", res.Model)
	}
	rec := readCodex(t, repo, func(r fakeCodexRecord) bool { return len(r.Turns) == 1 })
	if rec.Thread["model"] != "gpt-fake" {
		t.Errorf("thread/start model: %v", rec.Thread)
	}
	if got := rec.TurnParams[0]["effort"]; got != "high" {
		t.Errorf("turn/start effort: %v (params %v)", got, rec.TurnParams[0])
	}
	if a := w.agentOf(t, res.SessionID); a == nil || a.Model != "gpt-fake:high" {
		t.Errorf("the session model: %+v", a)
	}
	n := w.waitNotice(t, 1)[0]
	if !strings.Contains(n.Body, "(codex, gpt-fake:high)") {
		t.Errorf("the notice does not name the model with its effort:\n%s", n.Body)
	}

	// A follow-up reopens the thread; its turn keeps the effort too.
	w.workerStopped(t, g, protocol.StopHandback)
	var sent protocol.MessageSent
	mustJSON(t, w.cli.mustCall("owner_send", protocol.OwnerSendRequest{To: res.SessionID, Body: "and run the tests"}), &sent)
	got := w.replyTo(t, owner, sent.Message.ID)
	if !strings.Contains(got.Body, "status: done") {
		t.Errorf("the resumed reply: %s", got.Body)
	}
	rec = readCodex(t, repo, func(r fakeCodexRecord) bool { return r.Resumed == 1 && len(r.Turns) == 1 })
	if got := rec.TurnParams[0]["effort"]; got != "high" {
		t.Errorf("the resumed turn/start effort: %v (params %v)", got, rec.TurnParams[0])
	}
}

// An effort the model does not list is refused at start, with the levels it
// does list.
func TestCodexWorkerRefusesUnsupportedEffort(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startCodexNode(t, g, shortSet)
	repo := newRepo(t, "shop")
	w.register(t, repo)

	_, err := w.start(protocol.WorkerStartRequest{Project: "shop", Harness: "codex", Model: "gpt-fake:max", Task: "say ok"})
	if err == nil {
		t.Fatal("the unsupported effort was accepted")
	}
	for _, want := range []string{`does not support reasoning effort "max"`, "low, medium, high"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal lacks %q: %v", want, err)
		}
	}
	if n := w.d.workerCount(); n != 0 {
		t.Errorf("%d workers left after the refusal", n)
	}
}

// A continued worker gets the old worker's model, effort included, so its
// turns run at the same level.
func TestCodexContinuedWorkerKeepsEffort(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startCodexNode(t, g, shortSet)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	old := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "codex", Model: "gpt-fake:high",
		Task: "say ok", Isolation: protocol.IsolationWorktree})
	w.waitNotice(t, 1)
	w.workerStopped(t, g, protocol.StopHandback)

	res, err := w.startDefault(protocol.WorkerStartRequest{Continue: old.SessionID})
	if err != nil {
		t.Fatalf("continue: %v", err)
	}
	if res.Model != "gpt-fake:high" {
		t.Errorf("the continued worker model is %q", res.Model)
	}
	rec := readCodex(t, res.Worktree, func(r fakeCodexRecord) bool { return len(r.Turns) == 1 })
	if rec.Thread["model"] != "gpt-fake" {
		t.Errorf("thread/start model: %v", rec.Thread)
	}
	if got := rec.TurnParams[0]["effort"]; got != "high" {
		t.Errorf("turn/start effort: %v (params %v)", got, rec.TurnParams[0])
	}
}

func TestCodexSupportedEfforts(t *testing.T) {
	out := []byte(`{"models":[{"slug":"gpt-a","supported_reasoning_levels":[{"effort":"low","description":"Low"},{"effort":"high","description":"High"}]}]}`)
	got, ok := codexSupportedEfforts(out, "gpt-a")
	if !ok || strings.Join(got, ",") != "low,high" {
		t.Errorf("gpt-a: %v %v", got, ok)
	}
	if _, ok := codexSupportedEfforts(out, "gpt-b"); ok {
		t.Error("a model that is not listed is known")
	}
	if _, ok := codexSupportedEfforts([]byte("not json"), "gpt-a"); ok {
		t.Error("bad json is known")
	}
}

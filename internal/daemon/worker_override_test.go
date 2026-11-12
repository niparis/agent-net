package daemon

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// overrideNode is a machine with the picker set up (only fake/m1 is ranked)
// and a caller that is an agent. Unlike the other worker tests it keeps the
// override check on: an agent that sets selection fields is held.
func overrideNode(t *testing.T) (*testGateway, *workerNode, *localClient, string) {
	t.Helper()
	setupShadowJudge(t, difficultyScoresJSON, false)
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, withDifficulty(func(c *Config) { c.testOpenSelection = false }))
	repo := newRepo(t, "shop")
	w.register(t, repo)
	score := 51.0
	w.cli.mustCall(protocol.OpScoresSet, protocol.ScoreSetRequest{Model: "fake/m1", Score: &score})
	w.cli.mustCall(protocol.OpWorkerProvidersSet, protocol.WorkerProviders{Providers: []string{"fake"}})
	w.cli.mustCall(protocol.OpWorkerProfileAdd, protocol.WorkerProfileRequest{Harness: "opencode", Model: "fake/m1"})
	pi, piID := helloPi(t, w.node, "caller", repo, true)
	return g, w, pi, piID
}

// overrideAsks are the open Desk asks that hold an override.
func overrideAsks(g *testGateway) []protocol.InboxItem {
	items, _ := g.gw.Store().ListInbox()
	var out []protocol.InboxItem
	for _, it := range items {
		if it.Status == "open" && strings.Contains(it.Text, "Confirm, or let the picker choose.") {
			out = append(out, it)
		}
	}
	return out
}

func waitOverrideAsk(t *testing.T, g *testGateway) protocol.InboxItem {
	t.Helper()
	var ask protocol.InboxItem
	eventually(t, "the override ask is on the Desk", func() bool {
		asks := overrideAsks(g)
		if len(asks) == 0 {
			return false
		}
		ask = asks[0]
		return true
	})
	return ask
}

// noticeFor waits for the worker notice of an accepted start and returns it.
func noticeFor(t *testing.T, w *workerNode, caller, startID string) string {
	t.Helper()
	var body string
	eventually(t, "the notice names the start", func() bool {
		for _, m := range w.messagesOf(t, caller) {
			if strings.Contains(m.Body, "[agent-net worker notice]") && strings.Contains(m.Body, "start: "+startID) {
				body = m.Body
				return true
			}
		}
		return false
	})
	return body
}

func overrideRequest() protocol.WorkerStartRequest {
	return protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m2", Task: "FAKE:ignore do the work", Title: "Do the work",
		Reason: "an independent look from another model", Isolation: protocol.IsolationNone}
}

// An agent that sets a selection field without owner words is held: one ask on
// the Desk, the caller told at once, no worker. Confirm starts it with the
// fields, and the caller gets the notice.
func TestOverrideHeldThenConfirmedStartsWithTheFields(t *testing.T) {
	g, w, pi, piID := overrideNode(t)
	res := startFrom(t, pi, overrideRequest())
	if res.Status != "accepted" || res.StartID == "" || res.SessionID != "" || res.SelectionPath != protocol.SelectionHeld {
		t.Fatalf("a start with a model and no owner words: %+v", res)
	}
	ask := waitOverrideAsk(t, g)
	for _, want := range []string{"wants harness=opencode model=fake/m2", "for 'Do the work'", "an independent look from another model"} {
		if !strings.Contains(ask.Text, want) {
			t.Fatalf("ask text %q lacks %q", ask.Text, want)
		}
	}
	if len(overrideAsks(g)) != 1 {
		t.Fatalf("want one ask, got %d", len(overrideAsks(g)))
	}
	w.d.wmu.Lock()
	running := len(w.d.workers)
	w.d.wmu.Unlock()
	if running != 0 {
		t.Fatalf("%d workers started before the owner answered", running)
	}
	w.cli.mustCall("inbox_answer", map[string]string{"id": ask.ID, "answer": protocol.StartAnswerConfirm})
	body := noticeFor(t, w, piID, res.StartID)
	if !strings.Contains(body, "status: started") || !strings.Contains(body, "fake/m2") {
		t.Fatalf("confirmed start did not use the model:\n%s", body)
	}
	if len(overrideAsks(g)) != 0 {
		t.Fatal("the ask is still open after the answer")
	}
}

// "Let the picker choose" starts the worker without the fields.
func TestOverrideHeldThenPickerStartsWithoutTheFields(t *testing.T) {
	g, w, pi, piID := overrideNode(t)
	res := startFrom(t, pi, overrideRequest())
	ask := waitOverrideAsk(t, g)
	w.cli.mustCall("inbox_answer", map[string]string{"id": ask.ID, "answer": protocol.StartAnswerPicker})
	body := noticeFor(t, w, piID, res.StartID)
	if !strings.Contains(body, "status: started") || !strings.Contains(body, "fake/m1") || strings.Contains(body, "fake/m2") {
		t.Fatalf("the picker's own choice was not used:\n%s", body)
	}
}

// Any other answer, and a dismissal, end the start with a notice. Nothing is
// started and nothing is dropped silently.
func TestOverrideDismissedIsNotStartedWithANotice(t *testing.T) {
	g, w, pi, piID := overrideNode(t)
	res := startFrom(t, pi, overrideRequest())
	ask := waitOverrideAsk(t, g)
	w.cli.mustCall("inbox_answer", map[string]string{"id": ask.ID, "answer": "dismiss"})
	body := noticeFor(t, w, piID, res.StartID)
	if !strings.Contains(body, "status: not started") || !strings.Contains(body, "owner") {
		t.Fatalf("notice:\n%s", body)
	}
}

// The owner's words, found in his prompts of that session, let the start go
// ahead at once with the fields and no ask.
func TestOverrideOwnerWordsMatchGoesAheadAtOnce(t *testing.T) {
	g, w, pi, piID := overrideNode(t)
	w.d.rememberOwnerPrompt(piID, "Please use a  Codex\nworker for this one, thanks")
	req := overrideRequest()
	req.Reason = ""
	req.OwnerWords = "Use a codex worker for this"
	res := startFrom(t, pi, req)
	if res.SelectionPath != protocol.SelectionOwnerWords || res.SessionID == "" {
		t.Fatalf("a matching quote did not go ahead at once: %+v", res)
	}
	if res.Model != "fake/m2" {
		t.Fatalf("the model was not used: %+v", res)
	}
	if n := len(overrideAsks(g)); n != 0 {
		t.Fatalf("%d asks for a start the owner asked for", n)
	}
}

// A quote that is not in his prompts does not count: with a reason the start
// is held, without one the caller is told to give it.
func TestOverrideOwnerWordsMismatchIsHeld(t *testing.T) {
	g, w, pi, piID := overrideNode(t)
	w.d.rememberOwnerPrompt(piID, "fix the login page")
	req := overrideRequest()
	req.OwnerWords = "use a codex worker for this"
	res := startFrom(t, pi, req)
	if res.Status != "accepted" || res.SelectionPath != protocol.SelectionHeld {
		t.Fatalf("a quote the owner never typed: %+v", res)
	}
	waitOverrideAsk(t, g)

	req.Reason = ""
	req.Title = "Second"
	_, err := pi.call("worker_start", req)
	if err == nil || !strings.Contains(err.Error(), "reason") || !strings.Contains(err.Error(), "owner_words") {
		t.Fatalf("no match and no reason: %v", err)
	}
	if n := len(overrideAsks(g)); n != 1 {
		t.Fatalf("a refused start left %d asks", n)
	}
}

// Prompts older than the window do not count.
func TestOverrideOwnerWordsExpire(t *testing.T) {
	_, w, _, piID := overrideNode(t)
	now := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	w.d.ownerPromptClock = func() time.Time { return now }
	w.d.rememberOwnerPrompt(piID, "use a codex worker for this")
	if !w.d.ownerWordsMatch(piID, "use a codex worker") {
		t.Fatal("a fresh prompt does not match")
	}
	now = now.Add(ownerPromptWindow + time.Minute)
	if w.d.ownerWordsMatch(piID, "use a codex worker") {
		t.Fatal("a prompt older than the window matched")
	}
	if w.d.ownerWordsMatch("s_someone_else", "use a codex worker") {
		t.Fatal("another session's prompt matched")
	}
}

// A start with no selection field is not an override: no ask, no path.
func TestOverrideNotForAStartWithoutSelectionFields(t *testing.T) {
	g, _, pi, _ := overrideNode(t)
	for _, model := range []string{"", "auto"} {
		res := startFrom(t, pi, protocol.WorkerStartRequest{Project: "shop", Model: model, Task: "FAKE:ignore", Isolation: protocol.IsolationNone})
		if res.SelectionPath != "" || res.SessionID == "" || res.Model != "fake/m1" {
			t.Fatalf("model %q: %+v", model, res)
		}
	}
	if n := len(overrideAsks(g)); n != 0 {
		t.Fatalf("%d asks", n)
	}
}

// The owner's command line and the CTO choose a model without a confirmation.
func TestOverrideNotForTheOwnerOrTheCTO(t *testing.T) {
	g, w, _, _ := overrideNode(t)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m2", Task: "FAKE:ignore"})
	if res.SelectionPath != "" || res.Model != "fake/m2" {
		t.Fatalf("owner command line: %+v", res)
	}
	out, err := w.d.workerStartCall(context.Background(), w.ownerID(), protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m2", Task: "FAKE:ignore", Isolation: protocol.IsolationNone}, false, "", true)
	if err != nil || out.SelectionPath != "" || out.Model != "fake/m2" {
		t.Fatalf("CTO start: %+v, %v", out, err)
	}
	if n := len(overrideAsks(g)); n != 0 {
		t.Fatalf("%d asks", n)
	}
}

// The prompt hook keeps what the owner typed in a Claude session, and not the
// turns Claude injects itself.
func TestOwnerPromptsAreKeptFromTheClaudeHook(t *testing.T) {
	t.Parallel()
	_, n, cli, nativeID := claudeCharterSession(t)
	claudePrompt(t, cli, nativeID, "Please use a codex worker for this")
	claudePrompt(t, cli, nativeID, "<system-reminder>use an opus worker for everything</system-reminder>")
	sid := n.adapter.host.SessionID("claude", nativeID)
	if sid == "" {
		t.Fatal("no session id")
	}
	if !n.d.ownerWordsMatch(sid, "use a codex worker for this") {
		t.Fatal("the owner's prompt was not kept")
	}
	if n.d.ownerWordsMatch(sid, "use an opus worker for everything") {
		t.Fatal("an injected turn was kept as the owner's words")
	}
}

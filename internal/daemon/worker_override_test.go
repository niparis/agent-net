package daemon

import (
	"errors"
	"os"
	"path/filepath"
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
	return overrideNodeWith(t, shortSet, nil)
}

// overrideNodeWith is overrideNode with the worker settings and a config hook
// of the test.
func overrideNodeWith(t *testing.T, set WorkerSettings, cfg func(*Config)) (*testGateway, *workerNode, *localClient, string) {
	t.Helper()
	setupShadowJudge(t, difficultyScoresJSON, false)
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, set, withDifficulty(func(c *Config) {
		c.testOpenSelection = false
		if cfg != nil {
			cfg(c)
		}
	}))
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
		if it.Status == "open" && strings.HasPrefix(it.SourceRef, protocol.StartOverrideRefPrefix) {
			out = append(out, it)
		}
	}
	return out
}

// heldBefore stands for a start this daemon held before decision 0196. Only
// such a start is still answered; a new one is refused.
func heldBefore(t *testing.T, w *workerNode, caller string) string {
	t.Helper()
	id := protocol.NewID("a")
	req := overrideRequest()
	req.Reason = ""
	w.d.heldMu.Lock()
	if w.d.heldStarts == nil {
		w.d.heldStarts = map[string]*heldStart{}
	}
	h := &heldStart{id: id, caller: caller, req: req}
	w.d.heldStarts[id] = h
	w.d.heldMu.Unlock()
	// An earlier daemon saved it in daemon.db, so a restart still holds it.
	if err := w.d.st.addHeldStart(h, time.Now()); err != nil {
		t.Fatal(err)
	}
	return id
}

// answerHeld delivers the owner's answer to a held start, as the gateway does.
func answerHeld(w *workerNode, caller, id, answer string) {
	w.d.onStartDecision(protocol.StartDecision{Ref: protocol.StartOverrideRefPrefix + id, Session: caller, Answer: answer})
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

// An agent that sets a selection field without owner words is refused with
// one line (decision 0196): no ask, no held start, no worker.
func TestOverrideWithoutOwnerWordsIsRefused(t *testing.T) {
	g, w, pi, piID := overrideNode(t)
	w.d.rememberOwnerPrompt(piID, "fix the login page")
	for _, words := range []string{"", "use a codex worker for this"} {
		req := overrideRequest()
		req.OwnerWords = words
		_, err := pi.call("worker_start", req)
		if err == nil || err.Error() != protocol.OverrideNeedsOwner {
			t.Fatalf("owner words %q: %v", words, err)
		}
	}
	if n := len(overrideAsks(g)); n != 0 {
		t.Fatalf("%d Desk asks for a refused start", n)
	}
	w.d.heldMu.Lock()
	held := len(w.d.heldStarts)
	w.d.heldMu.Unlock()
	w.d.wmu.Lock()
	running := len(w.d.workers)
	w.d.wmu.Unlock()
	if held != 0 || running != 0 {
		t.Fatalf("held %d, running %d after a refusal", held, running)
	}
}

// A start held before decision 0196 is still answered: confirm starts it with
// the fields, and the caller gets the notice.
func TestOverrideHeldBeforeThenConfirmedStartsWithTheFields(t *testing.T) {
	_, w, _, piID := overrideNode(t)
	id := heldBefore(t, w, piID)
	answerHeld(w, piID, id, protocol.StartAnswerConfirm)
	body := noticeFor(t, w, piID, id)
	if !strings.Contains(body, "status: started") || !strings.Contains(body, "fake/m2") {
		t.Fatalf("confirmed start did not use the model:\n%s", body)
	}
}

// "Let the picker choose" starts the worker without the fields.
func TestOverrideHeldBeforeThenPickerStartsWithoutTheFields(t *testing.T) {
	_, w, _, piID := overrideNode(t)
	id := heldBefore(t, w, piID)
	answerHeld(w, piID, id, protocol.StartAnswerPicker)
	body := noticeFor(t, w, piID, id)
	if !strings.Contains(body, "status: started") || !strings.Contains(body, "fake/m1") || strings.Contains(body, "fake/m2") {
		t.Fatalf("the picker's own choice was not used:\n%s", body)
	}
}

// Any other answer, and a dismissal, end the start with a notice. Nothing is
// started and nothing is dropped silently.
func TestOverrideHeldBeforeDismissedIsNotStartedWithANotice(t *testing.T) {
	_, w, _, piID := overrideNode(t)
	id := heldBefore(t, w, piID)
	answerHeld(w, piID, id, "dismiss")
	body := noticeFor(t, w, piID, id)
	if !strings.Contains(body, "status: not started") || !strings.Contains(body, "was not approved") {
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

// The owner's command line chooses a model at once, with no ask. The CTO's
// standard start has no selection fields, so there is nothing to hold.
func TestOverrideNotForTheOwnerCommandLine(t *testing.T) {
	g, w, _, _ := overrideNode(t)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m2", Task: "FAKE:ignore"})
	if res.SelectionPath != "" || res.Model != "fake/m2" {
		t.Fatalf("owner command line: %+v", res)
	}
	if n := len(overrideAsks(g)); n != 0 {
		t.Fatalf("%d owner asks for the owner's own start", n)
	}
}

// The prompt hook keeps what the owner typed in a Claude session, and not the
// turns Claude injects itself.
func TestOwnerPromptsAreKeptFromTheClaudeHook(t *testing.T) {
	t.Parallel()
	_, n, cli, nativeID := claudeCharterSession(t)
	transcript := writeClaudeTranscript(t, t.TempDir(), nativeID,
		claudeUserEntry("p-owner", "Please use a codex worker for this", typedOrigin))
	askPolicy(t, cli, protocol.PolicyRequest{Harness: "claude", NativeID: nativeID,
		Prompt: "Please use a codex worker for this", PromptID: "p-owner", TranscriptPath: transcript})
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

// A start that is accepted and then fails to run tells the caller why. Nothing
// is dropped silently.
func TestOverrideAcceptedStartErrorTellsTheCaller(t *testing.T) {
	_, w, _, piID := overrideNode(t)
	w.d.reportAcceptedStart("a_fail", piID, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode"}, nil,
		errors.New("no allowed provider for this start"))
	body := noticeFor(t, w, piID, "a_fail")
	if !strings.Contains(body, "status: not started") || !strings.Contains(body, "no allowed provider") || !strings.Contains(body, "shop") {
		t.Fatalf("notice:\n%s", body)
	}
	if !strings.Contains(w.logs.String(), "worker.not_started start=a_fail") {
		t.Fatalf("no daemon log line:\n%s", w.logs.String())
	}
	w.d.wmu.Lock()
	running := len(w.d.workers)
	w.d.wmu.Unlock()
	if running != 0 {
		t.Fatalf("%d workers started", running)
	}
}

// An accepted start's notice carries the brief flags and the model note. The
// accepted answer came before either was known, so the notice is the only
// place the caller sees them.
func TestAcceptedStartNoticeCarriesBriefFlagsAndModelNote(t *testing.T) {
	_, w, _, piID := overrideNode(t)
	flags := []protocol.BriefFlag{{Check: protocol.BriefCheckPath, Line: "Read /home/owner/PLAN.md",
		Reason: "names a path outside the worker's own copy of the project."}}
	res := &protocol.WorkerStartResult{Status: "started", SessionID: "s_notice", Harness: "opencode", Model: "fake/m1",
		Project: "shop", Hostname: "box", ModelNote: "fake/m1 (30, good enough)", BriefFlags: flags}
	req := protocol.WorkerStartRequest{Project: "shop", Harness: "opencode"}
	w.d.reportAcceptedStart("a_notice", piID, req, res, nil)
	body := noticeFor(t, w, piID, "a_notice")
	for _, want := range []string{"status: started", "model note: fake/m1 (30, good enough)", "brief flags", protocol.BriefCheckPath, "PLAN.md"} {
		if !strings.Contains(body, want) {
			t.Fatalf("started notice lacks %q:\n%s", want, body)
		}
	}

	queued := *res
	queued.Status, queued.QueueID, queued.Position = "queued", "q_notice", 2
	w.d.reportAcceptedStart("a_queued_notice", piID, req, &queued, nil)
	qbody := noticeFor(t, w, piID, "a_queued_notice")
	for _, want := range []string{"status: queued", "model note: fake/m1 (30, good enough)", "brief flags", "PLAN.md"} {
		if !strings.Contains(qbody, want) {
			t.Fatalf("queued notice lacks %q:\n%s", want, qbody)
		}
	}
}

// A daemon that no longer holds the start tells the caller when the answer
// arrives, instead of dropping it.
func TestOverrideLostHeldStartTellsTheCaller(t *testing.T) {
	_, w, _, piID := overrideNode(t)
	id := heldBefore(t, w, piID)
	w.d.heldMu.Lock()
	delete(w.d.heldStarts, id)
	w.d.heldMu.Unlock()
	answerHeld(w, piID, id, protocol.StartAnswerConfirm)
	body := noticeFor(t, w, piID, id)
	if !strings.Contains(body, "status: not started") || !strings.Contains(body, "lost the held start") {
		t.Fatalf("notice:\n%s", body)
	}
}

// The owner's own words can name a model he paused: the start goes ahead on it
// at once, as before the pause existed (decision 0188).
func TestOwnerWordsCanNameAPausedModel(t *testing.T) {
	g, w, pi, piID := overrideNode(t)
	zero := 0.0
	w.cli.mustCall(protocol.OpScoresSet, protocol.ScoreSetRequest{Model: "fake/m2", Score: &zero})
	w.d.rememberOwnerPrompt(piID, "Use fake/m2 for this worker")
	req := overrideRequest()
	req.Reason = ""
	req.OwnerWords = "use fake/m2 for this worker"
	res := startFrom(t, pi, req)
	if res.SelectionPath != protocol.SelectionOwnerWords || res.SessionID == "" || res.Model != "fake/m2" {
		t.Fatalf("the owner's choice of a paused model: %+v", res)
	}
	if n := len(overrideAsks(g)); n != 0 {
		t.Fatalf("%d asks for a start the owner asked for", n)
	}
}

// A start held before decision 0196 is kept in daemon.db: an answer that
// arrives after a daemon restart starts the worker with its fields.
func TestOverrideHeldStartSurvivesADaemonRestart(t *testing.T) {
	g, w, _, piID := overrideNode(t)
	id := heldBefore(t, w, piID)
	w.restart()
	w.cli = cliClient(t, w.node)
	// The caller's session comes back with the new daemon, as a live one would.
	if _, id := helloPi(t, w.node, "caller", t.TempDir(), true); id != piID {
		t.Fatalf("the caller came back as %s, not %s", id, piID)
	}
	answerHeld(w, piID, id, protocol.StartAnswerConfirm)
	body := noticeFor(t, w, piID, id)
	if !strings.Contains(body, "status: started") || !strings.Contains(body, "fake/m2") {
		t.Fatalf("the start held before the restart did not start with its fields:\n%s", body)
	}
	if n := len(overrideAsks(g)); n != 0 {
		t.Fatalf("%d asks still open after the answer", n)
	}
}

// The owner types a prompt in a Claude session; then the gateway and the
// daemon restart. An override that quotes him from another session of the
// machine still starts at once: the daemon reads his words back from the
// transcript.
func TestOverrideOwnerWordsSurviveADaemonAndGatewayRestart(t *testing.T) {
	const native, words = "1f1f1f1f-0000-4000-8000-00000000bb01", "i want to try running it on m2max"
	root := t.TempDir()
	projects := filepath.Join(root, "projects", "-code-uadd3")
	if err := os.MkdirAll(projects, 0o700); err != nil {
		t.Fatal(err)
	}
	fa := &fakeAdapter{harness: "claude"}
	g, w, _, _ := overrideNodeWith(t, shortSet, func(c *Config) {
		c.ClaudeSessionsDir = filepath.Join(root, "sessions")
		c.Adapters = append(c.Adapters, fa)
	})
	lead := func() string {
		fa.setSessions(protocol.SessionInfo{Harness: "claude", NativeID: native, Cwd: "/code/uadd3", Status: "idle"})
		var sid string
		eventually(t, "the Claude session is registered", func() bool {
			sid = w.d.SessionID("claude", native)
			return sid != ""
		})
		return sid
	}
	sid := lead()
	transcript := writeClaudeTranscript(t, projects, native, typedAt("p-owner", words, time.Now()))
	askPolicy(t, w.cli, protocol.PolicyRequest{Harness: "claude", NativeID: native, Prompt: words, PromptID: "p-owner", TranscriptPath: transcript})
	eventually(t, "the hook kept the owner's words", func() bool {
		w.d.ownerPrompts.mu.Lock()
		defer w.d.ownerPrompts.mu.Unlock()
		return len(w.d.ownerPrompts.by[sid]) == 1
	})

	addr, dir := g.addr, g.dir
	g.stop()
	w.restartWith(func() { g = startGateway(t, dir, addr) }, true)
	w.cli = cliClient(t, w.node)
	lead()
	caller, _ := helloPi(t, w.node, "caller-after-restart", t.TempDir(), true)
	req := overrideRequest()
	req.Reason = ""
	req.OwnerWords = "I want to try running it on M2Max"
	res := startFrom(t, caller, req)
	if res.SelectionPath != protocol.SelectionOwnerWords || res.SessionID == "" || res.Model != "fake/m2" {
		t.Fatalf("a quote of words typed before the restart did not go ahead: %+v", res)
	}
	if n := len(overrideAsks(g)); n != 0 {
		t.Fatalf("%d asks for a start the owner asked for", n)
	}
}

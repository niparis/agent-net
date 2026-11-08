package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestClaudeTranscriptFailureParsing(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sess.jsonl")

	// 1. Normal assistant reply is not a failure.
	normal := `{"type":"assistant","message":{"role":"assistant","model":"claude-3-5-sonnet","content":[{"type":"text","text":"Hello world"}]}}` + "\n"
	if err := os.WriteFile(p, []byte(normal), 0o600); err != nil {
		t.Fatal(err)
	}

	msg, provider, newOff := claudeTranscriptFailureAt(p, 0)
	if msg != "" || provider {
		t.Fatalf("normal reply gave failure: %q, provider: %v", msg, provider)
	}
	if newOff != int64(len(normal)) {
		t.Fatalf("newOff = %d, want %d", newOff, len(normal))
	}

	// 2. The check reads only new lines.
	msg, provider, nextOff := claudeTranscriptFailureAt(p, newOff)
	if msg != "" || provider || nextOff != newOff {
		t.Fatalf("no new lines gave failure: %q %v %d", msg, provider, nextOff)
	}

	// 3. Synthetic model-not-found error.
	syntheticModel := `{"type":"assistant","message":{"role":"assistant","model":"<synthetic>","content":[{"type":"text","text":"There's an issue with the selected model (claude-opus-5-5:xhigh). It may not exist or you may not have access to it. Run /model to pick a different model."}]}}` + "\n"
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(syntheticModel)
	f.Close()

	msg, provider, endOff := claudeTranscriptFailureAt(p, newOff)
	wantText := "There's an issue with the selected model (claude-opus-5-5:xhigh). It may not exist or you may not have access to it. Run /model to pick a different model."
	if msg != wantText {
		t.Fatalf("got %q, want %q", msg, wantText)
	}
	if !provider {
		t.Fatal("synthetic model error must be provider=true")
	}
	if endOff != int64(len(normal)+len(syntheticModel)) {
		t.Fatalf("endOff = %d, want %d", endOff, len(normal)+len(syntheticModel))
	}

	// 4. Rate limit / capacity error in synthetic message.
	p2 := filepath.Join(dir, "sess2.jsonl")
	capacity := `{"type":"assistant","message":{"role":"assistant","model":"<synthetic>","content":[{"type":"text","text":"HTTP 429: Selected model is at capacity. Please try a different model."}]}}` + "\n"
	if err := os.WriteFile(p2, []byte(capacity), 0o600); err != nil {
		t.Fatal(err)
	}
	msg, provider, _ = claudeTranscriptFailureAt(p2, 0)
	if !strings.Contains(msg, "at capacity") || !provider {
		t.Fatalf("capacity error got %q %v", msg, provider)
	}
	if got := protocol.WorkerOutcomeDetail(protocol.StopModelFails, protocol.StatusFailed, nil, msg); got != protocol.OutcomeInfra {
		t.Fatalf("capacity error outcome: got %s, want %s", got, protocol.OutcomeInfra)
	}
}

func writeClaudeTranscriptLine(t *testing.T, projectsDir, sessionID, cwd, line string) {
	t.Helper()
	dir := filepath.Join(projectsDir, claudeTranscriptDir(cwd))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, sessionID+".jsonl")
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if !strings.HasSuffix(line, "\n") {
		line += "\n"
	}
	if _, err := f.WriteString(line); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeWorkerSyntheticModelFailureSendsNotice(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	set := shortSet
	set.Idle = time.Hour
	c := startClaudeNode(t, g, set)
	repo := newRepo(t, "shop")
	c.register(t, repo)

	// FAKE:ignore keeps the fake session from handing back, so the model
	// failure is the only way the worker ends and its notice is the failure.
	res := c.mustStart(t, protocol.WorkerStartRequest{Model: "claude-opus-5-5:xhigh", Project: "shop", Harness: "claude", Task: "FAKE:ignore test task"})
	rec := readClaude(t, repo)

	projectsDir := filepath.Join(filepath.Dir(c.sessions), "projects")
	errLine := `{"type":"assistant","message":{"role":"assistant","model":"<synthetic>","content":[{"type":"text","text":"There's an issue with the selected model (claude-opus-5-5:xhigh). It may not exist or you may not have access to it. Run /model to pick a different model."}]}}`

	var wk *worker
	c.d.wmu.Lock()
	for _, w := range c.d.workers {
		if w.sessionID == res.SessionID {
			wk = w
			break
		}
	}
	c.d.wmu.Unlock()
	if wk == nil {
		t.Fatal("worker not found")
	}

	writeClaudeTranscriptLine(t, projectsDir, rec.Session, wk.path, errLine)

	// Trigger worker check
	c.d.checkWorkers(context.Background())

	c.workerStopped(t, g, protocol.StopModelFails)

	n := c.waitNotice(t, 1)[0]
	wantNoticeText := "There's an issue with the selected model (claude-opus-5-5:xhigh). It may not exist or you may not have access to it. Run /model to pick a different model."
	if !strings.Contains(n.Body, "status: failed") {
		t.Errorf("notice lacks status: failed:\n%s", n.Body)
	}
	if !strings.Contains(n.Body, "note: the model call failed: "+wantNoticeText) {
		t.Errorf("notice lacks expected note:\n%s", n.Body)
	}
}

func TestClaudeWorkerProcessExitedFailureNoteWins(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	set := shortSet
	c := startClaudeNode(t, g, set)
	repo := newRepo(t, "shop")
	c.register(t, repo)

	// FAKE:ignore keeps the fake session from handing back, so the killed
	// process is the only end and the failure note must win over it.
	res := c.mustStart(t, protocol.WorkerStartRequest{Model: "claude-opus-5-5:xhigh", Project: "shop", Harness: "claude", Task: "FAKE:ignore test task"})
	rec := readClaude(t, repo)

	projectsDir := filepath.Join(filepath.Dir(c.sessions), "projects")
	errLine := `{"type":"assistant","message":{"role":"assistant","model":"<synthetic>","content":[{"type":"text","text":"There's an issue with the selected model (claude-opus-5-5:xhigh). It may not exist or you may not have access to it. Run /model to pick a different model."}]}}`

	var wk *worker
	c.d.wmu.Lock()
	for _, w := range c.d.workers {
		if w.sessionID == res.SessionID {
			wk = w
			break
		}
	}
	c.d.wmu.Unlock()
	if wk == nil {
		t.Fatal("worker not found")
	}

	writeClaudeTranscriptLine(t, projectsDir, rec.Session, wk.path, errLine)

	// Kill the process so it is gone
	syscall.Kill(-rec.Pgid, syscall.SIGKILL)

	// The failure note must win over "the harness process ended by itself, with no hand-back"
	c.workerStopped(t, g, protocol.StopModelFails)

	n := c.waitNotice(t, 1)[0]
	if !strings.Contains(n.Body, "the model call failed: There's an issue with the selected model") {
		t.Errorf("failure note did not win over process exit notice:\n%s", n.Body)
	}
}

func TestClaudeWorkerCapacityFailureIsInfra(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	set := shortSet
	set.Idle = time.Hour
	c := startClaudeNode(t, g, set)
	repo := newRepo(t, "shop")
	c.register(t, repo)

	// FAKE:ignore keeps the fake session from handing back, so the model
	// failure is the only way the worker ends and its notice is the failure.
	res := c.mustStart(t, protocol.WorkerStartRequest{Model: "claude-opus-5-5", Project: "shop", Harness: "claude", Task: "FAKE:ignore test task"})
	rec := readClaude(t, repo)

	projectsDir := filepath.Join(filepath.Dir(c.sessions), "projects")
	errLine := `{"type":"assistant","message":{"role":"assistant","model":"<synthetic>","content":[{"type":"text","text":"HTTP 429: Selected model is at capacity. Please try a different model."}]}}`

	var wk *worker
	c.d.wmu.Lock()
	for _, w := range c.d.workers {
		if w.sessionID == res.SessionID {
			wk = w
			break
		}
	}
	c.d.wmu.Unlock()
	if wk == nil {
		t.Fatal("worker not found")
	}

	writeClaudeTranscriptLine(t, projectsDir, rec.Session, wk.path, errLine)

	c.d.checkWorkers(context.Background())

	c.workerStopped(t, g, protocol.StopModelFails)

	n := c.waitNotice(t, 1)[0]
	if !strings.Contains(n.Body, "status: failed") {
		t.Errorf("notice lacks status: failed:\n%s", n.Body)
	}
	if !strings.Contains(n.Body, "Selected model is at capacity") {
		t.Errorf("notice lacks error text:\n%s", n.Body)
	}

	c.d.wmu.Lock()
	failureText := wk.failure
	c.d.wmu.Unlock()

	outcome := protocol.WorkerOutcomeDetail(protocol.StopModelFails, protocol.StatusFailed, nil, failureText)
	if outcome != protocol.OutcomeInfra {
		t.Errorf("outcome = %q, want %q", outcome, protocol.OutcomeInfra)
	}
}

// The bug of 2026-11-04: a Claude worker hands back and ends, and its caller
// sends it a request. The daemon resumes the session, and Claude Code closes
// the turn the stop cut short with a "No response requested." placeholder
// (model <synthetic>, isApiErrorMessage false). The failure check read that
// placeholder as a failed run and stopped the worker before it did the work:
// "status: failed / note: the worker run failed: No response requested.".
// The follow-up must run, and its hand-back must be reported as done.
func TestClaudeWorkerFollowUpAfterHandbackIsNotAFailure(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	set := shortSet
	set.Idle = time.Hour
	c := startClaudeNode(t, g, set)
	repo := newRepo(t, "shop")
	c.register(t, repo)
	defer func() {
		if t.Failed() {
			t.Logf("notices: %+v", c.notices(t))
			t.Logf("daemon log: %v", c.logs.lines())
		}
	}()

	res := c.mustStart(t, protocol.WorkerStartRequest{Model: "sonnet", Project: "shop", Harness: "claude", Task: "FAKE:handback first"})
	rec := readClaude(t, repo)
	c.waitNotice(t, 1)
	eventually(t, "the worker ended after its hand-back", func() bool { return c.d.workerCount() == 0 && !alive(rec.Pid) })

	// The follow-up: the worker reads it and works (it does not hand back yet).
	c.cli.mustCall("owner_send", protocol.OwnerSendRequest{To: res.SessionID, Body: "FAKE:ignore add the clean-tree check"})
	eventually(t, "the follow-up reached the resumed session", func() bool { return len(c.adapter.deliveredTo(res.SessionID)) >= 2 })
	wk := c.d.workerBySession(res.SessionID)
	if wk == nil {
		t.Fatalf("the resumed worker was ended: %v", c.logs.lines("worker.stopped"))
	}
	rec2 := readClaude(t, repo)
	if argValue(rec2.Args, "--resume") != rec.Session {
		t.Fatalf("the follow-up did not resume session %s: %+v", rec.Session, rec2)
	}
	// The follow-up reaches the worker as a request, never as a reply: the
	// reply frame tells the model that no answer is needed.
	if all := c.adapter.deliveredTo(res.SessionID); all[len(all)-1].Type == protocol.TypeReply || strings.Contains(all[len(all)-1].Body, "No answer is needed") {
		t.Fatalf("the follow-up was delivered as %q: %q", all[len(all)-1].Type, all[len(all)-1].Body)
	}

	// The placeholder of the resume is in the transcript. The run goes on.
	for i := 0; i < 3; i++ {
		c.d.checkWorkers(context.Background())
	}
	if c.d.workerBySession(res.SessionID) == nil {
		t.Fatalf("the resumed worker was ended: %v", c.logs.lines("worker.stopped"))
	}
	c.d.wmu.Lock()
	failure := wk.failure
	c.d.wmu.Unlock()
	if failure != "" {
		t.Fatalf("the resumed worker has a failure: %q", failure)
	}

	// The worker finishes the follow-up and hands back: a normal report.
	c.cli.mustCall("owner_send", protocol.OwnerSendRequest{To: res.SessionID, Body: "FAKE:handback done"})
	ns := c.waitNotice(t, 2)
	n := ns[len(ns)-1]
	if !strings.Contains(n.Body, "status: done") || strings.Contains(n.Body, "No response requested") {
		t.Fatalf("the follow-up's notice:\n%s", n.Body)
	}
}

// Claude Code marks a real API error with isApiErrorMessage true. Its
// placeholder for a turn with no answer is synthetic too, but is no error, and
// neither is a short final reply of the model.
func TestClaudeTranscriptPlaceholderIsNotAFailure(t *testing.T) {
	dir := t.TempDir()
	for name, line := range map[string]string{
		"placeholder":      fakeClaudeNoResponseLine,
		"short reply":      `{"type":"assistant","isSidechain":false,"message":{"role":"assistant","model":"claude-sonnet-5-5","stop_reason":"end_turn","content":[{"type":"text","text":"No response requested."}]},"isApiErrorMessage":false}`,
		"short reply done": `{"type":"assistant","message":{"role":"assistant","model":"claude-sonnet-5-5","stop_reason":"end_turn","content":[{"type":"text","text":"Done."}]}}`,
	} {
		p := filepath.Join(dir, strings.ReplaceAll(name, " ", "-")+".jsonl")
		if err := os.WriteFile(p, []byte(line+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if msg, provider, _ := claudeTranscriptFailureAt(p, 0); msg != "" || provider {
			t.Errorf("%s: read as a failure: %q provider=%v", name, msg, provider)
		}
	}
	// Real API errors, in the shape of real transcripts, still fail the run.
	for name, line := range map[string]string{
		"session limit": `{"type":"assistant","error":"rate_limit","isApiErrorMessage":true,"message":{"role":"assistant","model":"<synthetic>","content":[{"type":"text","text":"You've hit your session limit · resets 8am (UTC)"}]}}`,
		"connection":    `{"type":"assistant","error":"unknown","isApiErrorMessage":true,"message":{"role":"assistant","model":"<synthetic>","content":[{"type":"text","text":"API Error: Connection lost mid-response. The response above may be incomplete."}]}}`,
	} {
		p := filepath.Join(dir, strings.ReplaceAll(name, " ", "-")+".jsonl")
		if err := os.WriteFile(p, []byte(line+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if msg, _, _ := claudeTranscriptFailureAt(p, 0); msg == "" {
			t.Errorf("%s: not read as a failure", name)
		}
	}
}

func TestClaudeTranscriptLastAssistant(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	lines := []string{
		`{"type":"user","message":{"role":"user","content":"do it"}}`,
		`{"type":"assistant","message":{"model":"claude-haiku-5-5","content":[{"type":"text","text":"first answer"}]}}`,
		`{"type":"assistant","isSidechain":true,"message":{"model":"claude-haiku-5-5","content":[{"type":"text","text":"sidechain text"}]}}`,
		`{"type":"assistant","message":{"model":"<synthetic>","content":[{"type":"text","text":"No response requested."}]}}`,
		`{"type":"assistant","message":{"model":"claude-haiku-5-5","content":[{"type":"tool_use","name":"Bash"}]}}`,
		`{"type":"assistant","message":{"model":"claude-haiku-5-5","content":[{"type":"text","text":"second"},{"type":"tool_use","name":"Bash"},{"type":"text","text":"answer"}]}}`,
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, want := claudeTranscriptLastAssistant(p), "second\nanswer"; got != want {
		t.Fatalf("last assistant = %q, want %q", got, want)
	}
	if got := claudeTranscriptLastAssistant(filepath.Join(t.TempDir(), "missing.jsonl")); got != "" {
		t.Fatalf("missing transcript = %q, want empty", got)
	}
}

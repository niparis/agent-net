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

	res := c.mustStart(t, protocol.WorkerStartRequest{Model: "claude-opus-5-5:xhigh", Project: "shop", Harness: "claude", Task: "test task"})
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

	res := c.mustStart(t, protocol.WorkerStartRequest{Model: "claude-opus-5-5:xhigh", Project: "shop", Harness: "claude", Task: "test task"})
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

	res := c.mustStart(t, protocol.WorkerStartRequest{Model: "claude-opus-5-5", Project: "shop", Harness: "claude", Task: "test task"})
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

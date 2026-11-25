package daemon

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/claudeframes"
	"github.com/niparis/agent-net/internal/protocol"
)

// transcriptPath is the file Claude Code writes for the fake peer session.
func (p *claudePeer) transcriptPath() string {
	return filepath.Join(filepath.Dir(p.sessions), "projects", "-work-claude", "claude-peer.jsonl")
}

func (p *claudePeer) appendTranscript(t *testing.T, entry map[string]any) {
	t.Helper()
	path := p.transcriptPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		t.Fatal(err)
	}
}

// frameIn reads the next inbox frame and returns its content and reply socket.
func (p *claudePeer) frameIn(t *testing.T) (content, sock string) {
	t.Helper()
	var f struct {
		From    string `json:"from"`
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(p.nextFrameLine()), &f); err != nil {
		t.Fatal(err)
	}
	return f.Message.Content, strings.TrimPrefix(f.From, "uds:")
}

// holdEntry is the transcript entry Claude Code writes when it holds a frame.
func holdEntry(uuid, sock string, d claudeframes.Description, at string) map[string]any {
	return map[string]any{
		"type": "system", "subtype": "informational", "uuid": uuid, "timestamp": at,
		"content": "Held peer message \u2014 from uds:" + sock + " (peer claims name: bob); preview: \u00ab" + d.Preview +
			"\u00bb \u2026[" + itoa(d.Lines) + " lines, " + itoa(d.Chars) + " chars total \u2014 expand to review before approving] \u2014 not delivered to Claude (1 held). " +
			"The sending session's permission mode class doesn't match this session's. Review it below, or set \"crossSessionInbound\" to \"accept\".",
	}
}

// described is the description a frame has as Claude Code reports it.
func described(t *testing.T, content string) claudeframes.Description {
	t.Helper()
	d, ok := claudeframes.Describe(content)
	if !ok {
		t.Fatalf("frame has no wrapper: %s", content)
	}
	return d
}

// A Claude session that held a message reports it as held, not delivered: the
// daemon logs it, the gateway keeps it out of the reminder chase, and the
// sender sees the state and the action. The release writes the exact frame
// back, which makes it delivered.
func TestClaudeHeldMessageIsReportedAndReleased(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	p := newClaudePeer(t)
	if err := os.MkdirAll(filepath.Dir(p.transcriptPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.transcriptPath(), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	logs := &logBuf{}
	n := startNode(t, g, nodeOpts{cmdline: "pi", detect: fakeInventory, interval: time.Hour, logger: log.New(logs, "", 0), cfg: func(c *Config) {
		c.Adapters = []Adapter{p.adapter()}
	}})
	var claudeID string
	eventually(t, "Claude session registered", func() bool {
		claudeID = n.d.SessionID("claude", "claude-peer")
		return claudeID != ""
	})
	sender, _ := helloPi(t, n, "bob", "/work/bob", true)
	body := "please rotate the backup key"
	req := sendResult(t, sender.mustCall("send", map[string]any{"to": claudeID, "body": body}))
	content, sock := p.frameIn(t)
	eventually(t, "the message is delivered", func() bool { return messageState(t, sender, req.ID) == protocol.StateDelivered })

	p.appendTranscript(t, holdEntry("held-1", sock, described(t, content), time.Now().UTC().Format(time.RFC3339Nano)))
	eventually(t, "the hold reaches the gateway", func() bool { return messageState(t, sender, req.ID) == protocol.StateHeldBySession })
	got, _, err := g.gw.Store().Message(req.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"held by Claude Code", "approve the held message", "crossSessionInbound", "may have expired"} {
		if !strings.Contains(got.DeliveryDetail, want) {
			t.Fatalf("detail %q missing %q", got.DeliveryDetail, want)
		}
	}
	if lines := logs.lines("session.delivery_held msg=" + req.ID); len(lines) != 1 {
		t.Fatalf("hold log lines = %d, want 1:\n%s", len(lines), logs.String())
	}
	// The sender reads the same state and the action.
	raw, err := sender.call("messages", map[string]any{"session_id": claudeID})
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		Messages []protocol.Message `json:"messages"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range page.Messages {
		if m.ID == req.ID {
			found = m.State == protocol.StateHeldBySession && strings.Contains(m.DeliveryDetail, "approve the held message")
		}
	}
	if !found {
		t.Fatalf("sender view of %s: %+v", req.ID, page.Messages)
	}

	// The owner approves it in the session: Claude Code writes the frame as a
	// user entry, and the daemon reports the release.
	p.appendTranscript(t, map[string]any{
		"type": "user", "uuid": "released-1", "timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"message": map[string]any{"role": "user", "content": "Another Claude session sent a message:\n" + content},
	})
	eventually(t, "the release reaches the gateway", func() bool { return messageState(t, sender, req.ID) == protocol.StateDelivered })
	got, _, _ = g.gw.Store().Message(req.ID)
	if got.DeliveryDetail != "released in Claude Code" {
		t.Fatalf("released detail = %q", got.DeliveryDetail)
	}
	if lines := logs.lines("session.delivery_released msg=" + req.ID); len(lines) != 1 {
		t.Fatalf("release log lines = %d, want 1:\n%s", len(lines), logs.String())
	}
}

// A daemon restart reads the transcript tail again. An old hold notice must
// not mark a message that arrived later; the notice was already applied.
func TestClaudeHeldNoticeIsAppliedOnceAcrossARestart(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	p := newClaudePeer(t)
	if err := os.MkdirAll(filepath.Dir(p.transcriptPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.transcriptPath(), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	logs := &logBuf{}
	n := startNode(t, g, nodeOpts{cmdline: "pi", detect: fakeInventory, interval: time.Hour, logger: log.New(logs, "", 0), cfg: func(c *Config) {
		c.Adapters = []Adapter{p.adapter()}
	}})
	var claudeID string
	eventually(t, "Claude session registered", func() bool {
		claudeID = n.d.SessionID("claude", "claude-peer")
		return claudeID != ""
	})
	sender, _ := helloPi(t, n, "bob", "/work/bob", true)
	held := sendResult(t, sender.mustCall("send", map[string]any{"to": claudeID, "body": "the held body"}))
	heldContent, sock := p.frameIn(t)
	eventually(t, "the held message is delivered", func() bool { return messageState(t, sender, held.ID) == protocol.StateDelivered })
	p.appendTranscript(t, holdEntry("held-1", sock, described(t, heldContent), time.Now().UTC().Format(time.RFC3339Nano)))
	eventually(t, "the hold is applied", func() bool { return messageState(t, sender, held.ID) == protocol.StateHeldBySession })

	later := sendResult(t, sender.mustCall("send", map[string]any{"to": claudeID, "body": "the later body"}))
	_, sockLater := p.frameIn(t)
	eventually(t, "the later message is delivered", func() bool { return messageState(t, sender, later.ID) == protocol.StateDelivered })

	n.cfg.Adapters = []Adapter{p.adapter()} // a new process has no memory of the scan
	n.restart()
	eventually(t, "Claude session registered again", func() bool { return n.d.SessionID("claude", "claude-peer") == claudeID })

	// A notice that matches nothing makes the scan past the old notice visible.
	p.appendTranscript(t, holdEntry("noise-1", sockLater,
		claudeframes.Description{Preview: "no message starts like this", Lines: 9, Chars: 999},
		time.Now().UTC().Format(time.RFC3339Nano)))
	eventually(t, "the restarted scan reads the transcript", func() bool {
		return len(logs.lines("matches no single unheld delivery", "no message starts like this")) == 1
	})
	laterGot, _, err := g.gw.Store().Message(later.ID)
	if err != nil {
		t.Fatal(err)
	}
	if laterGot.State != protocol.StateDelivered {
		t.Fatalf("the old hold notice marked the later message: %s", laterGot.State)
	}
	heldGot, _, err := g.gw.Store().Message(held.ID)
	if err != nil {
		t.Fatal(err)
	}
	if heldGot.State != protocol.StateHeldBySession {
		t.Fatalf("the held message changed on restart: %s", heldGot.State)
	}
}

// A notice written before a message was delivered cannot describe it.
func TestClaudeHeldNoticeDoesNotMatchAMessageDeliveredAfterIt(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	p := newClaudePeer(t)
	if err := os.MkdirAll(filepath.Dir(p.transcriptPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.transcriptPath(), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	logs := &logBuf{}
	n := startNode(t, g, nodeOpts{cmdline: "pi", detect: fakeInventory, interval: time.Hour, logger: log.New(logs, "", 0), cfg: func(c *Config) {
		c.Adapters = []Adapter{p.adapter()}
	}})
	var claudeID string
	eventually(t, "Claude session registered", func() bool {
		claudeID = n.d.SessionID("claude", "claude-peer")
		return claudeID != ""
	})
	sender, _ := helloPi(t, n, "bob", "/work/bob", true)
	req := sendResult(t, sender.mustCall("send", map[string]any{"to": claudeID, "body": "arrived after the notice"}))
	content, sock := p.frameIn(t)
	eventually(t, "the message is delivered", func() bool { return messageState(t, sender, req.ID) == protocol.StateDelivered })

	p.appendTranscript(t, holdEntry("old-1", sock, described(t, content), time.Now().Add(-5*time.Minute).UTC().Format(time.RFC3339Nano)))
	eventually(t, "the old notice is refused", func() bool {
		return len(logs.lines("matches no single unheld delivery", "arrived after the notice")) == 1
	})
	if state := messageState(t, sender, req.ID); state != protocol.StateDelivered {
		t.Fatalf("a notice older than the message marked it: %s", state)
	}
}

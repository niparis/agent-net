package daemon

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// A Claude session that held a message reports it as held, not delivered: the
// daemon logs it, the gateway keeps it out of the reminder chase, and the
// sender sees the state and the action. A later release makes it delivered.
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
	sender, senderID := helloPi(t, n, "bob", "/work/bob", true)
	req := sendResult(t, sender.mustCall("send", map[string]any{"to": claudeID, "body": "please rotate the backup key"}))
	addr := p.nextFrame()
	eventually(t, "the message is delivered", func() bool { return messageState(t, sender, req.ID) == protocol.StateDelivered })

	p.appendTranscript(t, map[string]any{
		"type": "system", "subtype": "informational", "timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"content": "Held peer message \u2014 from uds:" + addr + " (peer claims name: bob); preview: \u00abplease rotate the backup key\u00bb \u2026[1 line, 27 chars \u2014 expand to review before approving] \u2014 not delivered to Claude (1 held). The sending session's permission mode class doesn't match this session's. Review it below, or set \"crossSessionInbound\" to \"accept\".",
	})
	eventually(t, "the hold reaches the gateway", func() bool { return messageState(t, sender, req.ID) == protocol.StateHeldBySession })
	got, _, err := g.gw.Store().Message(req.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"held by Claude Code", "approve the held message", "crossSessionInbound"} {
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

	// The owner approves it in the session: Claude Code writes the peer frame
	// as a user entry, and the daemon reports the release.
	p.appendTranscript(t, map[string]any{
		"type": "user",
		"message": map[string]any{"role": "user", "content": "Another Claude session sent a message:\n" +
			"<cross-session-message from=\"uds:" + addr + "\" from-name=\"bob\" from-mode=\"prompting\">\n" +
			"please rotate the backup key\n\n[agent-net] message " + req.ID + " from bob (pi, session " + senderID + ").\n</cross-session-message>"},
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

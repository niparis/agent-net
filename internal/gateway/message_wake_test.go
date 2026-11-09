package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

type wakeProbe struct {
	s     *Store
	g     *Gateway
	node  string
	from  string
	wakes chan map[string]string
	mu    sync.Mutex
	// refusal is the reason the machine gives for a wake, or empty to accept.
	refusal string
	// gate, when set, holds the machine's answer to a wake until it is closed.
	gate chan struct{}
}

func (p *wakeProbe) setRefusal(reason string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.refusal = reason
}

// wakeRefusal is the machine's answer to a wake request.
func (p *wakeProbe) wakeRefusal() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.refusal
}

// holdWakes makes the machine's answer to each wake wait. The returned function
// lets the answers through. It is safe to call more than once.
func (p *wakeProbe) holdWakes() func() {
	gate := make(chan struct{})
	p.mu.Lock()
	p.gate = gate
	p.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { close(gate) }) }
}

// wakeGate is the gate that holds the machine's answer, or nil.
func (p *wakeProbe) wakeGate() chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.gate
}

// newWakeProbe has one machine with a fake daemon that records wake frames,
// and a second machine with the sender.
func newWakeProbe(t *testing.T) *wakeProbe {
	t.Helper()
	return newWakeProbeRefusing(t, "")
}

// newWakeProbeRefusing is a wake probe whose machine refuses every wake with
// the given reason. An empty reason accepts every wake.
func newWakeProbeRefusing(t *testing.T, refusal string) *wakeProbe {
	t.Helper()
	s, _ := newTestStore(t)
	p := &wakeProbe{s: s, g: &Gateway{store: s, logger: log.New(io.Discard, "", 0), conns: map[string]*nodeConn{}}, wakes: make(chan map[string]string, 8), refusal: refusal}
	var err error
	if p.node, _, err = s.CreateNode("ha", "linux"); err != nil {
		t.Fatal(err)
	}
	other, _, err := s.CreateNode("hb", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if p.from, err = s.UpsertSession(other, protocol.SessionInfo{Harness: "claude", NativeID: "sender", Name: "sender"}); err != nil {
		t.Fatal(err)
	}
	fakeOwnerNode(t, p.g, p.node, func(h protocol.Header, raw []byte) error {
		if h.Type == protocol.FrameSessionWake {
			var m map[string]string
			if err := json.Unmarshal(raw, &m); err != nil {
				return err
			}
			p.wakes <- m
			if gate := p.wakeGate(); gate != nil {
				<-gate
			}
			if reason := p.wakeRefusal(); reason != "" {
				return errors.New(reason)
			}
		}
		return nil
	})
	return p
}

// offline adds an offline owner session on the wake machine, with its autonomy.
func (p *wakeProbe) offline(t *testing.T, harness, mode, autonomy string) string {
	t.Helper()
	id, err := p.s.UpsertSession(p.node, protocol.SessionInfo{Harness: harness, NativeID: harness + "-native", Cwd: "/w/proj", Name: harness, Mode: mode})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.s.SetSessionOffline(p.node, id); err != nil {
		t.Fatal(err)
	}
	setAutonomy(t, p.s, id, autonomy)
	return id
}

func (p *wakeProbe) message(t *testing.T, to string) protocol.Message {
	t.Helper()
	m, _ := p.sendMessage(t, to)
	return m
}

// sendMessage sends a message from the probe's sender and dispatches it. It also
// reports whether dispatch started a wake, which the receipt note uses.
func (p *wakeProbe) sendMessage(t *testing.T, to string) (protocol.Message, bool) {
	t.Helper()
	m, _, err := send(p.s, p.from, to, "wake-"+to, "")
	if err != nil {
		t.Fatal(err)
	}
	started := p.g.dispatch(context.Background(), m)
	return m, started
}

func (p *wakeProbe) stepMessage(t *testing.T, to string) protocol.Message {
	t.Helper()
	msgID := protocol.NewID("msg")
	changeID := protocol.NewID("ch")
	idemKey := "step_" + msgID
	now := p.s.now()
	expires := now.Add(time.Hour)
	if _, err := p.s.db.Exec(`INSERT INTO delivery_changes(id, project, machine, branch, head_commit, revision, origin, state, created_at)
		VALUES(?, 'proj', 'node1', 'feature', 'head1', 1, '{}', 'open', ?)`, changeID, now.Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := p.s.db.Exec(`INSERT INTO delivery_step_tasks(change_id, revision, step, for_step, after_merge, owner_node, claimed_by, completed, created_at)
		VALUES(?, 1, 'test', '', 0, 'node1', '', 0, ?)`, changeID, now.Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := p.s.db.Exec(`INSERT INTO messages(id, conversation_id, sender, recipient, type, body, hop, idem_key, created_at, expires_at, state)
		VALUES(?, 'conv', 'agent-net', ?, 'notification', 'step task', 0, ?, ?, ?, 'stored')`,
		msgID, to, idemKey, now.UnixNano(), expires.UnixNano()); err != nil {
		t.Fatal(err)
	}
	if _, err := p.s.db.Exec(`INSERT INTO delivery_step_messages(change_id, revision, step, for_step, after_merge, session_id, message_id)
		VALUES(?, 1, 'test', '', 0, ?, ?)`, changeID, to, msgID); err != nil {
		t.Fatal(err)
	}
	m, ok, err := p.s.Message(msgID)
	if err != nil || !ok {
		t.Fatalf("step message: ok=%v err=%v", ok, err)
	}
	p.g.dispatch(context.Background(), m)
	return m
}

func (p *wakeProbe) expectWake(t *testing.T, session string) map[string]string {
	t.Helper()
	select {
	case w := <-p.wakes:
		if w["session_id"] != session {
			t.Fatalf("woke %q, want %q", w["session_id"], session)
		}
		return w
	case <-time.After(2 * time.Second):
		t.Fatal("no wake request")
	}
	return nil
}

func (p *wakeProbe) expectNoWake(t *testing.T) {
	t.Helper()
	select {
	case w := <-p.wakes:
		t.Fatalf("unexpected wake request: %v", w)
	case <-time.After(150 * time.Millisecond):
	}
}

func setWakeOnMessage(t *testing.T, s *Store, session, value string) {
	t.Helper()
	if err := s.SetNodeSetting("proj", protocol.SettingWakeOnMessage, value, "owner"); err != nil {
		t.Fatal(err)
	}
}

// With autonomy off and wake_on_message unset (the default), a message to an
// offline interactive Claude session wakes it.
func TestMessageWakesInteractiveClaudeWithAutonomyOffByDefault(t *testing.T) {
	p := newWakeProbe(t)
	id := p.offline(t, "claude", protocol.ModeForeground, protocol.AutonomyOff)
	p.message(t, id)
	p.expectWake(t, id)
}

// With wake_on_message off and autonomy off, an interactive Claude session stays asleep
// and the receipt note says wake is off for this agent.
func TestMessageDoesNotWakeInteractiveClaudeWhenWakeOnMessageOff(t *testing.T) {
	p := newWakeProbe(t)
	id := p.offline(t, "claude", protocol.ModeForeground, protocol.AutonomyOff)
	setWakeOnMessage(t, p.s, id, protocol.WakeOnMessageOff)
	m, started := p.sendMessage(t, id)
	p.expectNoWake(t)
	if note := p.g.wakeReceiptNote(m, "", started); !strings.Contains(note, "wake is off for this agent") {
		t.Fatalf("receipt note = %q, want 'wake is off for this agent'", note)
	}
}

// With wake_on_message off but autonomy wake, an interactive Claude session is woken
// for backward compatibility.
func TestMessageWakesInteractiveClaudeWithWakeOnMessageOffAndAutonomyWake(t *testing.T) {
	p := newWakeProbe(t)
	id := p.offline(t, "claude", protocol.ModeForeground, protocol.AutonomyWake)
	setWakeOnMessage(t, p.s, id, protocol.WakeOnMessageOff)
	p.message(t, id)
	w := p.expectWake(t, id)
	if w["autonomy"] != protocol.AutonomyWake || w["token"] == "" {
		t.Fatalf("wake frame = %v", w)
	}
}

// With autonomy wake, a message to an offline interactive Claude session asks
// its machine to resume it.
func TestMessageWakesInteractiveClaudeWithAutonomyWake(t *testing.T) {
	p := newWakeProbe(t)
	id := p.offline(t, "claude", protocol.ModeForeground, protocol.AutonomyWake)
	p.message(t, id)
	w := p.expectWake(t, id)
	if w["token"] == "" {
		t.Fatalf("wake frame = %v", w)
	}
	p.expectNoWake(t)
}

// The Claude background wake does not depend on autonomy.
func TestMessageWakesBackgroundClaudeWithAutonomyOff(t *testing.T) {
	p := newWakeProbe(t)
	id := p.offline(t, "claude", protocol.ModeBackground, protocol.AutonomyOff)
	p.message(t, id)
	w := p.expectWake(t, id)
	if w["token"] == "" {
		t.Fatalf("wake frame = %v", w)
	}
}

func TestMessageWakesCodexAndOpenCodeByDefault(t *testing.T) {
	for _, harness := range []string{"codex", "opencode"} {
		t.Run(harness, func(t *testing.T) {
			p := newWakeProbe(t)
			id := p.offline(t, harness, "", protocol.AutonomyOff)
			p.message(t, id)
			p.expectWake(t, id)
			p.expectNoWake(t)

			off := newWakeProbe(t)
			id = off.offline(t, harness, "", protocol.AutonomyOff)
			setWakeOnMessage(t, off.s, id, protocol.WakeOnMessageOff)
			m, started := off.sendMessage(t, id)
			off.expectNoWake(t)
			if note := off.g.wakeReceiptNote(m, "", started); !strings.Contains(note, "wake is off for this agent") {
				t.Fatalf("receipt note = %q, want 'wake is off for this agent'", note)
			}
		})
	}
}

// A second message inside ten minutes does not ask again.
func TestMessageWakeKeepsTheTenMinuteLimitForAutonomy(t *testing.T) {
	p := newWakeProbe(t)
	id := p.offline(t, "codex", "", protocol.AutonomyWake)
	p.message(t, id)
	p.expectWake(t, id)
	m, _, err := send(p.s, p.from, id, "second", "")
	if err != nil {
		t.Fatal(err)
	}
	p.g.dispatch(context.Background(), m)
	p.expectNoWake(t)
}

// Pi cannot open an existing session. The message stays queued, and the
// sender's routing note says so.
func TestMessageToOfflinePiIsNotWoken(t *testing.T) {
	p := newWakeProbe(t)
	id := p.offline(t, "pi", "", protocol.AutonomyWake)
	m, started := p.sendMessage(t, id)
	p.expectNoWake(t)
	if m.State != protocol.StateStored {
		t.Fatalf("state = %s", m.State)
	}
	if note := p.g.wakeReceiptNote(m, "Redirected to x.", started); note != "Redirected to x. Pi sessions are not woken by messages" {
		t.Fatalf("note = %q", note)
	}
	if note := p.g.wakeReceiptNote(m, "", started); note != "Pi sessions are not woken by messages" {
		t.Fatalf("note = %q", note)
	}

	off := newWakeProbe(t)
	id = off.offline(t, "pi", "", protocol.AutonomyOff)
	m, started = off.sendMessage(t, id)
	if note := off.g.wakeReceiptNote(m, "", started); note != "" {
		t.Fatalf("autonomy off note = %q", note)
	}
	// Codex is woken by the message, so its receipt says the wake is under way. It
	// never gets the Pi line.
	cx := newWakeProbe(t)
	id = cx.offline(t, "codex", "", protocol.AutonomyWake)
	cm, cstarted := cx.sendMessage(t, id)
	if note := cx.g.wakeReceiptNote(cm, "", cstarted); note != "queued; proj is offline, waking it" {
		t.Fatalf("codex note = %q", note)
	}
}

// A current step message to an offline interactive Claude session does not wake it
// when wake_on_message is off and autonomy is off.
func TestStepMessageDoesNotWakeInteractiveClaudeWhenWakeOnMessageOff(t *testing.T) {
	p := newWakeProbe(t)
	id := p.offline(t, "claude", protocol.ModeForeground, protocol.AutonomyOff)
	setWakeOnMessage(t, p.s, id, protocol.WakeOnMessageOff)
	p.stepMessage(t, id)
	p.expectNoWake(t)
}

// A current step message wakes an offline interactive Claude session by default
// (wake_on_message unset/default-on).
func TestStepMessageWakesInteractiveClaudeByDefault(t *testing.T) {
	p := newWakeProbe(t)
	id := p.offline(t, "claude", protocol.ModeForeground, protocol.AutonomyOff)
	p.stepMessage(t, id)
	p.expectWake(t, id)
}

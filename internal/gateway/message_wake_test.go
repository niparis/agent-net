package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
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
	m, _, err := send(p.s, p.from, to, "wake-"+to, "")
	if err != nil {
		t.Fatal(err)
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

// With autonomy wake, a message to an offline interactive Claude session asks
// its machine to resume it. With autonomy off nothing is asked.
func TestMessageWakesInteractiveClaudeOnlyWithAutonomy(t *testing.T) {
	p := newWakeProbe(t)
	id := p.offline(t, "claude", protocol.ModeForeground, protocol.AutonomyWake)
	p.message(t, id)
	w := p.expectWake(t, id)
	if w["autonomy"] != protocol.AutonomyWake || w["token"] == "" {
		t.Fatalf("wake frame = %v", w)
	}
	p.expectNoWake(t)

	off := newWakeProbe(t)
	id = off.offline(t, "claude", protocol.ModeForeground, protocol.AutonomyOff)
	off.message(t, id)
	off.expectNoWake(t)
}

// The Claude background wake does not depend on autonomy.
func TestMessageWakesBackgroundClaudeWithAutonomyOff(t *testing.T) {
	p := newWakeProbe(t)
	id := p.offline(t, "claude", protocol.ModeBackground, protocol.AutonomyOff)
	p.message(t, id)
	w := p.expectWake(t, id)
	if w["autonomy"] != "" {
		t.Fatalf("autonomy off must not ask for more: %v", w)
	}
}

func TestMessageWakesCodexAndOpenCodeOnlyWithAutonomy(t *testing.T) {
	for _, harness := range []string{"codex", "opencode"} {
		t.Run(harness, func(t *testing.T) {
			p := newWakeProbe(t)
			id := p.offline(t, harness, "", protocol.AutonomyWake)
			p.message(t, id)
			p.expectWake(t, id)
			p.expectNoWake(t)

			off := newWakeProbe(t)
			id = off.offline(t, harness, "", protocol.AutonomyOff)
			off.message(t, id)
			off.expectNoWake(t)
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
	m := p.message(t, id)
	p.expectNoWake(t)
	if m.State != protocol.StateStored {
		t.Fatalf("state = %s", m.State)
	}
	if note := p.g.wakeReceiptNote(m, "Redirected to x."); note != "Redirected to x. Pi sessions are not woken by messages" {
		t.Fatalf("note = %q", note)
	}
	if note := p.g.wakeReceiptNote(m, ""); note != "Pi sessions are not woken by messages" {
		t.Fatalf("note = %q", note)
	}

	off := newWakeProbe(t)
	id = off.offline(t, "pi", "", protocol.AutonomyOff)
	m = off.message(t, id)
	if note := off.g.wakeReceiptNote(m, ""); note != "" {
		t.Fatalf("autonomy off note = %q", note)
	}
	// Other offline harnesses and online sessions get no such line.
	cx := newWakeProbe(t)
	id = cx.offline(t, "codex", "", protocol.AutonomyWake)
	if note := cx.g.wakeReceiptNote(cx.message(t, id), ""); note != "" {
		t.Fatalf("codex note = %q", note)
	}
}

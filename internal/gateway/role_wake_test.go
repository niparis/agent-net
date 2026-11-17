package gateway

import (
	"encoding/json"
	"io"
	"log"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A role session is woken for a message whatever its agent's wake and
// autonomy settings say: the chief and the merger are what the other agents
// rely on, so a setting made for a coding session must not leave them asleep
// (decision 0171, phase 2). The wake frame carries the autonomy the machine
// needs to wake a Codex or OpenCode session.
func TestRoleSessionsAreWokenForAMessage(t *testing.T) {
	for _, harness := range []string{"claude", "codex", "opencode"} {
		for _, role := range []string{"chief", "merger"} {
			t.Run(role+" on "+harness, func(t *testing.T) {
				p := newWakeProbe(t)
				id := p.offline(t, harness, protocol.ModeForeground, protocol.AutonomyOff)
				setWakeOnMessage(t, p.s, id, protocol.WakeOnMessageOff)
				pinRole(t, p.s, role, p.node, id)
				m, started := p.sendMessage(t, id)
				w := p.expectWake(t, id)
				if w["autonomy"] != protocol.AutonomyWake {
					t.Fatalf("wake frame = %v, want autonomy wake so the machine wakes a %s session", w, harness)
				}
				if !started {
					t.Fatal("dispatch did not report a wake")
				}
				if note := p.g.wakeReceiptNote(m, "", started); note != "queued; proj is offline, waking it" {
					t.Fatalf("receipt note = %q, want that the session is being woken", note)
				}
			})
		}
	}
}

// The role rule does not widen to other sessions: an ordinary session with
// wake off stays asleep.
func TestOrdinarySessionWithWakeOffStaysAsleep(t *testing.T) {
	p := newWakeProbe(t)
	id := p.offline(t, "codex", protocol.ModeForeground, protocol.AutonomyOff)
	setWakeOnMessage(t, p.s, id, protocol.WakeOnMessageOff)
	p.message(t, id)
	p.expectNoWake(t)
}

// A ready step wakes an offline Codex or OpenCode merger with autonomy off.
// Before phase 2 only Claude sessions were looked up, and a Codex merger
// waited for ever.
func TestReadyStepWakesOfflineCodexAndOpenCodeMerger(t *testing.T) {
	for _, harness := range []string{"codex", "opencode"} {
		t.Run(harness, func(t *testing.T) {
			r := newDeliveryRig(t)
			r.g.logger = log.New(io.Discard, "", 0)
			r.g.conns = map[string]*nodeConn{}
			wakes := make(chan map[string]string, 10)
			fakeOwnerNode(t, r.g, r.a, func(h protocol.Header, raw []byte) error {
				if h.Type == protocol.FrameSessionWake {
					var m map[string]string
					if err := json.Unmarshal(raw, &m); err != nil {
						return err
					}
					wakes <- m
				}
				return nil
			})
			if _, err := r.s.db.Exec(`UPDATE sessions SET harness=? WHERE id=?`, harness, r.sonnet); err != nil {
				t.Fatal(err)
			}
			if err := r.s.SetMerger(protocol.MergerSetRequest{Project: "demo", NodeID: r.a, SessionID: r.sonnet}, "owner"); err != nil {
				t.Fatal(err)
			}
			if err := r.s.SetDeliveryOwner(protocol.DeliveryOwnerSetRequest{Project: "demo", Step: "test", Node: "@merger"}, "owner"); err != nil {
				t.Fatal(err)
			}
			if err := r.s.SetSessionOffline(r.a, r.sonnet); err != nil {
				t.Fatal(err)
			}
			change := r.submit(r.a, r.workerSubmit("feature/role-wake", commitA)).Change.Change.ID
			r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: change, StepRef: stepAccept, Result: protocol.StepResultPass, Commit: commitA})
			select {
			case w := <-wakes:
				if w["session_id"] != r.sonnet || w["autonomy"] != protocol.AutonomyWake {
					t.Fatalf("wake frame = %v, want session %s with autonomy wake", w, r.sonnet)
				}
			case <-time.After(2 * time.Second):
				t.Fatalf("the %s merger was not woken for its ready step", harness)
			}
		})
	}
}

// pinRole makes a session the chief or the project's merger.
func pinRole(t *testing.T, s *Store, role, node, session string) {
	t.Helper()
	switch role {
	case "chief":
		if err := s.SetChief(protocol.ChiefSetRequest{SessionID: session}, "owner"); err != nil {
			t.Fatal(err)
		}
	case "merger":
		if err := s.SetMerger(protocol.MergerSetRequest{Project: "proj", NodeID: node, SessionID: session}, "owner"); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unknown role %q", role)
	}
}

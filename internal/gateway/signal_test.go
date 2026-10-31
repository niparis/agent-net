package gateway

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestSignalPromptSendOutcomeAndRetention(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	if _, err := s.db.Exec(`UPDATE sessions SET repo_project=CASE id WHEN ? THEN 'sovl-clone' ELSE 'homelab' END WHERE id IN (?,?)`, a, a, b); err != nil {
		t.Fatal(err)
	}
	if err := s.RememberOwnerPrompt(a, "ask homelab: "+strings.Repeat("x", 400)); err != nil {
		t.Fatal(err)
	}
	m, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "please", Type: protocol.TypeRequest})
	if err != nil {
		t.Fatal(err)
	}
	reply, _, err := s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, Body: "done", ParentID: m.ID, ReplyOutcome: protocol.ReplyOutcome{Outcome: "reject", Reason: "already in main"}})
	if err != nil {
		t.Fatal(err)
	}
	signals, err := s.Signals()
	if err != nil || len(signals) != 1 {
		t.Fatalf("signals=%+v err=%v", signals, err)
	}
	v := signals[0]
	if v.From != "sovl-clone" || v.To != "homelab" || v.MessageID != m.ID || len([]rune(v.Prompt)) != 300 || v.Outcome != "reject" || v.Reason != "already in main" || v.ReplyID != reply.ID {
		t.Fatalf("signal: %+v", v)
	}
	c.t = c.t.Add(91 * 24 * time.Hour)
	if _, err = s.Prune(Retention{}); err != nil {
		t.Fatal(err)
	}
	signals, err = s.Signals()
	if err != nil || len(signals) != 0 {
		t.Fatalf("retention: %+v %v", signals, err)
	}
}

func TestSignalPromptExpiryAndRecipientMention(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	s.db.Exec(`UPDATE sessions SET repo_project='homelab' WHERE id=?`, b)
	if err := s.RememberOwnerPrompt(a, "unrelated"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "hello"}); err != nil {
		t.Fatal(err)
	}
	s.RememberOwnerPrompt(a, "ask homelab")
	c.t = c.t.Add(11 * time.Minute)
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "hello"}); err != nil {
		t.Fatal(err)
	}
	signals, err := s.Signals()
	if err != nil || len(signals) != 0 {
		t.Fatalf("signals=%+v err=%v", signals, err)
	}
}

func TestSignalOwnerReadGate(t *testing.T) {
	s, _ := newTestStore(t)
	a, _ := twoSessions(t, s)
	node := mustNode(t, s, a)
	g := &Gateway{store: s}
	nc := &nodeConn{id: node}
	if _, err := g.handle(context.Background(), nc, "signal.list", mustJSON(t, map[string]string{"owner": a})); err == nil {
		t.Fatal("agent could read signals")
	}
	if _, err := g.handle(context.Background(), nc, "signal.prompt", mustJSON(t, map[string]string{"session_id": "missing", "prompt": "hello"})); err == nil {
		t.Fatal("accepted another session")
	}
}

func TestSignalPromptNamesAnotherProject(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	if _, err := s.db.Exec(`UPDATE sessions SET repo_project='homelab' WHERE id=?`, b); err != nil {
		t.Fatal(err)
	}
	if err := s.AddArchitect("architect", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.RememberOwnerPrompt(a, "ask architect to arrange this"); err != nil {
		t.Fatal(err)
	}
	_, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "please"})
	if err != nil {
		t.Fatal(err)
	}
	signals, err := s.Signals()
	if err != nil || len(signals) != 0 {
		t.Fatalf("signals %+v: %v", signals, err)
	}
}

func TestSignalNotification(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	if err := s.RememberOwnerPrompt(a, "tell bob"); err != nil {
		t.Fatal(err)
	}
	m, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "notice", Type: protocol.TypeNotification})
	if err != nil {
		t.Fatal(err)
	}
	signals, err := s.Signals()
	if err != nil || len(signals) != 1 || signals[0].MessageID != m.ID {
		t.Fatalf("signals %+v: %v", signals, err)
	}
}

func TestSignalKeepsMultipleOutcomes(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	s.RememberOwnerPrompt(a, "ask bob")
	m, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "please"})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range []protocol.ReplyOutcome{{Outcome: "accept"}, {Outcome: "reject", Reason: "already in main"}} {
		if _, _, err = s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, ParentID: m.ID, Body: "reply", ReplyOutcome: o}); err != nil {
			t.Fatal(err)
		}
	}
	signals, err := s.Signals()
	if err != nil || len(signals) != 2 || signals[0].MessageID != m.ID || signals[1].MessageID != m.ID || signals[0].Outcome == signals[1].Outcome {
		t.Fatalf("signals %+v: %v", signals, err)
	}
}

func TestSignalOnlyFirstSend(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	if err := s.RememberOwnerPrompt(a, "ask BOB please"); err != nil {
		t.Fatal(err)
	}
	first, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "first"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "second"}); err != nil {
		t.Fatal(err)
	}
	signals, err := s.Signals()
	if err != nil || len(signals) != 1 || signals[0].MessageID != first.ID {
		t.Fatalf("signals %+v: %v", signals, err)
	}
}

func TestSignalUnmatchedFirstSendConsumesPrompt(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	if err := s.RememberOwnerPrompt(a, "ask somebody else"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "first"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE sessions SET name='somebody' WHERE id=?`, b); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "second"}); err != nil {
		t.Fatal(err)
	}
	signals, err := s.Signals()
	if err != nil || len(signals) != 0 {
		t.Fatalf("signals %+v: %v", signals, err)
	}
}

func TestSignalOwnerInstructionOnMessage(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	prompt := "ask bob " + strings.Repeat("é", 220)
	if err := s.RememberOwnerPrompt(a, prompt); err != nil {
		t.Fatal(err)
	}
	sent, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "work"})
	if err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.Message(sent.ID)
	if err != nil || !ok || got.OwnerInstruction != string([]rune(prompt)[:200]) {
		t.Fatalf("message %+v: %v", got, err)
	}
}

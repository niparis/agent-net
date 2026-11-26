package gateway

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func dueAdd(t *testing.T, s *Store, session string, args protocol.DueArgs) protocol.DueItem {
	t.Helper()
	args.Action = protocol.DueActionAdd
	res, err := s.SessionDue(session, args)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 1 {
		t.Fatalf("add answered %d items, want 1", len(res.Items))
	}
	return res.Items[0]
}

// tickDues runs the minute check once and returns the wake messages it made.
func tickDues(t *testing.T, s *Store) []protocol.Message {
	t.Helper()
	msgs, err := s.checkSessionDues()
	if err != nil {
		t.Fatal(err)
	}
	return msgs
}

// dueAsks returns the due items agent-net raised. They are its own items, so
// each is a log line, not a stored ask (decision 0196).
func dueAsks(logs *[]string) []followUpAsk {
	line := regexp.MustCompile(`^desk\.dropped kind \S+ from_kind \S+ session (\S+) ref (due:\S+) text (.*)\n$`)
	var out []followUpAsk
	for _, l := range *logs {
		if m := line.FindStringSubmatch(l); m != nil {
			out = append(out, followUpAsk{session: m[1], ref: m[2], text: m[3]})
		}
	}
	return out
}

func TestDueTimeItemWakesSessionAtDueNotBefore(t *testing.T) {
	s, c, _, a, _, _ := followUpRig(t)
	item := dueAdd(t, s, a, protocol.DueArgs{What: "Flip ch_1 after the lead's review", Due: "45m"})
	set := c.t

	c.set(set.Add(44 * time.Minute))
	if got := tickDues(t, s); len(got) != 0 {
		t.Fatalf("woke before the due time: %+v", got)
	}
	c.set(set.Add(46 * time.Minute))
	got := tickDues(t, s)
	if len(got) != 1 {
		t.Fatalf("got %d wakes at the due time, want 1: %+v", len(got), got)
	}
	if got[0].From != "agent-net" || got[0].To != a || !strings.Contains(got[0].Body, item.ID) {
		t.Errorf("wake = from %q to %q body %q, want agent-net to %s naming %s", got[0].From, got[0].To, got[0].Body, a, item.ID)
	}
	if again := tickDues(t, s); len(again) != 0 {
		t.Fatalf("woke the session twice: %+v", again)
	}
}

func TestDueAfterReplyWakesOnlyAfterTheReply(t *testing.T) {
	s, c, _, a, b, _ := followUpRig(t)
	req, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "Review ch_1", Type: protocol.TypeRequest})
	if err != nil {
		t.Fatal(err)
	}
	item := dueAdd(t, s, a, protocol.DueArgs{What: "Read bob's review", After: &protocol.DueAfter{Reply: req.ID}})

	c.advance(time.Hour)
	if got := tickDues(t, s); len(got) != 0 {
		t.Fatalf("woke before any reply: %+v", got)
	}
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, Body: "done", Type: protocol.TypeReply, ParentID: req.ID}); err != nil {
		t.Fatal(err)
	}
	got := tickDues(t, s)
	if len(got) != 1 || got[0].To != a || !strings.Contains(got[0].Body, item.ID) {
		t.Fatalf("after the reply: wakes %+v, want one naming %s", got, item.ID)
	}
}

func TestDueIdleAfterWakeRaisesOneChiefAsk(t *testing.T) {
	s, c, _, a, _, _ := followUpRig(t)
	logs := captureLog(s)
	item := dueAdd(t, s, a, protocol.DueArgs{What: "Flip ch_1", Due: "10m"})
	c.advance(11 * time.Minute)
	if got := tickDues(t, s); len(got) != 1 {
		t.Fatalf("got %d wakes, want 1", len(got))
	}
	woke := c.t

	c.set(woke.Add(14 * time.Minute))
	tickDues(t, s)
	if got := dueAsks(logs); len(got) != 0 {
		t.Fatalf("asked before 15 minutes without activity: %+v", got)
	}

	c.set(woke.Add(16 * time.Minute))
	tickDues(t, s)
	tickDues(t, s)
	got := dueAsks(logs)
	if len(got) != 1 {
		t.Fatalf("got %d asks, want exactly 1: %+v", len(got), got)
	}
	if got[0].session != a || got[0].ref != "due:"+item.ID {
		t.Errorf("ask = session %q ref %q", got[0].session, got[0].ref)
	}
	for _, want := range []string{item.ID, "Flip ch_1"} {
		if !strings.Contains(got[0].text, want) {
			t.Errorf("ask text %q lacks %q", got[0].text, want)
		}
	}
}

func TestDueSessionActivityAfterWakeRaisesNoAsk(t *testing.T) {
	s, c, _, a, b, _ := followUpRig(t)
	logs := captureLog(s)
	dueAdd(t, s, a, protocol.DueArgs{What: "Flip ch_1", Due: "10m"})
	c.advance(11 * time.Minute)
	tickDues(t, s)

	c.advance(2 * time.Minute)
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "Flipping ch_1 now", Type: protocol.TypeRequest}); err != nil {
		t.Fatal(err)
	}
	c.advance(30 * time.Minute)
	tickDues(t, s)
	tickDues(t, s)
	if got := dueAsks(logs); len(got) != 0 {
		t.Fatalf("asked although the session sent a message after the wake: %+v", got)
	}
}

func TestDueDoneBeforeTheAskRaisesNothing(t *testing.T) {
	s, c, _, a, _, _ := followUpRig(t)
	logs := captureLog(s)
	item := dueAdd(t, s, a, protocol.DueArgs{What: "Flip ch_1", Due: "10m"})
	c.advance(11 * time.Minute)
	tickDues(t, s)

	c.advance(3 * time.Minute)
	if _, err := s.SessionDue(a, protocol.DueArgs{Action: protocol.DueActionDone, ID: item.ID}); err != nil {
		t.Fatal(err)
	}
	c.advance(30 * time.Minute)
	tickDues(t, s)
	if got := dueAsks(logs); len(got) != 0 {
		t.Fatalf("asked after the item was done: %+v", got)
	}
	open, err := s.SessionDue(a, protocol.DueArgs{Action: protocol.DueActionList})
	if err != nil {
		t.Fatal(err)
	}
	if len(open.Items) != 0 {
		t.Fatalf("done item still listed as open: %+v", open.Items)
	}
}

func TestDueDoneOfAnotherSessionIsRefused(t *testing.T) {
	s, c, _, a, b, _ := followUpRig(t)
	item := dueAdd(t, s, a, protocol.DueArgs{What: "Flip ch_1", Due: "10m"})
	c.advance(time.Minute)
	if _, err := s.SessionDue(b, protocol.DueArgs{Action: protocol.DueActionDone, ID: item.ID}); err == nil {
		t.Fatal("bob closed carol's due item")
	}
}

func TestDueOwnerBlockedRaisesOneChiefAskAndNoWake(t *testing.T) {
	s, c, _, a, _, _ := followUpRig(t)
	logs := captureLog(s)
	item := dueAdd(t, s, a, protocol.DueArgs{What: "Decide the merge", Due: "10m", Blocker: protocol.DueBlockerOwner})
	c.advance(11 * time.Minute)
	wakes := tickDues(t, s)
	wakes = append(wakes, tickDues(t, s)...)
	c.advance(time.Hour)
	wakes = append(wakes, tickDues(t, s)...)
	if len(wakes) != 0 {
		t.Fatalf("an owner-blocked item woke a session: %+v", wakes)
	}
	got := dueAsks(logs)
	if len(got) != 1 {
		t.Fatalf("got %d asks, want exactly 1: %+v", len(got), got)
	}
	if !strings.Contains(got[0].text, item.ID) {
		t.Errorf("ask text %q", got[0].text)
	}
}

package gateway

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// briefRig is a store with a live chief (alice), another live session (bob)
// and a node. The owner set the brief time zone to the zone given.
func briefRig(t *testing.T) (s *Store, c *clock, chief, other string) {
	t.Helper()
	s, c = newTestStore(t)
	chief, other = twoSessions(t, s)
	if _, err := s.db.Exec(`UPDATE sessions SET online=1, archived_at=0 WHERE id IN (?,?)`, chief, other); err != nil {
		t.Fatal(err)
	}
	if err := s.SetChief(protocol.ChiefSetRequest{SessionID: chief}, "owner"); err != nil {
		t.Fatal(err)
	}
	return s, c, chief, other
}

func setBriefZone(t *testing.T, s *Store, zone string) {
	t.Helper()
	if _, err := s.db.Exec(`INSERT INTO owner_settings(key,value,updated_at) VALUES(?,?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, timeZoneSettingsKey, zone, s.now().UnixNano()); err != nil {
		t.Fatal(err)
	}
}

func setBriefTime(t *testing.T, s *Store, hhmm string) {
	t.Helper()
	if _, err := s.WriteSettings([]SettingsChange{{Key: protocol.ChiefBriefTimeKey, Level: "network", Scope: "network", Value: json.RawMessage(`"` + hhmm + `"`)}}, "owner", "test"); err != nil {
		t.Fatal(err)
	}
}

// briefMessages returns the bodies of the timer's messages to the chief.
func briefMessages(t *testing.T, s *Store, to string) []string {
	t.Helper()
	rows, err := s.db.Query(`SELECT body FROM messages WHERE recipient=? AND sender='agent-net' ORDER BY created_at`, to)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			t.Fatal(err)
		}
		out = append(out, body)
	}
	return out
}

// The timer sends once per owner-local date. A restart on the same date sends
// nothing, and the next date sends once more.
func TestDailyBriefTimerSendsOncePerLocalDateAcrossRestart(t *testing.T) {
	path := seededStorePath(t, "gw.db")
	open := func(now time.Time) *Store {
		s, err := OpenStore(path)
		if err != nil {
			t.Fatal(err)
		}
		s.now = func() time.Time { return now }
		s.tempFolders = []string{}
		return s
	}
	base := time.Date(2026, 11, 9, 8, 0, 0, 0, time.UTC) // 09:00 in Paris
	s := open(base.Add(-time.Minute))
	if _, err := s.db.Exec(`UPDATE sessions SET online=1, archived_at=0`); err != nil {
		t.Fatal(err)
	}
	chief, _ := twoSessions(t, s)
	if _, err := s.db.Exec(`UPDATE sessions SET online=1, archived_at=0 WHERE id=?`, chief); err != nil {
		t.Fatal(err)
	}
	if err := s.SetChief(protocol.ChiefSetRequest{SessionID: chief}, "owner"); err != nil {
		t.Fatal(err)
	}
	setBriefZone(t, s, "Europe/Paris")

	// 08:59 Paris: not due.
	if _, sent, err := s.checkDailyBrief(base.Add(-time.Minute)); err != nil || sent {
		t.Fatalf("before the brief time: sent=%v err=%v", sent, err)
	}
	// 09:00 Paris: due. The message names the owner-local date.
	m, sent, err := s.checkDailyBrief(base)
	if err != nil || !sent {
		t.Fatalf("at the brief time: sent=%v err=%v", sent, err)
	}
	if !strings.Contains(m.Body, "2026-11-09") || !strings.Contains(m.Body, "agent_owner_brief") {
		t.Fatalf("brief wake body = %q", m.Body)
	}
	if got := briefMessages(t, s, chief); len(got) != 1 {
		t.Fatalf("messages after the first send = %v", got)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// A restart later the same date must not send again.
	s = open(base.Add(4 * time.Hour))
	defer s.Close()
	if _, sent, err := s.checkDailyBrief(base.Add(4 * time.Hour)); err != nil || sent {
		t.Fatalf("same date after a restart: sent=%v err=%v", sent, err)
	}
	if got := briefMessages(t, s, chief); len(got) != 1 {
		t.Fatalf("messages after the restart = %v", got)
	}

	// The next local date sends once more.
	next := base.AddDate(0, 0, 1)
	if _, sent, err := s.checkDailyBrief(next); err != nil || !sent {
		t.Fatalf("next date: sent=%v err=%v", sent, err)
	}
	got := briefMessages(t, s, chief)
	if len(got) != 2 || !strings.Contains(got[1], "2026-11-10") {
		t.Fatalf("messages after the next date = %v", got)
	}
}

// Without the owner's time zone the timer does not run, and the status says
// what it waits for.
func TestDailyBriefDoesNotRunWithoutTimeZone(t *testing.T) {
	s, c, chief, _ := briefRig(t)
	// The default is auto, which is not a zone the timer can use.
	now := c.t
	if _, sent, err := s.checkDailyBrief(now); err != nil || sent {
		t.Fatalf("without a zone: sent=%v err=%v", sent, err)
	}
	if got := briefMessages(t, s, chief); len(got) != 0 {
		t.Fatalf("messages without a zone = %v", got)
	}
	status, err := s.dailyBriefStatus(now)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != protocol.BriefStatusWaitingZone {
		t.Fatalf("status = %q, want %q", status.Status, protocol.BriefStatusWaitingZone)
	}
	if status.NextRunLocal != "" || status.NextRunUTC != "" {
		t.Fatalf("a waiting timer has no next run: %+v", status)
	}
}

// The brief_time setting is read in the owner's zone, including a zone whose
// offset is not a whole hour.
func TestDailyBriefUsesBriefTimeInHalfHourZone(t *testing.T) {
	s, c, chief, _ := briefRig(t)
	setBriefZone(t, s, "Asia/Kolkata") // UTC+05:30
	setBriefTime(t, s, "08:15")

	// 07:30 local: the next run is 08:15 local, 02:45 UTC.
	c.t = time.Date(2026, 11, 9, 2, 0, 0, 0, time.UTC)
	status, err := s.dailyBriefStatus(c.t)
	if err != nil {
		t.Fatal(err)
	}
	if status.BriefTime != "08:15" || status.TimeZone != "Asia/Kolkata" {
		t.Fatalf("schedule = %+v", status)
	}
	if status.NextRunLocal != "2026-11-09 08:15" || status.NextRunUTC != "2026-11-09T02:45:00Z" {
		t.Fatalf("next run = %q / %q", status.NextRunLocal, status.NextRunUTC)
	}

	// 08:14 local: not due. 08:15 local: due.
	if _, sent, err := s.checkDailyBrief(time.Date(2026, 11, 9, 2, 44, 0, 0, time.UTC)); err != nil || sent {
		t.Fatalf("08:14 local: sent=%v err=%v", sent, err)
	}
	if _, sent, err := s.checkDailyBrief(time.Date(2026, 11, 9, 2, 45, 0, 0, time.UTC)); err != nil || !sent {
		t.Fatalf("08:15 local: sent=%v err=%v", sent, err)
	}
	if got := briefMessages(t, s, chief); len(got) != 1 {
		t.Fatalf("messages = %v", got)
	}
}

// A brief is one informational item on the Desk. A second call for the same
// local date replaces it, and it takes no approval answer.
func TestOwnerBriefCreatesInformationalItemAndReplacesSameDate(t *testing.T) {
	s, c, chief, _ := briefRig(t)
	first, err := s.putOwnerBrief(chief, protocol.OwnerBriefArgs{Body: "Handled two fixes.\nOne decision waits.", Date: "2026-10-28"}, c.t)
	if err != nil {
		t.Fatal(err)
	}
	if first.ItemID == "" || first.Date != "2026-10-28" {
		t.Fatalf("receipt = %+v", first)
	}
	asks, err := s.ListAsks()
	if err != nil {
		t.Fatal(err)
	}
	var brief *protocol.Ask
	for i := range asks {
		if asks[i].Kind == protocol.AskKindBrief {
			brief = &asks[i]
		}
		if asks[i].Kind == protocol.AskKindApproval || asks[i].Kind == protocol.AskKindQuestion {
			t.Fatalf("a brief made an ask that needs an answer: %+v", asks[i])
		}
	}
	if brief == nil {
		t.Fatal("the brief is not on the Desk")
	}
	if brief.From != "Daily brief 2026-10-28" || brief.Question != "Handled two fixes.\nOne decision waits." || brief.Next != protocol.AskNextOwner {
		t.Fatalf("brief ask = %+v", brief)
	}
	if _, err := s.AnswerInbox(brief.ID, "approve"); err == nil || !strings.Contains(err.Error(), "brief") {
		t.Fatalf("a brief accepted an approval: %v", err)
	}

	second, err := s.putOwnerBrief(chief, protocol.OwnerBriefArgs{Body: "A new brief for the same date.", Date: "2026-10-28"}, c.t.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if second.ItemID != first.ItemID {
		t.Fatalf("replacement id = %q, first %q", second.ItemID, first.ItemID)
	}
	asks, err = s.ListAsks()
	if err != nil {
		t.Fatal(err)
	}
	var open []protocol.Ask
	for _, a := range asks {
		if a.Kind == protocol.AskKindBrief {
			open = append(open, a)
		}
	}
	if len(open) != 1 || open[0].Question != "A new brief for the same date." {
		t.Fatalf("open briefs after a replacement = %+v", open)
	}
	status, err := s.dailyBriefStatus(c.t.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if status.LastItemID != first.ItemID {
		t.Fatalf("last brief id = %q, want %q", status.LastItemID, first.ItemID)
	}
	// The owner may dismiss it; that is its only action.
	if _, err := s.AnswerInbox(first.ItemID, "dismiss"); err != nil {
		t.Fatal(err)
	}
}

// Another session cannot deliver a brief.
func TestOwnerBriefRefusesAnotherSession(t *testing.T) {
	s, c, _, other := briefRig(t)
	_, err := s.putOwnerBrief(other, protocol.OwnerBriefArgs{Body: "not mine", Date: "2026-10-28"}, c.t)
	if err == nil || !strings.Contains(err.Error(), "chief of staff") {
		t.Fatalf("another session delivered a brief: %v", err)
	}
}

// The status call returns the schedule and the receipt.
func TestOwnerBriefStatusReturnsNextRun(t *testing.T) {
	s, c, chief, _ := briefRig(t)
	setBriefZone(t, s, "Asia/Kolkata")
	setBriefTime(t, s, "08:15")
	c.t = time.Date(2026, 11, 9, 2, 0, 0, 0, time.UTC)
	status, err := s.dailyBriefStatus(c.t)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != protocol.BriefStatusActive || status.NextRunLocal != "2026-11-09 08:15" || status.NextRunUTC != "2026-11-09T02:45:00Z" {
		t.Fatalf("status = %+v", status)
	}
	if _, sent, err := s.checkDailyBrief(time.Date(2026, 11, 9, 2, 45, 0, 0, time.UTC)); err != nil || !sent {
		t.Fatalf("send: sent=%v err=%v", sent, err)
	}
	_, err = s.putOwnerBrief(chief, protocol.OwnerBriefArgs{Body: "Today, one line.", Date: "2026-11-09"}, c.t)
	if err != nil {
		t.Fatal(err)
	}
	status, err = s.dailyBriefStatus(time.Date(2026, 11, 9, 3, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if status.LastDateSent != "2026-11-09" || status.LastItemID == "" {
		t.Fatalf("receipt = %+v", status)
	}
	if status.NextRunLocal != "2026-11-10 08:15" || status.NextRunUTC != "2026-11-10T02:45:00Z" {
		t.Fatalf("next run after the send = %q / %q", status.NextRunLocal, status.NextRunUTC)
	}
}

// A brief longer than 15 lines is refused with the limit in the error.
func TestOwnerBriefRefusesMoreThanFifteenLines(t *testing.T) {
	s, c, chief, _ := briefRig(t)
	lines := make([]string, 16)
	for i := range lines {
		lines[i] = "line"
	}
	_, err := s.putOwnerBrief(chief, protocol.OwnerBriefArgs{Body: strings.Join(lines, "\n"), Date: "2026-10-28"}, c.t)
	if err == nil || !strings.Contains(err.Error(), "15") {
		t.Fatalf("16 lines: %v", err)
	}
	if _, err := s.putOwnerBrief(chief, protocol.OwnerBriefArgs{Body: strings.Join(lines[:15], "\n"), Date: "2026-10-28"}, c.t); err != nil {
		t.Fatalf("15 lines: %v", err)
	}
}

// The gateway frames deliver and read the schedule, and refuse another
// session or another machine.
func TestOwnerBriefGatewayFrames(t *testing.T) {
	r := newDeliveryRig(t)
	if _, err := r.s.db.Exec(`UPDATE sessions SET online=1, archived_at=0 WHERE id IN (?,?,?)`, r.caller, r.sonnet, r.deepseek); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetChief(protocol.ChiefSetRequest{SessionID: r.caller}, "owner"); err != nil {
		t.Fatal(err)
	}
	setBriefZone(t, r.s, "Europe/Paris")

	receipt, err := deliveryCall[protocol.OwnerBriefResult](r, r.a, protocol.FrameOwnerBrief,
		protocol.OwnerBriefRequest{SessionID: r.caller, OwnerBriefArgs: protocol.OwnerBriefArgs{Body: "One line.", Date: "2026-10-28"}})
	if err != nil || receipt.ItemID == "" || receipt.Date != "2026-10-28" {
		t.Fatalf("brief.put = %+v, %v", receipt, err)
	}
	status, err := deliveryCall[protocol.OwnerBriefResult](r, r.a, protocol.FrameOwnerBriefStatus,
		protocol.OwnerBriefRequest{SessionID: r.caller})
	if err != nil || status.Status != protocol.BriefStatusActive || status.NextRunLocal == "" || status.LastItemID != receipt.ItemID {
		t.Fatalf("brief.status = %+v, %v", status, err)
	}
	if _, err := deliveryCall[protocol.OwnerBriefResult](r, r.a, protocol.FrameOwnerBrief,
		protocol.OwnerBriefRequest{SessionID: r.sonnet, OwnerBriefArgs: protocol.OwnerBriefArgs{Body: "Not mine.", Date: "2026-10-28"}}); err == nil {
		t.Fatal("another session delivered a brief")
	}
	if _, err := deliveryCall[protocol.OwnerBriefResult](r, r.b, protocol.FrameOwnerBrief,
		protocol.OwnerBriefRequest{SessionID: r.caller, OwnerBriefArgs: protocol.OwnerBriefArgs{Body: "Bad node.", Date: "2026-10-28"}}); err == nil {
		t.Fatal("a brief was delivered from another node")
	}
}

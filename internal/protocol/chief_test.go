package protocol

import (
	"strings"
	"testing"
	"time"
)

// Each sort result needs what it acts on: a target for merge, a session for
// send, a priority and a why_you line for owner. A reason is always needed.
func TestAskSortArgsValidate(t *testing.T) {
	ok := []AskSortArgs{
		{Ask: "i_1", Result: AskSortClose, Reason: "answered"},
		{Ask: "i_1", Result: AskSortMerge, Into: "i_2", Reason: "same"},
		{Ask: "i_1", Result: AskSortSend, To: "s_2", Reason: "theirs"},
		{Ask: "i_1", Result: AskSortOwner, Priority: 3, WhyYou: "only you can decide", Reason: "decision"},
		{Result: AskSortList},
		{Ask: "i_1", Detail: "full"},
		{Ask: "i_1", Result: AskSortUndo},
		{Result: AskSortList, Sorter: "owner", Since: 10, Until: 20, FromID: 1, ToID: 9},
	}
	for _, a := range ok {
		if err := a.Validate(); err != nil {
			t.Errorf("%+v: %v", a, err)
		}
	}
	bad := []AskSortArgs{
		{Result: AskSortClose, Reason: "x"},
		{Result: AskSortUndo},
		{Ask: "i_1", Result: AskSortClose},
		{Ask: "i_1", Result: "answer", Reason: "x"},
		{Ask: "i_1", Result: AskSortMerge, Reason: "x"},
		{Ask: "i_1", Result: AskSortSend, Reason: "x"},
		{Ask: "i_1", Result: AskSortOwner, Priority: 0, WhyYou: "x", Reason: "x"},
		{Ask: "i_1", Result: AskSortOwner, Priority: 4, WhyYou: "x", Reason: "x"},
		{Ask: "i_1", Result: AskSortOwner, Priority: 1, Reason: "x"},
		{Ask: "i_1", Detail: "short"},
		{Ask: "i_1", Detail: "full", Result: AskSortClose, Reason: "x"},
		{Ask: "i_1", Detail: "full", Priority: 1},
		{Result: AskSortList, Since: -1},
		{Result: AskSortList, Since: 20, Until: 10},
		{Result: AskSortList, FromID: 9, ToID: 1},
	}
	for _, a := range bad {
		if err := a.Validate(); err == nil {
			t.Errorf("accepted %+v", a)
		}
	}
}

// A mark needs an ask and one of the two marks; the why is optional and short.
func TestAskMarkRequestValidate(t *testing.T) {
	ok := []AskMarkRequest{
		{Ask: "i_1", Mark: MarkDidntNeedMe},
		{Ask: "i_1", Mark: MarkShouldHaveAsked, Why: "the deploy was mine"},
	}
	for _, m := range ok {
		if err := m.Validate(); err != nil {
			t.Errorf("%+v: %v", m, err)
		}
	}
	bad := []AskMarkRequest{
		{Mark: MarkDidntNeedMe},
		{Ask: "i_1", Mark: "maybe"},
		{Ask: "i_1", Mark: MarkDidntNeedMe, Why: strings.Repeat("x", MaxChiefReasonChars+1)},
	}
	for _, m := range bad {
		if err := m.Validate(); err == nil {
			t.Errorf("%+v was accepted", m)
		}
	}
}

// The chief's quota verdict reads the picker's quota data: a used-up window of
// the chief's provider says so and names the reset.
func TestChiefQuotaDown(t *testing.T) {
	q := &Quotas{GeneratedAt: 1, Providers: []QuotaProvider{{Provider: "codex",
		Windows: []QuotaWindow{{ID: "weekly", Label: "weekly", PercentUsed: 100, ResetsAt: time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC).Unix()}}}}}
	got := ChiefQuotaDown(q, "codex", "")
	if got != "codex quota exhausted (weekly window, until 2026-11-15 00:00 UTC)" {
		t.Fatalf("down = %q", got)
	}
	q.Providers[0].Windows[0].PercentUsed = 40
	if got := ChiefQuotaDown(q, "codex", ""); got != "" {
		t.Fatalf("down = %q with quota left, want none", got)
	}
	q.Providers[0].Windows[0].PercentUsed = 100
	q.Providers[0].Stale = true
	if got := ChiefQuotaDown(q, "codex", ""); got != "" {
		t.Fatalf("down = %q for a stale reading, want none", got)
	}
	if got := ChiefQuotaDown(nil, "codex", ""); got != "" {
		t.Fatalf("down = %q with no reading, want none", got)
	}
}

func TestChiefOfflineLineAndSilentText(t *testing.T) {
	if got := ChiefOfflineLine("no sorts for 10 minutes"); got != "chief of staff offline: no sorts for 10 minutes" {
		t.Fatalf("line = %q", got)
	}
	if got := ChiefSilentText(ChiefSilentAfter); got != "no sorts for 10 minutes" {
		t.Fatalf("text = %q", got)
	}
}

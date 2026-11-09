package protocol

import (
	"strings"
	"testing"
)

// Each sort result needs what it acts on: a target for merge, a session for
// send, a priority and a why_you line for owner. A reason is always needed.
func TestAskSortArgsValidate(t *testing.T) {
	ok := []AskSortArgs{
		{Ask: "i_1", Result: AskSortClose, Reason: "answered"},
		{Ask: "i_1", Result: AskSortMerge, Into: "i_2", Reason: "same"},
		{Ask: "i_1", Result: AskSortSend, To: "s_2", Reason: "theirs"},
		{Ask: "i_1", Result: AskSortOwner, Priority: 3, WhyYou: "only you can decide", Reason: "decision"},
		{Ask: "i_1", Detail: "full"},
	}
	for _, a := range ok {
		if err := a.Validate(); err != nil {
			t.Errorf("%+v: %v", a, err)
		}
	}
	bad := []AskSortArgs{
		{Result: AskSortClose, Reason: "x"},
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

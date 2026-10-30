package protocol

import (
	"strings"
	"testing"
)

func TestCleanOutcome(t *testing.T) {
	cases := []struct {
		input ReplyOutcome
		valid bool
	}{
		{ReplyOutcome{}, true}, {ReplyOutcome{Outcome: "accept"}, true},
		{ReplyOutcome{Outcome: "reject"}, false}, {ReplyOutcome{Outcome: "reject", Reason: "no"}, true},
		{ReplyOutcome{Outcome: "redirect", Reason: "elsewhere"}, false}, {ReplyOutcome{Outcome: "redirect", Reason: "elsewhere", RedirectTo: "other"}, true},
		{ReplyOutcome{Outcome: "escalate", Reason: "unclear"}, false}, {ReplyOutcome{Outcome: "escalate", Reason: "unclear", Recommendation: "ask"}, true},
		{ReplyOutcome{Outcome: "other"}, false}, {ReplyOutcome{Outcome: "reject", Reason: strings.Repeat("x", MaxOutcomeTextBytes+1)}, false},
	}
	for _, c := range cases {
		_, err := CleanOutcome(c.input)
		if (err == nil) != c.valid {
			t.Errorf("%+v: %v", c.input, err)
		}
	}
	got, err := CleanOutcome(ReplyOutcome{Outcome: "reject", Reason: "  n\x00o  "})
	if err != nil || got.Reason != "no" {
		t.Fatalf("clean: %+v, %v", got, err)
	}
}

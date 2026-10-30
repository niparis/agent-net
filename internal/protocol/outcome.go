package protocol

import (
	"fmt"
	"strings"
	"unicode"
)

// ReplyOutcome is an optional decision on a request.
type ReplyOutcome struct {
	Outcome        string `json:"outcome,omitempty" jsonschema:"accept, reject, redirect or escalate"`
	Reason         string `json:"reason,omitempty" jsonschema:"required for reject, redirect and escalate"`
	RedirectTo     string `json:"redirect_to,omitempty" jsonschema:"node to redirect to"`
	Recommendation string `json:"recommendation,omitempty" jsonschema:"required for escalate"`
}

const MaxOutcomeTextBytes = 2048

// CleanOutcome removes controls and checks the decision and its fields.
func CleanOutcome(o ReplyOutcome) (ReplyOutcome, error) {
	clean := func(s string) string {
		return strings.TrimSpace(strings.Map(func(r rune) rune {
			if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
				return -1
			}
			return r
		}, s))
	}
	o.Outcome, o.Reason, o.RedirectTo, o.Recommendation = clean(o.Outcome), clean(o.Reason), clean(o.RedirectTo), clean(o.Recommendation)
	if len(o.Reason) > MaxOutcomeTextBytes || len(o.Recommendation) > MaxOutcomeTextBytes || len(o.RedirectTo) > ProjectMaxFieldLen {
		return o, fmt.Errorf("outcome field too long")
	}
	switch o.Outcome {
	case "":
		if o.Reason != "" || o.RedirectTo != "" || o.Recommendation != "" {
			return o, fmt.Errorf("outcome required")
		}
	case "accept":
	case "reject", "redirect", "escalate":
		if o.Reason == "" {
			return o, fmt.Errorf("reason required")
		}
	default:
		return o, fmt.Errorf("invalid outcome")
	}
	if o.Outcome == "redirect" {
		if o.RedirectTo == "" || o.RedirectTo == "." || o.RedirectTo == ".." {
			return o, fmt.Errorf("invalid redirect_to node")
		}
		for _, r := range o.RedirectTo {
			if !unicode.IsPrint(r) || r == '/' || r == '\\' {
				return o, fmt.Errorf("invalid redirect_to node")
			}
		}
	}
	if o.Outcome == "escalate" && o.Recommendation == "" {
		return o, fmt.Errorf("recommendation required")
	}
	return o, nil
}

// EdgeOutcome records a decision on a request between two nodes.
type EdgeOutcome struct {
	From      string `json:"from"`
	To        string `json:"to"`
	MessageID string `json:"message_id"`
	ReplyID   string `json:"reply_id"`
	ReplyOutcome
	At int64 `json:"at"`
}

// OutcomeInboxEntry is an unresolved decision for the owner.
type OutcomeInboxEntry struct {
	EdgeOutcome
	Sender    string `json:"sender"`
	Recipient string `json:"recipient"`
	Request   string `json:"request"`
}

// OutcomeResolution is an owner decision on an inbox entry.
type OutcomeResolution struct {
	ReplyID string `json:"reply_id"`
	Action  string `json:"action"`
	Owner   string `json:"owner,omitempty"`
}

const OverruleNote = "Nicolas overrules: do it."

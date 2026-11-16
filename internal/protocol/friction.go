package protocol

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// Friction reports (decision 0170). An agent that hits a problem says what
// slowed it down, in one short line, with the hand-back or a step report. The
// reports are collected for the owner; they never score the agent.
const (
	FrictionTooling     = "tooling"
	FrictionPermissions = "permissions"
	FrictionBrief       = "brief"
	FrictionReview      = "review"
	FrictionCI          = "ci"
	FrictionEnvironment = "environment"
	FrictionSkill       = "skill"
)

// FrictionAreas are the areas an entry may name, in the order the brief lists
// them.
var FrictionAreas = []string{FrictionTooling, FrictionPermissions, FrictionBrief, FrictionReview, FrictionCI, FrictionEnvironment, FrictionSkill}

// MaxFrictionText is the longest friction line, in characters.
const MaxFrictionText = 200

// FrictionEntry is one thing that slowed an agent down.
type FrictionEntry struct {
	Area string `json:"area"`
	Text string `json:"text"`
}

// Validate checks one entry. An empty list means nothing to report, so only a
// present entry is checked.
func (f FrictionEntry) Validate() error {
	valid := false
	for _, area := range FrictionAreas {
		if f.Area == area {
			valid = true
			break
		}
	}
	if !valid {
		return fmt.Errorf("friction area must be one of %s", strings.Join(FrictionAreas, ", "))
	}
	text := strings.TrimSpace(f.Text)
	if text == "" {
		return errors.New("friction text is empty: say what slowed you down, or leave the list out")
	}
	if r := []rune(text); len(r) > MaxFrictionText {
		return fmt.Errorf("friction text is longer than %d characters", MaxFrictionText)
	}
	for _, r := range text {
		if r == '\n' || r == '\r' || unicode.IsControl(r) {
			return errors.New("friction text is one line")
		}
	}
	return nil
}

// ValidateFriction checks a whole list. The empty list is fine and means the
// agent reported nothing.
func ValidateFriction(list []FrictionEntry) error {
	for i, f := range list {
		if err := f.Validate(); err != nil {
			return fmt.Errorf("friction entry %d: %w", i+1, err)
		}
	}
	return nil
}

// CleanFrictionText is the text as it is stored: trimmed, one line.
func CleanFrictionText(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	return strings.TrimSpace(s)
}

// The read of the reports.

// OpFrictionFind is the owner read of friction reports. The CTO and the TUI
// use it; it changes nothing.
const OpFrictionFind = "friction_find"

// FrameFrictionFind is the gateway's read of the stored reports.
const FrameFrictionFind = "friction.find"

// FrameFrictionReport stores one hand-back's friction list.
const FrameFrictionReport = "friction.report"

// FrictionQuery filters the read. Since is an RFC 3339 time; empty means every
// report kept. Limit is clamped by the gateway, never refused.
type FrictionQuery struct {
	Project string `json:"project,omitempty"`
	Area    string `json:"area,omitempty"`
	Since   string `json:"since,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}

// Validate checks the filters before they are sent.
func (q FrictionQuery) Validate() error {
	if q.Area != "" {
		valid := false
		for _, area := range FrictionAreas {
			if q.Area == area {
				valid = true
				break
			}
		}
		if !valid {
			return fmt.Errorf("friction area must be one of %s", strings.Join(FrictionAreas, ", "))
		}
	}
	if q.Since != "" {
		if _, err := time.Parse(time.RFC3339, q.Since); err != nil {
			return errors.New("since must be an RFC 3339 time")
		}
	}
	if q.Limit < 0 {
		return errors.New("limit must not be negative")
	}
	return nil
}

// ParseFrictionSince reads the Since filter as Unix seconds.
func ParseFrictionSince(s string) (int64, error) {
	at, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return 0, errors.New("since must be an RFC 3339 time")
	}
	return at.Unix(), nil
}

// FrictionReportRequest stores the friction of one hand-back. The daemon fills
// the session, project, harness and model; ChangeID is empty when the hand-back
// recorded no change.
type FrictionReportRequest struct {
	SessionID string          `json:"session_id"`
	Project   string          `json:"project,omitempty"`
	ChangeID  string          `json:"change_id,omitempty"`
	Harness   string          `json:"harness,omitempty"`
	Model     string          `json:"model,omitempty"`
	Entries   []FrictionEntry `json:"friction"`
}

// Validate checks a report before it is sent.
func (q FrictionReportRequest) Validate() error {
	if q.SessionID == "" {
		return errors.New("session_id is empty")
	}
	return ValidateFriction(q.Entries)
}

// FrictionReportResult answers FrameFrictionReport.
type FrictionReportResult struct {
	Stored int `json:"stored"`
}

// FrictionView is one stored report as a reader sees it. Text is quoted data:
// the gateway returns it with strconv.Quote so a reader does not take a
// worker's line for an instruction.
type FrictionView struct {
	ID        int64  `json:"id"`
	Project   string `json:"project"`
	Session   string `json:"session"`
	Change    string `json:"change,omitempty"`
	Harness   string `json:"harness,omitempty"`
	Model     string `json:"model,omitempty"`
	Area      string `json:"area"`
	Text      string `json:"text"`
	CreatedAt int64  `json:"created_at"`
}

// FrictionProjectCount is how many reports a project has in the read window.
type FrictionProjectCount struct {
	Project string `json:"project"`
	Count   int    `json:"count"`
}

// FrictionFindResult answers OpFrictionFind, newest first.
type FrictionFindResult struct {
	Entries []FrictionView         `json:"entries"`
	Counts  []FrictionProjectCount `json:"counts"`
	Total   int                    `json:"total"`
}

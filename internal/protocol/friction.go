package protocol

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
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

// The read status of the reports (decision 0175). A report with no status row
// is new. Read and triage are the owner's marks; fixed, wontfix and infra close
// a report.
const (
	FrictionNew     = "new"
	FrictionRead    = "read"
	FrictionTriaged = "triaged"
	FrictionFixed   = "fixed"
	FrictionWontfix = "wontfix"
	FrictionInfra   = "infra"
)

// FrictionStatuses are the states a report may hold, in the order the owner
// reads them.
var FrictionStatuses = []string{FrictionNew, FrictionRead, FrictionTriaged, FrictionFixed, FrictionWontfix, FrictionInfra}

// MaxFrictionLink and MaxFrictionNote bound the link to a fix and the note of a
// mark, in characters.
const (
	MaxFrictionLink = 200
	MaxFrictionNote = 200
)

// ValidFrictionStatus tells whether s is one of the read states.
func ValidFrictionStatus(s string) bool {
	for _, status := range FrictionStatuses {
		if s == status {
			return true
		}
	}
	return false
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
// report kept. Group is the id a reply gave a report's duplicates. ID reads one
// report with its status history. Limit is clamped by the gateway, never
// refused; a reply with more rows names the cursor of the next page.
type FrictionQuery struct {
	Project string `json:"project,omitempty"`
	Area    string `json:"area,omitempty"`
	Since   string `json:"since,omitempty"`
	Status  string `json:"status,omitempty"`
	Text    string `json:"text,omitempty"`
	Group   string `json:"group,omitempty"`
	ID      int64  `json:"report_id,omitempty"`
	Limit   int    `json:"limit,omitempty"`
	Cursor  string `json:"cursor,omitempty"`
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
	if q.Status != "" && !ValidFrictionStatus(q.Status) {
		return fmt.Errorf("status must be one of %s", strings.Join(FrictionStatuses, ", "))
	}
	if q.Limit < 0 {
		return errors.New("limit must not be negative")
	}
	if q.ID < 0 {
		return errors.New("report_id must not be negative")
	}
	if q.Cursor != "" {
		if id, err := strconv.ParseInt(q.Cursor, 10, 64); err != nil || id <= 0 {
			return errors.New("cursor must be the next_cursor of an earlier friction_find reply")
		}
	}
	return nil
}

// FrictionMarkRequest sets the read status of reports. Give exactly one way to
// pick them: the report ids, a group id, or a filter (project, area and a text
// match). DryRun counts the matches and writes nothing.
type FrictionMarkRequest struct {
	IDs     []int64 `json:"ids,omitempty"`
	Group   string  `json:"group,omitempty"`
	Project string  `json:"project,omitempty"`
	Area    string  `json:"area,omitempty"`
	Text    string  `json:"text,omitempty"`
	Status  string  `json:"status"`
	Link    string  `json:"link,omitempty"`
	Note    string  `json:"note,omitempty"`
	DryRun  bool    `json:"dry_run,omitempty"`
}

// Validate checks a mark before it is sent. The status is required, and the
// selection must name one way to pick reports.
func (q FrictionMarkRequest) Validate() error {
	if !ValidFrictionStatus(q.Status) {
		return fmt.Errorf("status must be one of %s", strings.Join(FrictionStatuses, ", "))
	}
	ways := 0
	if len(q.IDs) > 0 {
		ways++
		for _, id := range q.IDs {
			if id <= 0 {
				return errors.New("ids must be report ids above zero")
			}
		}
	}
	if q.Group != "" {
		ways++
	}
	if q.Project != "" || q.Area != "" || q.Text != "" {
		ways++
	}
	if ways != 1 {
		return errors.New("give exactly one of ids, group, or a filter with project, area or text")
	}
	if q.Area != "" && !validFrictionArea(q.Area) {
		return fmt.Errorf("friction area must be one of %s", strings.Join(FrictionAreas, ", "))
	}
	if q.Link != "" {
		if err := checkFrictionLink(q.Link); err != nil {
			return err
		}
	}
	if n := len([]rune(CleanFrictionText(q.Note))); n > MaxFrictionNote {
		return fmt.Errorf("note is longer than %d characters", MaxFrictionNote)
	}
	return nil
}

// checkFrictionLink accepts a change id (ch_...) or a web address, one token.
func checkFrictionLink(link string) error {
	if len([]rune(link)) > MaxFrictionLink {
		return fmt.Errorf("link is longer than %d characters", MaxFrictionLink)
	}
	if strings.ContainsAny(link, " \t\r\n") {
		return errors.New("link must be one token: a change id such as ch_1234 or a web address")
	}
	if strings.HasPrefix(link, "ch_") || strings.HasPrefix(link, "https://") || strings.HasPrefix(link, "http://") {
		return nil
	}
	return errors.New("link must be a change id such as ch_1234 or a web address")
}

func validFrictionArea(area string) bool {
	for _, a := range FrictionAreas {
		if area == a {
			return true
		}
	}
	return false
}

// FrictionMarkCall is the mark as the daemon sends it to the gateway: the
// request and the owner or CTO who made it.
type FrictionMarkCall struct {
	Actor string `json:"actor"`
	// SessionID is set only for an agent's mark (friction_mark_lead). The gateway
	// then checks that the session is a lead before it writes.
	SessionID string `json:"session_id,omitempty"`
	FrictionMarkRequest
}

// FrictionMarkResult answers OpFrictionMark. Matched counts the reports the
// selection holds; Marked is zero on a dry run. SampleIDs holds the first few
// ids, so the owner can see what a filter caught.
type FrictionMarkResult struct {
	Status    string  `json:"status"`
	Matched   int     `json:"matched"`
	Marked    int     `json:"marked"`
	DryRun    bool    `json:"dry_run"`
	SampleIDs []int64 `json:"sample_ids"`
}

// OpFrictionMark sets the read status of friction reports. Only the owner's
// command line or TUI and the CTO may call it: the operator tier.
const OpFrictionMark = "friction_mark"

// FrameFrictionMark sets the status of reports at the gateway.
const FrameFrictionMark = "friction.mark"

// OpFrictionMarkLead is the agent's mark, for the lead session of an Agent. The
// daemon sends it as a FrameFrictionMark with the session id, and the gateway
// refuses any session that is not a lead.
const OpFrictionMarkLead = "friction_mark_lead"

var (
	frictionPathToken = regexp.MustCompile(`\S*/\S*`)
	frictionIDToken   = regexp.MustCompile(`\b[a-z]{1,4}_[0-9a-z]{4,}\b`)
	frictionDigits    = regexp.MustCompile(`[0-9]+`)
)

// FrictionGroup is the id of a report's duplicates: the same area and the same
// text once paths, ids and numbers are removed. Reports from different sessions
// that say the same thing share it, so one mark can close them all. The id is a
// function of the text, so changing this function changes the ids it returns.
func FrictionGroup(area, text string) string {
	norm := strings.ToLower(CleanFrictionText(text))
	norm = frictionPathToken.ReplaceAllString(norm, " ")
	norm = frictionIDToken.ReplaceAllString(norm, " ")
	norm = frictionDigits.ReplaceAllString(norm, "")
	norm = strings.Join(strings.Fields(norm), " ")
	sum := sha256.Sum256([]byte(area + "\n" + norm))
	return "fg_" + hex.EncodeToString(sum[:])[:12]
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
	// Status is the read state; a report nobody marked is new. Group is the id
	// its duplicates share.
	Status    string `json:"status"`
	Group     string `json:"group"`
	ChangedBy string `json:"changed_by,omitempty"`
	ChangedAt int64  `json:"changed_at,omitempty"`
	Link      string `json:"link,omitempty"`
	Note      string `json:"note,omitempty"`
}

// FrictionProjectCount is how many reports a project has in the read window,
// and how many of them are still new.
type FrictionProjectCount struct {
	Project string `json:"project"`
	Count   int    `json:"count"`
	Unread  int    `json:"unread"`
}

// FrictionFindResult answers OpFrictionFind, newest first. Total and Unread
// count every report the filter holds, not only this page. NextCursor is set
// when more reports follow; pass it back as cursor to read them.
type FrictionFindResult struct {
	Entries    []FrictionView         `json:"entries"`
	Counts     []FrictionProjectCount `json:"counts"`
	Total      int                    `json:"total"`
	Unread     int                    `json:"unread"`
	NextCursor string                 `json:"next_cursor,omitempty"`
}

package protocol

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// The chief of staff's daily brief (decision 0114). One timer wakes the chief
// at a local time, and the chief delivers one informational item to the
// owner's Desk. The item is a kind of its own: it never asks for an answer.

// ChiefBriefTimeKey is the setting of the local time of the daily brief, as
// HH:MM in the owner's time zone.
const (
	ChiefBriefTimeKey       = "chief.brief_time"
	DefaultChiefBriefTime   = "09:00"
	MaxOwnerBriefLines      = 15
	BriefStatusActive       = "active"
	BriefStatusWaitingZone  = "waiting for the owner's time zone"
	BriefStatusNoChief      = "no chief of staff"
	BriefStatusLooksInvalid = "invalid chief.brief_time"
)

// Owner settings for the daily brief schedule and its delivery receipt. The
// wake date and the delivered date are separate: a wake is not a delivery.
const (
	BriefLastSentKey  = "chief.brief_last_sent"
	BriefLastWakeKey  = "chief.brief_last_wake"
	BriefLastItemKey  = "chief.brief_last_item"
	briefSourcePrefix = "brief:"
)

// OpOwnerBrief is the agent tool agent_owner_brief. The two frames carry a
// delivery and the schedule read from the daemon to the gateway.
const (
	OpOwnerBrief          = "owner_brief"
	FrameOwnerBrief       = "brief.put"
	FrameOwnerBriefStatus = "brief.status"
)

// OwnerBriefArgs are the arguments of agent_owner_brief. An empty body reads
// the schedule instead of delivering a brief. Date is the owner-local date the
// brief is for, YYYY-MM-DD.
type OwnerBriefArgs struct {
	Body string `json:"body"`
	Date string `json:"date"`
}

// IsStatus says whether the call reads the schedule.
func (a OwnerBriefArgs) IsStatus() bool { return strings.TrimSpace(a.Body) == "" }

// Validate checks a delivery before it reaches the gateway. The line limit is
// in the error, so an agent knows what to cut.
func (a OwnerBriefArgs) Validate() error {
	if a.IsStatus() {
		return nil
	}
	if strings.TrimSpace(a.Date) == "" {
		return errors.New("date is required: the owner-local date, YYYY-MM-DD")
	}
	if _, err := time.Parse("2006-01-02", strings.TrimSpace(a.Date)); err != nil {
		return fmt.Errorf("date must be the owner-local date as YYYY-MM-DD: %v", err)
	}
	if n := OwnerBriefLines(a.Body); n > MaxOwnerBriefLines {
		return fmt.Errorf("a brief is at most %d lines; this one has %d", MaxOwnerBriefLines, n)
	}
	return nil
}

// OwnerBriefLines counts the lines of a brief. A trailing newline is not an
// extra empty line.
func OwnerBriefLines(body string) int {
	body = strings.TrimRight(body, "\r\n")
	if strings.TrimSpace(body) == "" {
		return 0
	}
	return strings.Count(body, "\n") + 1
}

// OwnerBriefRequest carries the authenticated caller's session and the tool
// arguments. The session is filled in by the daemon, never by the agent.
type OwnerBriefRequest struct {
	SessionID string `json:"session_id"`
	OwnerBriefArgs
}

// OwnerBriefResult is the receipt of a delivery or the schedule of the timer.
type OwnerBriefResult struct {
	ItemID       string `json:"item_id,omitempty"`
	Date         string `json:"date,omitempty"`
	BriefTime    string `json:"brief_time,omitempty"`
	TimeZone     string `json:"time_zone,omitempty"`
	NextRunLocal string `json:"next_run_local,omitempty"`
	NextRunUTC   string `json:"next_run_utc,omitempty"`
	LastDateSent string `json:"last_date_sent,omitempty"`
	LastWakeDate string `json:"last_wake_date,omitempty"`
	LastItemID   string `json:"last_item_id,omitempty"`
	Status       string `json:"status"`
}

// BriefSourceRef is the source key of the brief of one owner-local date.
func BriefSourceRef(date string) string { return briefSourcePrefix + date }

package protocol

import (
	"errors"
	"strings"
	"time"
)

// ParseFollowUp reads follow_up_at: an RFC 3339 time, or a duration such as
// 45m that counts from now. A duration must be positive.
func ParseFollowUp(raw string, now time.Time) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, errors.New("follow_up_at is empty")
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.UTC(), nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return time.Time{}, errors.New("follow_up_at is an RFC 3339 time (2026-11-20T07:30:00Z) or a positive duration such as 45m")
	}
	return now.Add(d).UTC(), nil
}

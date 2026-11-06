package protocol

import (
	"errors"
	"strings"
	"time"
)

const MachineLogMaxLines = 1000
const MachineLogMaxBytes = 256 << 10

// MachineLogRequest never accepts a command or a path.
type MachineLogRequest struct {
	Machine string `json:"machine"`
	Log     string `json:"log"`
	Since   string `json:"since,omitempty"`
	Filter  string `json:"filter,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}

type MachineLogResult struct {
	Machine   string   `json:"machine"`
	Log       string   `json:"log"`
	Since     string   `json:"since"`
	Lines     []string `json:"lines"`
	Truncated bool     `json:"truncated"`
}

// NormalizeMachineLog validates requests at both ends of the relay. An absolute
// timestamp on the wire makes the same window apply at both machines.
func NormalizeMachineLog(q MachineLogRequest, now time.Time) (MachineLogRequest, time.Time, error) {
	q.Machine = strings.TrimSpace(q.Machine)
	if q.Machine == "" || len(q.Machine) > 256 {
		return q, time.Time{}, errors.New("machine name or ID required")
	}
	if q.Log != "daemon" && q.Log != "gateway" {
		return q, time.Time{}, errors.New("log must be daemon or gateway")
	}
	if len(q.Filter) > 1024 || len(q.Since) > 128 {
		return q, time.Time{}, errors.New("log filter or since is too long")
	}
	if q.Limit == 0 {
		q.Limit = 200
	}
	if q.Limit < 1 || q.Limit > MachineLogMaxLines {
		return q, time.Time{}, errors.New("limit must be between 1 and 1000")
	}
	if q.Since == "" {
		q.Since = "1h"
	}
	var since time.Time
	if duration, err := time.ParseDuration(q.Since); err == nil {
		if duration <= 0 {
			return q, time.Time{}, errors.New("since duration must be positive")
		}
		since = now.Add(-duration)
	} else {
		since, err = time.Parse(time.RFC3339, q.Since)
		if err != nil {
			return q, time.Time{}, errors.New("since must be an RFC3339 time or a positive duration")
		}
	}
	if since.After(now) {
		return q, time.Time{}, errors.New("since cannot be in the future")
	}
	q.Since = since.UTC().Format(time.RFC3339Nano)
	return q, since, nil
}

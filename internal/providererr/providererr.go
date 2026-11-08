// Package providererr classifies errors that a model provider returns, and
// reads the reset time out of a usage-limit error. The daemon uses it for
// worker failures and for the limit shown on a live session.
package providererr

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

var local = []string{"sqlerror", "sqlite", "database", "tool.error", "tool error"}

var modelMarkers = []string{"not exist", "no access", "not have access", "model not found", "model_not_found", "model unavailable", "model not available", "unknown model", "invalid model", "issue with the selected model"}
var authMarkers = []string{"401", "403", "unauthorized", "forbidden", "not logged in", "authentication", "invalid api key", "invalid_api_key"}

// limitMarkers are the errors that mean the account is out of quota or rate:
// the session can answer again after a reset.
var limitMarkers = []string{"429", "usage limit", "usage_limit", "session limit", "rate limit", "rate_limit", "quota", "too many requests", "resource_exhausted", "credit balance"}
var capacityMarkers = []string{"at capacity", "overloaded"}
var apiMarkers = []string{"500", "502", "503", "504", "529", "api error", "api_error", "internal server error", "bad gateway", "service unavailable", "overloaded_error", "anthropic api"}

func hasAny(s string, markers []string) bool {
	for _, m := range markers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

// Failure tells whether text is a provider error: model not found or no
// access, auth (401/403), quota, rate limit or capacity, or another API error.
func Failure(text string) bool {
	s := strings.ToLower(text)
	if hasAny(s, local) {
		return false
	}
	return hasAny(s, modelMarkers) || hasAny(s, authMarkers) || hasAny(s, limitMarkers) || hasAny(s, capacityMarkers) || hasAny(s, apiMarkers)
}

// Limited is a usage limit with the time it ends.
type Limited struct {
	Until  time.Time
	Reason string
}

// DefaultWait is used when the error names no reset time.
const DefaultWait = time.Hour

// maxWait caps a parsed wait so a misread number cannot park a session for good.
const maxWait = 8 * 24 * time.Hour

var (
	hitYour   = regexp.MustCompile(`(?i)hit (?:your|the) ([A-Za-z0-9][A-Za-z0-9 .\-]{0,30}?) (usage limit|rate limit|session limit|limit)`)
	relative  = regexp.MustCompile(`(?i)(?:try again|retry|resets?|available again)(?: after)? in\s+(.{1,60})`)
	unitPair  = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*(hours?|hrs?|h|minutes?|mins?|m|seconds?|secs?|s)\b`)
	retryAft  = regexp.MustCompile(`(?i)retry[-_ ]after"?\s*[:=]?\s*"?(\d+)`)
	clockTime = regexp.MustCompile(`(?i)resets?(?: at| by)?\s+(\d{1,2})(?::(\d{2}))?\s*(am|pm)?(?:\s*\(([A-Za-z_/+\-0-9]+)\))?`)
)

// Limit reports whether text is a usage or rate limit, and when it ends,
// counted from at. The reset time comes from "try again in ~N min", a
// retry-after number of seconds or "resets 8am (UTC)"; otherwise it is one
// hour after at.
func Limit(text string, at time.Time) (Limited, bool) {
	s := strings.ToLower(text)
	if text == "" || hasAny(s, local) || !hasAny(s, limitMarkers) {
		return Limited{}, false
	}
	l := Limited{Until: at.Add(DefaultWait), Reason: reasonOf(text)}
	if d, ok := relativeWait(text); ok {
		l.Until = at.Add(d)
	} else if m := retryAft.FindStringSubmatch(text); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil && n > 0 {
			l.Until = at.Add(clampWait(time.Duration(n) * time.Second))
		}
	} else if t, ok := clockReset(text, at); ok {
		l.Until = t
	}
	return l, true
}

func clampWait(d time.Duration) time.Duration {
	if d <= 0 {
		return DefaultWait
	}
	return min(d, maxWait)
}

func relativeWait(text string) (time.Duration, bool) {
	m := relative.FindStringSubmatch(text)
	if m == nil {
		return 0, false
	}
	var total time.Duration
	for _, p := range unitPair.FindAllStringSubmatch(m[1], -1) {
		n, err := strconv.ParseFloat(p[1], 64)
		if err != nil {
			continue
		}
		switch strings.ToLower(p[2])[0] {
		case 'h':
			total += time.Duration(n * float64(time.Hour))
		case 'm':
			total += time.Duration(n * float64(time.Minute))
		case 's':
			total += time.Duration(n * float64(time.Second))
		}
	}
	if total <= 0 {
		return 0, false
	}
	return clampWait(total), true
}

// clockReset reads "resets 8am (UTC)" or "resets at 15:30". Without a zone
// the clock is the daemon's local time, as Claude Code prints it.
func clockReset(text string, at time.Time) (time.Time, bool) {
	m := clockTime.FindStringSubmatch(text)
	if m == nil {
		return time.Time{}, false
	}
	hour, _ := strconv.Atoi(m[1])
	minute := 0
	if m[2] != "" {
		minute, _ = strconv.Atoi(m[2])
	}
	switch strings.ToLower(m[3]) {
	case "am":
		if hour == 12 {
			hour = 0
		}
	case "pm":
		if hour < 12 {
			hour += 12
		}
	case "":
		if m[2] == "" { // "resets 8" is not a time
			return time.Time{}, false
		}
	}
	if hour > 23 || minute > 59 {
		return time.Time{}, false
	}
	loc := time.Local
	if m[4] != "" {
		if l, err := time.LoadLocation(m[4]); err == nil {
			loc = l
		}
	}
	local := at.In(loc)
	t := time.Date(local.Year(), local.Month(), local.Day(), hour, minute, 0, 0, loc)
	if !t.After(at) {
		t = t.AddDate(0, 0, 1)
	}
	if t.Sub(at) > maxWait {
		return time.Time{}, false
	}
	return t, true
}

func reasonOf(text string) string {
	if m := hitYour.FindStringSubmatch(text); m != nil {
		return strings.TrimSpace(m[1]) + " " + strings.ToLower(m[2])
	}
	s := strings.ToLower(text)
	switch {
	case strings.Contains(s, "usage limit") || strings.Contains(s, "usage_limit"):
		return "usage limit"
	case strings.Contains(s, "session limit"):
		return "session limit"
	case strings.Contains(s, "quota") || strings.Contains(s, "credit balance") || strings.Contains(s, "resource_exhausted"):
		return "quota"
	}
	return "rate limit"
}

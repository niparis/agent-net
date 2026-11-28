package protocol

import (
	"testing"
	"time"
)

func TestParseFollowUp(t *testing.T) {
	now := time.Date(2026, 11, 20, 5, 0, 0, 0, time.UTC)
	for raw, want := range map[string]time.Time{
		"45m":                       now.Add(45 * time.Minute),
		"1h30m":                     now.Add(90 * time.Minute),
		"2026-11-20T07:30:00Z":      time.Date(2026, 11, 20, 7, 30, 0, 0, time.UTC),
		"2026-11-20T09:30:00+02:00": time.Date(2026, 11, 20, 7, 30, 0, 0, time.UTC),
	} {
		got, err := ParseFollowUp(raw, now)
		if err != nil || !got.Equal(want) {
			t.Errorf("ParseFollowUp(%q) = %v, %v; want %v", raw, got, err, want)
		}
	}
	for _, raw := range []string{"", "soon", "-5m", "0s", "tomorrow 9am"} {
		if _, err := ParseFollowUp(raw, now); err == nil {
			t.Errorf("ParseFollowUp(%q) accepted", raw)
		}
	}
}

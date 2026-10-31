package tui

import (
	"testing"
	"time"
)

func TestActivityBand(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name           string
		age            time.Duration
		previous       int
		online, urgent bool
		want           int
	}{
		{"busy", time.Hour, 3, true, true, 0},
		{"recent", time.Minute, 3, true, false, 1},
		{"first boundary margin", 11 * time.Minute, 1, true, false, 1},
		{"first boundary passed", 13 * time.Minute, 1, true, false, 2},
		{"second boundary margin", 65 * time.Minute, 2, true, false, 2},
		{"second boundary passed", 71 * time.Minute, 2, true, false, 3},
		{"old", 25 * time.Hour, -1, true, false, 4},
		{"offline", time.Minute, 1, false, false, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := activityBand(now, now.Add(-tc.age), tc.previous, tc.online, tc.urgent); got != tc.want {
				t.Fatalf("got %d want %d", got, tc.want)
			}
		})
	}
}

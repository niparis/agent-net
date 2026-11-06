package protocol

import (
	"testing"
	"time"
)

func TestNormalizeMachineLogTimeAndLimits(t *testing.T) {
	now := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct{ since, want string }{{"", "2026-01-02T11:00:00Z"}, {"30m", "2026-01-02T11:30:00Z"}, {"2026-01-02T08:30:00-02:00", "2026-01-02T10:30:00Z"}} {
		q, _, err := NormalizeMachineLog(MachineLogRequest{Machine: "box", Log: "daemon", Since: tc.since}, now)
		if err != nil || q.Since != tc.want || q.Limit != 200 {
			t.Errorf("since %q: %+v %v", tc.since, q, err)
		}
	}
	for _, tc := range []MachineLogRequest{{Machine: "box", Log: "secret"}, {Machine: "box", Log: "daemon", Limit: 1001}, {Machine: "box", Log: "daemon", Since: "-1h"}, {Machine: "box", Log: "daemon", Since: "tomorrow"}, {Machine: "box", Log: "daemon", Since: "2026-01-03T00:00:00Z"}} {
		if _, _, err := NormalizeMachineLog(tc, now); err == nil {
			t.Errorf("accepted %+v", tc)
		}
	}
}

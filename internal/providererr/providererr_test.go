package providererr

import (
	"testing"
	"time"
)

func TestPiUsageLimitText(t *testing.T) {
	at := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
	l, ok := Limit("Error: You have hit your ChatGPT usage limit (plus plan). Try again in ~102 min.", at)
	if !ok {
		t.Fatal("not a limit")
	}
	if want := at.Add(102 * time.Minute); !l.Until.Equal(want) {
		t.Fatalf("until %v, want %v", l.Until, want)
	}
	if l.Reason != "ChatGPT usage limit" {
		t.Fatalf("reason %q", l.Reason)
	}
}

func TestLimitResetForms(t *testing.T) {
	at := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
	for name, c := range map[string]struct {
		text string
		want time.Time
	}{
		"hours and minutes": {"rate limit reached. Try again in 1 hour 5 minutes", at.Add(65 * time.Minute)},
		"retry after":       {"429 Too Many Requests, retry-after: 90", at.Add(90 * time.Second)},
		"clock utc":         {"You've hit your session limit · resets 8am (UTC)", time.Date(2026, 11, 9, 8, 0, 0, 0, time.UTC)},
		"clock later today": {"usage limit, resets at 15:30 (UTC)", time.Date(2026, 11, 8, 15, 30, 0, 0, time.UTC)},
		"no time":           {"quota exceeded", at.Add(time.Hour)},
	} {
		l, ok := Limit(c.text, at)
		if !ok || !l.Until.Equal(c.want) {
			t.Errorf("%s: got %v %v, want %v", name, l.Until, ok, c.want)
		}
	}
}

func TestNotALimit(t *testing.T) {
	for _, s := range []string{"model not found", "401 unauthorized", "API Error: Connection lost", "sqlite database is locked, rate limit", "", "overloaded_error"} {
		if _, ok := Limit(s, time.Now()); ok {
			t.Errorf("%q is not a usage limit", s)
		}
	}
}

func TestFailureKeepsOldMarkers(t *testing.T) {
	for _, s := range []string{"model not found", "401", "429 too many", "503 service unavailable", "overloaded"} {
		if !Failure(s) {
			t.Errorf("%q should be a provider failure", s)
		}
	}
	if Failure("tool.error 429") || Failure("plain text") {
		t.Error("local errors and plain text are no provider failure")
	}
}

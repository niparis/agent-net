package upgrade

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseReleaseAndIsNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"v0.10.0", "v0.9.9", true},
		{"v0.9.9", "v0.10.0", false},
		{"v1.0.0", "v0.99.99", true},
		{"v0.3.1", "v0.3.0", true},
		{"v0.3.0", "v0.3.0", false},
		{"v0.2.0", "v0.3.0", false},
		// Only releases compare: never dev, never a describe string.
		{"v0.4.0", "dev", false},
		{"dev", "v0.4.0", false},
		{"v0.4.0", "v0.3.0-5-gabc1234", false},
		{"v0.4.0", "v0.3.0-dirty", false},
		{"v0.3.0-5-gabc1234-dirty", "v0.3.0", false},
		{"v0.4.0", "", false},
		{"0.4.0", "v0.3.0", false},
		{"v0.4", "v0.3.0", false},
	} {
		if got := IsNewer(c.a, c.b); got != c.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
	if _, ok := ParseRelease("v1.2.3"); !ok {
		t.Error("v1.2.3 is a release")
	}
	for _, v := range []string{"dev", "", "v1.2", "v1.2.3-1-gabc", "v1.2.3-dirty", "v1.2.3+meta", "1.2.3", "va.b.c"} {
		if _, ok := ParseRelease(v); ok {
			t.Errorf("%q is not a release", v)
		}
	}
}

func TestDecideAuto(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	failed := func(v string, ago time.Duration) AutoState {
		return AutoState{Failed: &AutoFailure{Version: v, At: now.Add(-ago)}}
	}
	for _, c := range []struct {
		name            string
		cur, served     string
		off             bool
		st              AutoState
		want, wantClear bool
		reason          string
	}{
		{name: "newer", cur: "v0.1.0", served: "v0.2.0", want: true},
		{name: "equal", cur: "v0.2.0", served: "v0.2.0", reason: "not newer"},
		{name: "older (never downgrade)", cur: "v0.3.0", served: "v0.2.0", reason: "not newer"},
		{name: "opt-out", cur: "v0.1.0", served: "v0.2.0", off: true, reason: "turned off"},
		{name: "dev build", cur: "dev", served: "v0.2.0", reason: "dev build"},
		{name: "empty version", cur: "", served: "v0.2.0", reason: "dev build"},
		{name: "dirty describe", cur: "v0.1.0-3-gabc-dirty", served: "v0.2.0", reason: "not a release"},
		{name: "served is dev", cur: "v0.1.0", served: "dev", reason: "not a release"},
		{name: "failed recently", cur: "v0.1.0", served: "v0.2.0", st: failed("v0.2.0", 10*time.Minute), reason: "failed"},
		{name: "failed long ago", cur: "v0.1.0", served: "v0.2.0", st: failed("v0.2.0", 61*time.Minute), want: true},
		{name: "failed just under an hour", cur: "v0.1.0", served: "v0.2.0", st: failed("v0.2.0", 59*time.Minute), reason: "failed"},
		{name: "failed another version", cur: "v0.1.0", served: "v0.3.0", st: failed("v0.2.0", time.Minute), want: true},
		{name: "held at served", cur: "v0.1.0", served: "v0.2.0", st: AutoState{Hold: "v0.2.0"}, reason: "held at v0.2.0"},
		{name: "held above served", cur: "v0.1.0", served: "v0.2.0", st: AutoState{Hold: "v0.3.0"}, reason: "held at v0.3.0"},
		{name: "newer than held", cur: "v0.1.0", served: "v0.3.0", st: AutoState{Hold: "v0.2.0"}, want: true, wantClear: true},
	} {
		d := DecideAuto(c.cur, c.served, c.off, c.st, now)
		if d.Upgrade != c.want || d.ClearHold != c.wantClear {
			t.Errorf("%s: got %+v, want upgrade=%v clear=%v", c.name, d, c.want, c.wantClear)
		}
		if !c.want && !strings.Contains(d.Reason, c.reason) {
			t.Errorf("%s: reason %q lacks %q", c.name, d.Reason, c.reason)
		}
	}
}

func TestFailureMemory(t *testing.T) {
	dir := t.TempDir()
	if st := LoadAuto(dir); st.Failed != nil || st.Hold != "" {
		t.Fatalf("empty dir should give an empty state, got %+v", st)
	}
	at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	if err := RecordFailure(dir, "v0.2.0", at, errors.New("checksum mismatch")); err != nil {
		t.Fatal(err)
	}
	st := LoadAuto(dir)
	if st.Failed == nil || st.Failed.Version != "v0.2.0" || !st.Failed.At.Equal(at) || st.Failed.Reason != "checksum mismatch" {
		t.Fatalf("failure not remembered: %+v", st.Failed)
	}
	// Inside the hour: skipped. After it: tried again.
	if DecideAuto("v0.1.0", "v0.2.0", false, st, at.Add(RetryAfter-time.Second)).Upgrade {
		t.Error("retried inside the hour")
	}
	if !DecideAuto("v0.1.0", "v0.2.0", false, st, at.Add(RetryAfter+time.Second)).Upgrade {
		t.Error("not retried after the hour")
	}
	// A failure does not erase a hold.
	SetHold(dir, "v0.5.0")
	RecordFailure(dir, "v0.6.0", at, errors.New("x"))
	if LoadAuto(dir).Hold != "v0.5.0" {
		t.Error("RecordFailure lost the hold")
	}
}

func TestHoldSetAndClear(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state", "daemon") // does not exist yet
	if err := ClearHold(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("clearing a hold that does not exist must not create files")
	}
	if err := SetHold(dir, "v0.3.0"); err != nil {
		t.Fatal(err)
	}
	st := LoadAuto(dir)
	if st.Hold != "v0.3.0" {
		t.Fatalf("hold = %q", st.Hold)
	}
	if got, want := st.HoldNote(), "auto-upgrade held at v0.3.0 after rollback"; got != want {
		t.Errorf("note = %q, want %q", got, want)
	}
	if err := ClearHold(dir); err != nil {
		t.Fatal(err)
	}
	if st := LoadAuto(dir); st.Hold != "" || st.HoldNote() != "" {
		t.Fatalf("hold not cleared: %+v", st)
	}
}

func TestBrokenStateFileIsEmpty(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "auto-upgrade.json"), []byte("{not json"), 0o600)
	if st := LoadAuto(dir); st.Hold != "" || st.Failed != nil {
		t.Fatalf("got %+v", st)
	}
}

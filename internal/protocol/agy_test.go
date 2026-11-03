package protocol

import "testing"

// agy is a known harness name (decision 0049), but workers cannot run it yet.
func TestAgyIsKnownButNotStartable(t *testing.T) {
	if !HarnessKnown("agy") {
		t.Error("agy is not a known harness name")
	}
	if WorkerHarnessSupported("agy") {
		t.Error("agy must not be startable in step 2")
	}
	for _, h := range WorkerHarnesses {
		if !HarnessKnown(h) {
			t.Errorf("%s is startable but not known", h)
		}
	}
	if HarnessKnown("gemini") {
		t.Error("gemini is a known harness name")
	}
}

// The effort is part of an agy model id, so nothing is split off or defaulted.
func TestAgyModelIdsKeepTheirEffort(t *testing.T) {
	for _, id := range []string{"gemini-3.8-flash-low", "gemini-4-argon-high", "claude-sonnet-5-5-medium", "gpt-oss-120b-medium"} {
		if name, level := SplitModelLevel(id); level != "" || name != id {
			t.Errorf("%s split into %q and %q", id, name, level)
		}
	}
}

func TestAgyModelVendor(t *testing.T) {
	for _, tt := range []struct{ model, want string }{
		{"gemini-3.8-flash-low", "google"},
		{"gemini-4-argon-high", "google"},
		{"claude-sonnet-5-5-medium", "anthropic"},
		{"gpt-oss-120b-medium", "openai"},
		{"something-else", "agy"},
	} {
		if got := ModelVendor("agy", tt.model); got != tt.want {
			t.Errorf("ModelVendor(agy, %s) = %q, want %q", tt.model, got, tt.want)
		}
	}
}

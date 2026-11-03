package protocol

import "testing"

func TestAgyWorkerHarness(t *testing.T) {
	if !HarnessKnown("agy") {
		t.Error("agy is not a known harness name")
	}
	if !WorkerHarnessSupported("agy") {
		t.Error("agy must be startable")
	}
	if !WorkerReadOnlySupported("agy") {
		t.Error("agy plan workers must support read-only access")
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

// Antigravity model IDs have no provider/ prefix. They must remain startable
// even when the subscription quota probe has not completed yet.
func TestAgyWorkerSubscription(t *testing.T) {
	for _, model := range []string{"gemini-3.8-flash-low", "claude-sonnet-5-5-medium", "gpt-oss-120b-medium"} {
		if !SubscriptionProvider("agy", model, nil) {
			t.Errorf("agy %s is a subscription", model)
		}
		if _, err := ResolveWorkerModelRankedQuota("agy", model, []string{model}, nil, nil, nil, nil, nil); err != nil {
			t.Errorf("resolve %s: %v", model, err)
		}
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

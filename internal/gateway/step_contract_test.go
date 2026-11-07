package gateway

import (
	"strings"
	"testing"
)

func TestStepContractSettingsLive(t *testing.T) {
	s, _ := newTestStore(t)
	for _, step := range []string{"accept", "review", "test", "approve", "merge", "release", "deploy", "verify"} {
		got := s.stepContract("alpha", step)
		if !strings.HasPrefix(got, "contract:\n  ") || len(got) < 40 {
			t.Fatalf("%s: %q", step, got)
		}
	}
	key := "delivery.contract.test"
	for _, change := range []SettingsChange{
		{Key: key, Level: "network", Scope: "network", Value: settingValue(`"Network tests"`)},
		{Key: key, Level: "repo", Scope: "alpha", Value: settingValue(`"Project tests"`)},
	} {
		if _, err := s.WriteSettings([]SettingsChange{change}, "cto", "contract update"); err != nil {
			t.Fatal(err)
		}
	}
	if got := s.stepContract("alpha", "test"); got != "contract:\n  Project tests" {
		t.Fatal(got)
	}
	if got := s.stepContract("beta", "test"); got != "contract:\n  Network tests" {
		t.Fatal(got)
	}
}

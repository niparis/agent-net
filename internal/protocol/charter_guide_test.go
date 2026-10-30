package protocol

import (
	"strings"
	"testing"
)

func TestCharterGuideCoversEveryAttribute(t *testing.T) {
	guide := CharterGuide()
	want := []string{"purpose", "maturity", "accepts", "refuses", "feature_requests", "consumers", "escalate", "needs", "notes"}
	if len(guide) != len(want) {
		t.Fatalf("guide has %d attributes, want %d", len(guide), len(want))
	}
	for i, attr := range want {
		entry := guide[i]
		if entry.Name != attr {
			t.Errorf("attribute %d = %q, want %q", i, entry.Name, attr)
		}
		for field, value := range map[string]string{
			"meaning": entry.Meaning,
			"reader":  entry.Reader,
			"size":    entry.Size,
			"good":    entry.Good,
			"bad":     entry.Bad,
		} {
			if strings.TrimSpace(value) == "" {
				t.Errorf("%s has no %s", attr, field)
			}
		}
	}
	accepts, ok := CharterGuideFor("accepts")
	if !ok || !strings.Contains(accepts.Meaning, "other agents") || !strings.Contains(accepts.Good, "Bug fixes in retrieval") || !strings.Contains(accepts.Bad, "Bearer-authenticated") {
		t.Fatalf("accepts guide misses the work boundary or examples: %+v", accepts)
	}
}

func TestCharterValueWarningForTooDetailedValues(t *testing.T) {
	guide, ok := CharterGuideFor("needs")
	if !ok {
		t.Fatal("needs guide missing")
	}
	for _, value := range []string{
		strings.Repeat("a", 201),
		"Reach 192.168.50.65 for the service",
		"Use bearer sk-abcdefghijklmnopqrstuvwxyz123456",
		"Read /home/project/private/config",
	} {
		warning := CharterValueWarning("needs", value)
		if !strings.Contains(warning, "too long/too detailed for a charter") || !strings.Contains(warning, "keep it to "+guide.Size) || !strings.Contains(warning, "put detail in AGENTS.md") {
			t.Errorf("warning for %q = %q", value, warning)
		}
	}
	for _, value := range []string{"quota-axi, gateway and cogforge", "Read the service's setup guide"} {
		if warning := CharterValueWarning("needs", value); warning != "" {
			t.Errorf("unexpected warning for %q: %q", value, warning)
		}
	}
	if warning := CharterValueWarning("unknown", "value"); warning != "" {
		t.Errorf("unexpected warning for unknown attribute: %q", warning)
	}
}

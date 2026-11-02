package difficulty

import (
	"math"
	"strings"
	"testing"
)

func TestParseProfile(t *testing.T) {
	valid := []byte(`{"reasoning":{"closest_pattern":"copy","new_logic":"none","evidence":["internal/a.go:2-4"]},"scope":{"components":["a"],"distinct_changes":["one"],"repeated_changes":[]},"coupling":{"contracts":[],"invariants":[],"evidence":["inspected: no shared contract"]},"uncertainty":{"open_questions":[],"inspected":["inspected: config and callers"]},"verification":{"tests":["unit"],"needed_checks":[],"gaps":[]},"risk":{"failures":[],"reversibility":"easy","evidence":["docs/a.md:3"]},"confidence":"high"}`)
	if _, err := ParseProfile(valid); err != nil {
		t.Fatalf("valid profile: %v", err)
	}
	cases := []struct{ name, raw string }{
		{"unknown field", strings.Replace(string(valid), `"confidence":"high"`, `"extra":1,"confidence":"high"`, 1)},
		{"missing dimension", strings.Replace(string(valid), `"risk":{"failures":[],"reversibility":"easy","evidence":["docs/a.md:3"]},`, "", 1)},
		{"bad confidence", strings.Replace(string(valid), `"confidence":"high"`, `"confidence":"maybe"`, 1)},
		{"bad evidence", strings.Replace(string(valid), `"internal/a.go:2-4"`, `"a.go:lines"`, 1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseProfile([]byte(tc.raw)); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestParseScores(t *testing.T) {
	got, insufficient, err := ParseScores([]byte(`{"reasoning":2,"scope":4,"coupling":2,"uncertainty":1,"verification":2,"risk":2,"reasons":{"reasoning":"internal/a.go:2"}}`))
	if err != nil || len(insufficient) != 0 || got.Reasoning != 2 || got.Scope != 4 {
		t.Fatalf("scores=%+v insufficient=%v err=%v", got, insufficient, err)
	}
	_, insufficient, err = ParseScores([]byte("INSUFFICIENT_EVIDENCE: coupling - inspect shared callers"))
	if err != nil || len(insufficient) != 1 || insufficient[0] != "coupling" {
		t.Fatalf("insufficient=%v err=%v", insufficient, err)
	}
	for _, n := range []int{0, 6} {
		raw := []byte(`{"reasoning":1,"scope":1,"coupling":1,"uncertainty":1,"verification":1,"risk":1}`)
		raw = []byte(strings.Replace(string(raw), `"reasoning":1`, `"reasoning":`+string(rune('0'+n)), 1))
		if _, _, err := ParseScores(raw); err == nil {
			t.Errorf("score %d accepted", n)
		}
	}
}

func TestLabel(t *testing.T) {
	s := Scores{Reasoning: 2, Scope: 4, Coupling: 2, Uncertainty: 1, Verification: 2, Risk: 2}
	label, d, _ := Label(s)
	if label != "simple" || math.Abs(d-2.05) > 1e-12 {
		t.Fatalf("Label=%s D=%v", label, d)
	}
	for _, tc := range []struct {
		d    float64
		want string
	}{{1.499, "trivial"}, {1.5, "simple"}, {2.25, "normal"}, {3, "hard"}, {3.75, "very_complex"}} {
		if got := labelForD(tc.d); got != tc.want {
			t.Errorf("D=%v: %s, want %s", tc.d, got, tc.want)
		}
	}
	if got, _, _ := Label(Scores{1, 1, 1, 1, 1, 1, nil}); got != "trivial" {
		t.Errorf("ones=%s", got)
	}
	if got, _, _ := Label(Scores{5, 5, 5, 5, 5, 5, nil}); got != "very_complex" {
		t.Errorf("fives=%s", got)
	}
	for i, name := range []string{"reasoning", "scope", "coupling", "uncertainty", "verification", "risk"} {
		scores := Scores{1, 1, 1, 1, 1, 1, nil}
		switch i {
		case 0:
			scores.Reasoning = 5
		case 1:
			scores.Scope = 5
		case 2:
			scores.Coupling = 5
		case 3:
			scores.Uncertainty = 5
		case 4:
			scores.Verification = 5
		case 5:
			scores.Risk = 5
		}
		got, _, b := Label(scores)
		if got != "hard" && got != "very_complex" {
			t.Errorf("%s score 5 gave %s", name, got)
		}
		if !strings.Contains(b, name) {
			t.Errorf("bottleneck %q missing %s", b, name)
		}
	}
}

func labelForD(d float64) string { return labelNames[labelIndex(d)] }

func TestParseProfileAcceptsFreeFormInspected(t *testing.T) {
	raw := `{"reasoning":{"closest_pattern":"copy","new_logic":"none","evidence":["internal/a.go:2-4"]},"scope":{"components":["a"],"distinct_changes":["one"],"repeated_changes":[]},"coupling":{"contracts":[],"invariants":[],"evidence":["inspected: no shared contract"]},"uncertainty":{"open_questions":[],"inspected":["internal/tui/keys.go","searched for apps.rename"]},"verification":{"tests":["unit"],"needed_checks":[],"gaps":[]},"risk":{"failures":[],"reversibility":"easy","evidence":["docs/a.md:3"]},"confidence":"high"}`
	if _, err := ParseProfile([]byte(raw)); err != nil {
		t.Fatalf("free-form inspected entries refused: %v", err)
	}
}

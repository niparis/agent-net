package protocol

import (
	"strings"
	"testing"
)

func TestReviewRiskAndSensitiveTriggers(t *testing.T) {
	for _, tc := range []struct {
		name, difficulty, sensitive, note string
		risk                              int
		required                          bool
	}{
		{name: "normal", difficulty: "normal"},
		{name: "unknown"},
		{name: "risk below", difficulty: "normal", risk: 3},
		{name: "hard", difficulty: "hard", required: true},
		{name: "very complex", difficulty: "very_complex", required: true},
		{name: "risk four", difficulty: "normal", risk: 4, required: true, note: "required: risk 4"},
		{name: "risk five", risk: 5, required: true, note: "required: risk 5"},
		{name: "migrations", sensitive: "migrations", required: true, note: "required: touches migrations"},
		{name: "authority", sensitive: "internal/gateway/auth.go", required: true, note: "required: touches internal/gateway/auth.go"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, c := PipelineToday(), testChange()
			c.Difficulty, c.Risk, c.SensitivePath = tc.difficulty, tc.risk, tc.sensitive
			ev := []Evidence{pass(c, refAccept, 1, nil), pass(c, refTest, 2, map[string]string{FieldSuite: SuiteChanged})}
			ready, missing := p.Ready(refMerge, c, ev)
			if ready == tc.required {
				t.Fatalf("ready=%v missing=%v required=%v", ready, missing, tc.required)
			}
			for _, st := range p.Status(c, nil) {
				if st.Step == StepReview && tc.note != "" && !strings.Contains(st.Note, tc.note) {
					t.Errorf("review note=%q want %q", st.Note, tc.note)
				}
			}
			if tc.required {
				ev = append(ev, pass(c, refReview, 3, map[string]string{FieldIndependent: IndependentFamily}))
				if ok, missing := p.Ready(refMerge, c, ev); !ok {
					t.Fatalf("review pass did not unblock merge: %v", missing)
				}
			}
		})
	}
}

package tui

import (
	"github.com/niparis/agent-net/internal/protocol"
	"strings"
	"testing"
)

func TestWorkerAssessmentDetailGolden(t *testing.T) {
	m := newModel(120, 40)
	for i := range m.snap.Agents {
		if m.snap.Agents[i].SessionID == "s_0000000000000002" {
			m.snap.Agents[i].Worker = &protocol.WorkerInfo{ID: "w_test", Owner: "s_0000000000000001", Project: "strategies", Assessment: &protocol.WorkerAssessment{Reasoning: 2, Scope: 1, Coupling: 1, Uncertainty: 1, Verification: 1, Risk: 1, D: 1.2, Label: "trivial", PickerLabel: "normal"}}
		}
	}
	m.selected = "s_0000000000000002"
	v := detailText(m)
	if !strings.Contains(v, "R2 S1 C1 U1 V1 K1 · D 1.20 · trivial (picker: normal)") {
		t.Fatalf("missing difficulty: %s", v)
	}
	golden(t, "worker_assessment_detail", v)
}

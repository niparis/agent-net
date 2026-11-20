package tui

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// A live session whose last turn failed shows it in its row and its detail.
func TestSessionTurnFailedScene(t *testing.T) {
	const cause = "AI.Error: misalignment_policy_violation: This request was blocked by our safety systems."
	for _, size := range []struct{ w, h int }{{80, 24}, {120, 34}} {
		m := quietFixtures(newModel(size.w, size.h))
		m.activeOnly = false
		a := &m.snap.Agents[0]
		a.Status = "idle"
		a.Profile.Task = "ingest the forge batch"
		a.TurnError, a.TurnErrorAt = cause, 1700000000
		m.selected = a.SessionID
		view := m.testView()
		// The list row cuts the cause to its column; the detail panel, when the
		// window has room for it, shows it whole.
		if !strings.Contains(view, "turn failed") {
			t.Fatalf("%dx%d: no failure in the view:\n%s", size.w, size.h, view)
		}
		golden(t, "sessions_turn_failed_"+itoa(size.w)+"x"+itoa(size.h), view)
	}
}

func TestSessionWithoutAFailedTurnShowsNoFailure(t *testing.T) {
	m := Model{}
	if got := m.sessionTaskText(protocol.AgentView{Online: true, Status: "idle"}); strings.Contains(got, "turn failed") {
		t.Fatalf("task = %q", got)
	}
	if got := m.sessionTaskText(protocol.AgentView{Online: false, TurnError: "x"}); strings.Contains(got, "turn failed") {
		t.Fatalf("an offline session shows the failure: %q", got)
	}
}

func TestDetailPaneShowsTheWholeFailedTurnCause(t *testing.T) {
	m, src := workerModel(140, 40)
	const target = "s_0000000000000001"
	const cause = "AI.Error: misalignment_policy_violation: This request was blocked by our safety systems. Reason: Potentially unintended activity."
	for i := range src.snap.Agents {
		if src.snap.Agents[i].SessionID == target {
			src.snap.Agents[i].TurnError = cause
			src.snap.Agents[i].TurnErrorAt = 1700000000
		}
	}
	s := strings.ReplaceAll(strings.Join(strings.Fields(detailText(selectID(refresh(m, src), target))), " "), " │ ", " ")
	if !strings.Contains(s, "turn failed 22:13: "+cause) {
		t.Fatalf("detail lacks the cause:\n%s", s)
	}
}

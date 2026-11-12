package mcpserver

import (
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// An agent sees only its own workers by default (started_by me), and only the
// running ones (state running).
func TestWorkersFindDefaultsToMyRunningWorkers(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "n_me")
	f := roReadFixture()
	cs := connect(t, f.call)

	out := roCall(t, cs, "agent_workers_find", map[string]any{})
	items := roPageItems(t, out)
	if len(items) != 1 {
		t.Fatalf("an agent sees %d workers by default, want its one running worker: %+v", len(items), items)
	}
	if got := items[0]["id"]; got != "s_w_mine" {
		t.Fatalf("worker id = %v, want s_w_mine", got)
	}
	if caller := items[0]["caller"].(map[string]any); caller["id"] != "s_me" || caller["name"] != "me" {
		t.Fatalf("caller = %+v", caller)
	}
	if items[0]["state"] != "running" || items[0]["run_time"] == "" {
		t.Fatalf("worker row = %+v", items[0])
	}

	all := roCall(t, cs, "agent_workers_find", map[string]any{"started_by": "anyone", "state": "any"})
	if got := len(roPageItems(t, all)); got != 3 {
		t.Fatalf("started_by anyone state any shows %d workers, want 3", got)
	}
	if total := int(all["total"].(float64)); total != 3 {
		t.Fatalf("total = %d, want 3", total)
	}
}

// The cursor of one filter set is refused with another.
func TestWorkersCursorRefusedWithOtherFilters(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "n_me")
	f := roReadFixture()
	cs := connect(t, f.call)
	out := roCall(t, cs, "agent_workers_find", map[string]any{"started_by": "anyone", "state": "any", "limit": 1})
	cursor, _ := out["next_cursor"].(string)
	if cursor == "" {
		t.Fatal("no cursor")
	}
	res, err := cs.CallTool(t.Context(), roToolCall("agent_workers_find", map[string]any{"started_by": "me", "state": "any", "cursor": cursor}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("a worker cursor from another filter set was accepted")
	}
}

func TestWorkerFullDetailIncludesAssessment(t *testing.T) {
	assessment := &protocol.WorkerAssessment{Reasoning: 2, Scope: 1, Coupling: 1, Uncertainty: 1, Verification: 1, Risk: 1, D: 1.2, Label: "trivial", RulesVersion: "2026-11-02.1", PickerLabel: "normal"}
	a := protocol.AgentView{SessionID: "s_test", Worker: &protocol.WorkerInfo{Assessment: assessment}}
	full, _ := roWorkerRowOf(a, nil, true)
	if full.Assessment != assessment {
		t.Fatalf("full row: %+v", full)
	}
	compact, _ := roWorkerRowOf(a, nil, false)
	if compact.Assessment != nil {
		t.Fatalf("compact row: %+v", compact)
	}
}

// The recorded cause of the latest run is shown by detail full only.
func TestWorkerFullDetailIncludesCause(t *testing.T) {
	a := protocol.AgentView{SessionID: "s_test", Worker: &protocol.WorkerInfo{Cause: protocol.CauseQuotaAuth}}
	full, _ := roWorkerRowOf(a, nil, true)
	if full.Cause != protocol.CauseQuotaAuth {
		t.Fatalf("full row cause = %q", full.Cause)
	}
	compact, _ := roWorkerRowOf(a, nil, false)
	if compact.Cause != "" {
		t.Fatalf("compact row has cause %q", compact.Cause)
	}
}

package daemon

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// The CTO's agent_workers_find with detail full shows what each worker's pick
// read of the quota windows (decision 0191), read once for the page through
// the ordinary agent's pick_quota read. Concise does not read it.
func TestCTOWorkersFindFullShowsTheQuotaRead(t *testing.T) {
	state := seededStateDir(t)
	st, err := openState(state)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.close() })
	lines := []string{"quota reading of 2026-11-04 11:58 UTC, 2 min before the pick"}
	var asked [][]string
	d := &Daemon{cfg: Config{StateDir: state, NodeID: "n_test"}, st: st, workers: map[string]*worker{}, log: log.New(io.Discard, "", 0)}
	d.cfg.testGatewayCall = func(_ context.Context, typ string, params any) (json.RawMessage, bool, error) {
		switch typ {
		case "query.agents":
			raw, err := json.Marshal(protocol.AgentsResult{Agents: []protocol.AgentView{{SessionID: "s_w", NodeID: "n_test", Harness: "codex",
				Worker: &protocol.WorkerInfo{ID: "w_1", Owner: "s_owner", Project: "alpha", StartedAt: 1}}}})
			return raw, true, err
		case "query.pick_quota":
			q := params.(protocol.QueryPickQuota)
			asked = append(asked, q.Sessions)
			raw, err := json.Marshal(protocol.PickQuotaResult{Picks: map[string]protocol.PickQuotaView{"s_w": {RunID: "r_1", Lines: lines}}})
			return raw, true, err
		}
		return nil, false, nil
	}
	read := func(detail string) map[string]any {
		t.Helper()
		out, err := d.readCTOCatalogTool(t.Context(), "agent_workers_find", json.RawMessage(`{"state":"any","detail":"`+detail+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(out)
		if err != nil {
			t.Fatal(err)
		}
		var page struct {
			Items []map[string]any `json:"items"`
		}
		if err := json.Unmarshal(raw, &page); err != nil || len(page.Items) != 1 {
			t.Fatalf("items: %s %v", raw, err)
		}
		return page.Items[0]
	}
	if row := read("concise"); row["quota_read"] != nil || len(asked) != 0 {
		t.Fatalf("concise read the quota: %+v %v", row, asked)
	}
	row := read("full")
	got, _ := row["quota_read"].([]any)
	if len(got) != 1 || got[0] != lines[0] || len(asked) != 1 || len(asked[0]) != 1 || asked[0][0] != "s_w" {
		t.Fatalf("quota_read %v, asked %v", row["quota_read"], asked)
	}
}

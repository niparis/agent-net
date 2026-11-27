package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// handleBytes runs one gateway request and returns the encoded answer, as it
// goes on the wire to the daemon.
func handleBytes(t *testing.T, g *Gateway, node, typ string, params any) []byte {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	data, err := g.handle(context.Background(), &nodeConn{id: node}, typ, raw)
	if err != nil {
		t.Fatalf("%s: %v", typ, err)
	}
	b, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestTUIAgentsLiteCutsBytes is the byte budget of one TUI refresh. A full
// agent row carries the whole effective charter of its project. The TUI never
// draws that copy; it reads the graph for charters.
func TestTUIAgentsLiteCutsBytes(t *testing.T) {
	f := newBenchFixtureChanges(t, 0) // the agents and nodes queries do not read changes
	long := strings.Repeat("x", 600)
	for i := 0; i < 20; i++ {
		project := fmt.Sprintf("proj%02d", i)
		for _, attr := range []string{"purpose", "maturity_note", "accepts", "refuses", "feature_requests"} {
			if err := f.g.store.SetCharter(project, attr, long, "owner"); err != nil {
				t.Fatal(err)
			}
		}
		if err := f.g.store.SetCharter(project, "maturity", "mature", "owner"); err != nil {
			t.Fatal(err)
		}
	}
	full := handleBytes(t, f.g, f.nodes[0], "query.agents", protocol.QueryAgents{IncludeOwners: true})
	lite := handleBytes(t, f.g, f.nodes[0], "query.agents", map[string]any{"include_owners": true, "lite": true})
	t.Logf("query.agents full=%d lite=%d", len(full), len(lite))
	if len(lite) >= len(full)*50/100 {
		t.Fatalf("lite agents is %d bytes of %d, want under half", len(lite), len(full))
	}
	var got protocol.AgentsResult
	if err := json.Unmarshal(lite, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Agents) == 0 || got.Agents[0].Charter.Purpose == "" || got.Agents[0].Profile.Name == "" {
		t.Fatalf("lite rows lost what the list draws: %+v", got.Agents)
	}
	if got.Agents[0].Charter.Effective != nil {
		t.Fatal("lite rows still carry the effective charter")
	}
}

// TestTUIVersionMatchesSendNoAgentsOrNodes says a safety refresh whose topic
// did not move costs almost nothing.
func TestTUIVersionMatchesSendNoAgentsOrNodes(t *testing.T) {
	f := newBenchFixtureChanges(t, 0) // the agents and nodes queries do not read changes
	for _, tc := range []struct {
		name   string
		typ    string
		params map[string]any
		ver    uint64
	}{
		{"agents", "query.agents", map[string]any{"include_owners": true}, f.g.topicVersions()[protocol.TopicAgents]},
		{"nodes", "query.nodes", map[string]any{}, f.g.topicVersions()[protocol.TopicNodes]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			full := handleBytes(t, f.g, f.nodes[0], tc.typ, tc.params)
			tc.params["if_version"] = tc.ver
			unchanged := handleBytes(t, f.g, f.nodes[0], tc.typ, tc.params)
			t.Logf("%s full=%d unchanged=%d", tc.typ, len(full), len(unchanged))
			if len(unchanged) >= len(full)/50 {
				t.Fatalf("unchanged %s is %d bytes of %d, want under 2%%", tc.typ, len(unchanged), len(full))
			}
		})
	}
}

// changeBytes makes one open change with a long note on every step, so the
// list payload has the shape a live gateway returns.
func changeBytes(t *testing.T) (*deliveryRig, string) {
	t.Helper()
	r := newDeliveryRig(t)
	res := r.submit(r.a, r.workerSubmit("agent-net/w_1", commitA))
	id := res.Change.Change.ID
	steps := []string{"accept", "review", "test", "approve", "merge", "verify", "release", "deploy"}
	note := strings.Repeat("detail ", 700) // about 4.9 KB
	for _, step := range steps {
		if _, err := r.s.db.Exec(`INSERT INTO delivery_evidence(change_id,revision,step,for_step,after_merge,result,commit_hash,fields,by_session,by_model,note,at)
			VALUES(?,1,?,'',0,'pass',?,'{}','s_0','gpt-5.5',?,1)`, id, step, commitA, note); err != nil {
			t.Fatal(err)
		}
	}
	// One failed step keeps its finding on the list screen.
	if _, err := r.s.db.Exec(`INSERT INTO delivery_evidence(change_id,revision,step,for_step,after_merge,result,commit_hash,fields,by_session,by_model,note,at)
		VALUES(?,1,'test','',0,'fail',?,'{}','s_0','gpt-5.5','the suite fails on main too',2)`, id, commitA); err != nil {
		t.Fatal(err)
	}
	return r, id
}

func TestTUIChangeListLiteCutsBytes(t *testing.T) {
	r, id := changeBytes(t)
	full := handleBytes(t, r.g, r.a, protocol.FrameChangeList, map[string]any{"state": protocol.ChangeListActive})
	lite := handleBytes(t, r.g, r.a, protocol.FrameChangeList, map[string]any{"state": protocol.ChangeListActive, "lite": true})
	t.Logf("change.list full=%d lite=%d", len(full), len(lite))
	if len(lite) >= len(full)*60/100 {
		t.Fatalf("lite change list is %d bytes of %d, want under 60%%", len(lite), len(full))
	}
	var got protocol.ChangeListResult
	if err := json.Unmarshal(lite, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Changes) != 1 || got.Changes[0].Change.ID != id || got.Changes[0].Change.Title == "" {
		t.Fatalf("lite rows lost the list fields: %+v", got.Changes)
	}
	finding := ""
	for _, st := range got.Changes[0].Steps {
		if st.Step == "test" && st.Evidence != nil {
			finding = st.Evidence.Note
		}
	}
	if finding != "the suite fails on main too" {
		t.Fatalf("lite list lost the failed step finding: %q", finding)
	}
	version := r.s.readStamp(topicTables[protocol.TopicChanges])
	unchanged := handleBytes(t, r.g, r.a, protocol.FrameChangeList, map[string]any{"state": protocol.ChangeListActive, "if_version": version})
	t.Logf("change.list unchanged=%d", len(unchanged))
	if len(unchanged) >= len(full)/50 {
		t.Fatalf("unchanged change list is %d bytes of %d, want under 2%%", len(unchanged), len(full))
	}
	// The full read still carries the detail note, which is what opening the
	// change asks for through change.get.
	detail := handleBytes(t, r.g, r.a, protocol.FrameChangeGet, protocol.ChangeGetRequest{ChangeID: id})
	if !strings.Contains(string(detail), strings.Repeat("detail ", 10)) {
		t.Fatal("change.get no longer returns the full evidence")
	}
}

package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// Snapshot every stored value, including histories, plans and sequences. A
// rollback validator must leave all of them unchanged, even on a refusal.
func proposalSnapshot(t *testing.T, s *Store) map[string][]string {
	t.Helper()
	rows, err := s.db.Query(`SELECT name FROM sqlite_schema WHERE type='table'`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	out := map[string][]string{}
	for _, name := range names {
		rows, err := s.db.Query(fmt.Sprintf("SELECT * FROM %q", name))
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			values := make([]any, len(columns))
			dest := make([]any, len(columns))
			for i := range values {
				dest[i] = &values[i]
			}
			if err := rows.Scan(dest...); err != nil {
				t.Fatal(err)
			}
			b, err := json.Marshal(values)
			if err != nil {
				t.Fatal(err)
			}
			out[name] = append(out[name], string(b))
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		sort.Strings(out[name])
	}
	return out
}

func TestCTOProposalValidationLeavesDatabaseUnchanged(t *testing.T) {
	g, node := scopeGateway(t)
	st := g.store
	if err := st.SetNodeHello(node, "test", "1"); err != nil {
		t.Fatal(err)
	}
	launch := true
	if err := st.SetNodeLaunch(node, &launch); err != nil {
		t.Fatal(err)
	}
	own, err := st.UpsertSession(node, protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	a, err := st.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "first", Repo: protocol.RepoInfo{Project: "alpha"}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "second", Repo: protocol.RepoInfo{Project: "beta"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetNodeHarnesses(node, []protocol.HarnessInfo{{Name: "claude", Models: []string{"model"}}}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddArchitect("architect", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := st.AddWorkerProfile(protocol.WorkerProfile{Name: "profile", Harness: "claude", Model: "model"}); err != nil {
		t.Fatal(err)
	}
	request, _, err := st.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "work"})
	if err != nil {
		t.Fatal(err)
	}
	reply, _, err := st.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, ParentID: request.ID, Body: "no", ReplyOutcome: protocol.ReplyOutcome{Outcome: "reject", Reason: "outside charter"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetEdge(protocol.Edge{From: "alpha", To: "beta", Kind: "depends", Rule: "ask"}, "owner"); err != nil {
		t.Fatal(err)
	}
	held, _, err := st.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "held"})
	if err != nil {
		t.Fatal(err)
	}
	item, err := st.CreateInboxItem(protocol.InboxItem{Kind: "approval", SessionID: a, Text: "approve work"})
	if err != nil {
		t.Fatal(err)
	}
	// The connection has no wire: attempting a probe or invocation would panic.
	nc := &nodeConn{id: node}
	g.conns[node] = nc
	t.Cleanup(func() { delete(g.conns, node) })
	tests := []struct {
		name string
		args any
	}{
		{"node_add", map[string]any{"name": "new"}}, {"node_remove", map[string]any{"name": "architect"}},
		{"charter_set", map[string]any{"node": "alpha", "attr": "purpose", "value": "test"}},
		{"edge_set", map[string]any{"from": "alpha", "to": "beta", "kind": "depends", "rule": "auto"}},
		{"edge_set", map[string]any{"from": "/delivery/merger", "to": "alpha", "kind": "releases"}},
		{"edge_remove", map[string]any{"from": "alpha", "to": "beta", "kind": "depends"}},
		{"graph_undo", map[string]any{}}, {"node_settings_set", map[string]any{"node": "alpha", "setting": "merge_authority", "value": "ask"}},
		{"session_rename", map[string]any{"session_id": a, "name": "planner"}},
		{"session_rename", map[string]any{"session_id": a, "name": ""}},
		{"session_trust", map[string]any{"session_id": a, "on": true}},
		{"session_policy", map[string]any{"session_id": a, "policy": map[string]any{"delegate": "off"}}},
		{"node_rename", map[string]any{"node_id": node, "name": "name"}},
		{"project_display_set", map[string]any{"project": "alpha", "name": "name"}},
		{"project_remove", map[string]any{"name": "alpha"}},
		{"held_answer", map[string]any{"message_id": held.ID, "action": "a"}},
		{"held_answer", map[string]any{"message_id": held.ID, "action": "R"}},
		{"inbox_answer", map[string]any{"id": item.ID, "answer": "approve"}},
		{"outcome_overrule", map[string]any{"reply_id": reply.ID, "action": "overrule"}},
		{"worker_profile_add", map[string]any{"name": "new", "harness": "claude", "model": "model"}},
		{"worker_profile_remove", map[string]any{"name": "profile"}},
		{"mcp_add", map[string]any{"server": map[string]any{"name": "new", "transport": "stdio", "command": "/bin/true"}}},
		{"mcp_remove", map[string]any{"name": "files"}}, {"mcp_test", map[string]any{"name": "files"}},
		{"mcp_enable", map[string]any{"name": "files", "enabled": false}},
		{"mcp_call", map[string]any{"server": "files", "tool": "a", "arguments": map[string]any{}}},
		{"playbook_set", map[string]any{"model": "model", "note": "test"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := proposalSnapshot(t, st)
			raw, _ := json.Marshal(map[string]any{"tool": tt.name, "args": tt.args, "owner": own})
			if _, err := g.validateCTOProposal(context.Background(), nc, raw); err != nil {
				t.Fatal(err)
			}
			after := proposalSnapshot(t, st)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("validation changed stored state")
			}
		})
	}
	t.Run("automatic worker selection", func(t *testing.T) {
		if _, err := st.db.Exec(`INSERT INTO owner_settings(key,value,updated_at) VALUES('model_scores',?,0) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, `[{"match":"model","score":23.3,"source":"owner"}]`); err != nil {
			t.Fatal(err)
		}
		before := proposalSnapshot(t, st)
		_, err := g.workerStart(context.Background(), nc, protocol.WorkerStartFrame{Owner: own, Level: "trivial", WorkerStartRequest: protocol.WorkerStartRequest{Project: "alpha", Task: "test", Harness: "claude"}}, true)
		if err != nil {
			t.Fatal(err)
		}
		if after := proposalSnapshot(t, st); !reflect.DeepEqual(before, after) {
			t.Fatal("worker validation changed stored state")
		}
	})

}

func TestCTOProposalValidationTeams(t *testing.T) {
	g, node := scopeGateway(t)
	st := g.store
	if _, err := st.db.Exec(`UPDATE graph_ref_gate SET enabled=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	st.graphRefsOn.Store(true)
	own, err := st.UpsertSession(node, protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	nc := &nodeConn{id: node}
	g.conns[node] = nc
	t.Cleanup(func() { delete(g.conns, node) })

	// Add an agent to be a member
	if _, err := st.db.Exec(`INSERT INTO agents(id, display_name, created_at, updated_at) VALUES('agent-1', 'Agent 1', 1, 1)`); err != nil {
		t.Fatal(err)
	}

	// 1. Validate team_create
	before := proposalSnapshot(t, st)
	raw, _ := json.Marshal(map[string]any{
		"tool":  "team_create",
		"args":  map[string]any{"name": "test-team", "purpose": "Testing"},
		"owner": own,
	})
	if _, err := g.validateCTOProposal(context.Background(), nc, raw); err != nil {
		t.Fatalf("validate team_create: %v", err)
	}
	after := proposalSnapshot(t, st)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("team_create changed database")
	}

	// Now actually create the team so rename/archive/member actions can be tested
	if err := st.WriteTeam(protocol.TeamWrite{Action: "create", ID: "test-team", Name: "test-team", Purpose: "Testing"}, "owner"); err != nil {
		t.Fatal(err)
	}

	// 2. Validate team_member_add
	before = proposalSnapshot(t, st)
	raw, _ = json.Marshal(map[string]any{
		"tool":  "team_member_add",
		"args":  map[string]any{"team": "test-team", "member": map[string]any{"kind": "agent", "id": "agent-1"}},
		"owner": own,
	})
	if _, err := g.validateCTOProposal(context.Background(), nc, raw); err != nil {
		t.Fatalf("validate team_member_add: %v", err)
	}
	after = proposalSnapshot(t, st)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("team_member_add changed database")
	}

	// 3. Validate team_rename
	before = proposalSnapshot(t, st)
	raw, _ = json.Marshal(map[string]any{
		"tool":  "team_rename",
		"args":  map[string]any{"team": "test-team", "name": "renamed-team"},
		"owner": own,
	})
	if _, err := g.validateCTOProposal(context.Background(), nc, raw); err != nil {
		t.Fatalf("validate team_rename: %v", err)
	}
	after = proposalSnapshot(t, st)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("team_rename changed database")
	}

	// 4. Validate team_archive (no members yet)
	before = proposalSnapshot(t, st)
	raw, _ = json.Marshal(map[string]any{
		"tool":  "team_archive",
		"args":  map[string]any{"team": "test-team"},
		"owner": own,
	})
	if _, err := g.validateCTOProposal(context.Background(), nc, raw); err != nil {
		t.Fatalf("validate team_archive: %v", err)
	}
	after = proposalSnapshot(t, st)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("team_archive changed database")
	}
}

package daemon

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/niparis/agent-net/internal/mcpserver"
	"github.com/niparis/agent-net/internal/protocol"
)

func TestCTOChangeParity(t *testing.T) {
	old := map[string]struct{ tool, args string }{
		"charter_set":             {"graph_change", `{"action":"charter_set","node":"component","attr":"purpose","value":"review"}`},
		"node_add":                {"graph_change", `{"action":"node_add","name":"architect"}`},
		"node_remove":             {"graph_change", `{"action":"node_remove","name":"architect"}`},
		"edge_set":                {"graph_change", `{"action":"edge_set","from":"first","to":"second","kind":"depends","rule":"auto"}`},
		"edge_remove":             {"graph_change", `{"action":"edge_remove","from":"first","to":"second","kind":"depends"}`},
		"graph_undo":              {"graph_change", `{"action":"undo","id":1}`},
		"node_settings_set":       {"graph_change", `{"action":"settings_set","node":"component","setting":"merge_authority","value":"ask"}`},
		"machine_remove":          {"settings_change", `{"area":"machine_remove","node_id":"n_test"}`},
		"machine_rotate":          {"settings_change", `{"area":"machine_rotate","node_id":"n_test"}`},
		"node_rename":             {"settings_change", `{"area":"machine_name","node_id":"n_test","name":"test"}`},
		"session_start":           {"session_change", `{"action":"session_start","project":"garden"}`},
		"session_rename":          {"session_change", `{"action":"rename","session_id":"s_test","name":"planner"}`},
		"session_trust":           {"session_change", `{"action":"trust","session_id":"s_test","on":true}`},
		"worker_start":            {"worker_change", `{"action":"start","project":"project","task":"test"}`},
		"worker_stop":             {"worker_change", `{"action":"stop","session":"s_test"}`},
		"held_answer":             {"answer", `{"kind":"held","message_id":"m_test","action":"a"}`},
		"inbox_answer":            {"answer", `{"kind":"inbox","id":"i_test","answer":"yes"}`},
		"outcome_overrule":        {"answer", `{"kind":"outcome","reply_id":"m_test","action":"let_stand"}`},
		"mcp_add":                 {"mcp_change", `{"action":"add","server":{"name":"test","transport":"stdio","command":"/bin/true"}}`},
		"mcp_remove":              {"mcp_change", `{"action":"remove","name":"test"}`},
		"mcp_test":                {"mcp_change", `{"action":"test","name":"test"}`},
		"mcp_enable":              {"mcp_change", `{"action":"enable","name":"test","enabled":true}`},
		"mcp_call":                {"mcp_call", `{"server":"test","tool":"send","arguments":{"body":"stored"}}`},
		"integrations_set":        {"settings_change", `{"area":"integrations","peer_messages":false}`},
		"notification_settings":   {"settings_change", `{"area":"notifications","disabled":true}`},
		"cto_settings":            {"settings_change", `{"area":"cto","title":"CTO"}`},
		"cto_new":                 {"cto_new", `{}`},
		"protected_providers_set": {"settings_change", `{"area":"protected_providers","protected_providers":["provider"]}`},
		"worker_providers_set":    {"settings_change", `{"area":"worker_providers","providers":["provider"]}`},
		"score_set":               {"settings_change", `{"area":"score","model":"model","score":50}`},
		"worker_profile_add":      {"settings_change", `{"area":"worker_profile_add","name":"profile","harness":"claude","model":"model"}`},
		"worker_profile_remove":   {"settings_change", `{"area":"worker_profile_remove","name":"profile"}`},
		"playbook_set":            {"settings_change", `{"area":"playbook","model":"model","note":"tests"}`},
		"project_create":          {"project_change", `{"action":"create","path":"~/garden"}`},
		"project_add":             {"project_change", `{"action":"add","paths":["/tmp/project"]}`},
		"project_remove":          {"project_change", `{"action":"remove","name":"project"}`},
		"project_display_set":     {"project_change", `{"action":"display","project":"project","name":"Project"}`},
		"invite":                  {"settings_change", `{"area":"invite"}`},
		"onboarding_restart":      {"settings_change", `{"area":"onboarding"}`},
	}
	for oldName, call := range old {
		t.Run(oldName, func(t *testing.T) {
			if oldName != call.tool {
				for _, name := range mcpserver.CTOToolNames() {
					if name == oldName {
						t.Fatalf("old name %s is still advertised", oldName)
					}
				}
			}
			class, err := ctoClass(call.tool, json.RawMessage(call.args))
			if err != nil || (class != ctoOwnerOnly && class != ctoMinor) {
				t.Fatalf("class=%s err=%v", class, err)
			}
			concrete, args, _, err := ctoConcreteCall(call.tool, json.RawMessage(call.args))
			if err != nil {
				t.Fatal(err)
			}
			if concrete != oldName {
				t.Fatalf("merged %s reached %s, want %s", call.tool, concrete, oldName)
			}
			var got map[string]any
			if err := json.Unmarshal(args, &got); err != nil {
				t.Fatal(err)
			}
			if _, ok := got[ctoActionField[call.tool]]; ok {
				t.Fatalf("routing field left in %s", args)
			}
			if err := mcpserver.CTOArguments(concrete, args); err != nil {
				t.Fatalf("concrete schema: %v", err)
			}
		})
	}
}

func TestCTOChangeUnknownActionFailsClosed(t *testing.T) {
	for _, name := range []string{"ui", "graph_change", "session_change", "worker_change", "answer", "mcp_change", "settings_change", "project_change"} {
		field := ctoActionField[name]
		if _, err := ctoClass(name, json.RawMessage(`{"`+field+`":"unknown"}`)); err == nil {
			t.Errorf("%s accepted unknown %s", name, field)
		}
	}
}

// A CTO stop of a queued worker start cancels it at once.
func TestCTOStopCancelsAQueuedStart(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	fl := &fakeLoad{l: cpuBusy()}
	w := startWorkerNode(t, g, queueSet, withLoad(fl))
	w.register(t, newRepo(t, "shop"))
	pi, _ := helloPi(t, w.node, "caller", "/work/caller", true)
	queued := startFrom(t, pi, protocol.WorkerStartRequest{Model: "fake/m1", Project: "shop", Harness: "opencode", Task: "test"})
	if queued.Status != "queued" {
		t.Fatalf("not queued: %+v", queued)
	}
	args, _ := json.Marshal(map[string]string{"action": "stop", "session": queued.QueueID})
	if _, err := ctoRunNow(context.Background(), w.d, "worker_change", args); err != nil {
		t.Fatal(err)
	}
	if count, _ := w.d.st.queueLen(); count != 0 {
		t.Fatal("the CTO stop did not cancel the queued start")
	}
}

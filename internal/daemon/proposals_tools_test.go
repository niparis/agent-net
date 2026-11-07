package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/mcpserver"
	"github.com/niparis/agent-net/internal/protocol"
)

var ctoProposalFixtures = map[string]string{
	"sessions_archive": `{"name":"judge-*"}`,
	"graph_change":     `{"action":"charter_set","node":"component","attr":"purpose","value":"review"}`,
	"session_change":   `{"action":"rename","session_id":"s_test","name":"planner"}`,
	"worker_change":    `{"action":"start","project":"project","task":"test","harness":"claude","touches":["tests"]}`,
	"answer":           `{"kind":"held","message_id":"m_test","action":"a"}`,
	"tools_change":     `{"action":"plan","item":"item","target":{"scope":"global"}}`,
	"mcp_change":       `{"action":"add","server":{"name":"test","transport":"stdio","command":"/bin/true"}}`,
	"mcp_call":         `{"server":"test","tool":"send","arguments":{"body":"stored"}}`,
	"settings_change":  `{"area":"cto","title":"CTO"}`,
	"project_change":   `{"action":"add","paths":["/tmp/project"]}`,
	"cto_new":          `{}`,
	"message":          `{"action":"send","as_owner":true,"to":"s_test","body":"stored"}`,
}

func TestConcreteProposalClassAndGraphReasonCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name, tool, args, concrete string
		wantErr                    bool
	}{
		{"gated graph action", "graph_change", `{"action":"node_remove","name":"old","reason":"retired"}`, "node_remove", false},
		{"legacy immediate graph action", "graph_change", `{"action":"team_create","name":"test","reason":"group work"}`, "team_create", false},
		{"legacy owner message", "message", `{"action":"send","as_owner":true,"to":"s_test","body":"hello"}`, "message", false},
		{"read", "agent_sessions_find", `{}`, "", true},
		{"talk", "cto_ui_show", `{}`, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			call, err := concreteProposal(proposalCall{Tool: tc.tool, Args: json.RawMessage(tc.args)})
			if tc.wantErr {
				if err == nil {
					t.Fatal("non-proposal call accepted")
				}
				return
			}
			if err != nil || call.Tool != tc.concrete {
				t.Fatalf("concrete call: %+v %v", call, err)
			}
			if tc.tool == "graph_change" && strings.Contains(string(call.Args), `"reason"`) {
				t.Fatalf("reason reached the concrete call: %s", call.Args)
			}
			if err := mcpserver.CTOArguments(call.Tool, call.Args); err != nil {
				t.Fatalf("concrete schema: %v", err)
			}
		})
	}
}

func TestProposalEveryChangeRunsStoredCallOnce(t *testing.T) {
	t.Parallel()
	// These fixtures represent proposals stored before the at-once policy.
	for name, args := range ctoProposalFixtures {
		t.Run(name, func(t *testing.T) {
			wantTool, wantArgs, wantAction, err := ctoConcreteCall(name, json.RawMessage(args))
			if err != nil {
				t.Fatal(err)
			}
			st, err := openState(seededStateDir(t))
			if err != nil {
				t.Fatal(err)
			}
			defer st.close()
			calls, validations := 0, 0
			p := proposalRunner{state: st, now: time.Now, validate: func(_ context.Context, call proposalCall) error {
				validations++
				normalized, err := concreteProposal(call)
				if err != nil {
					return err
				}
				return mcpserver.CTOArguments(normalized.Tool, normalized.Args)
			}, run: func(ctx context.Context, call proposalCall, actor string) (json.RawMessage, error) {
				return runStoredCTOProposal(ctx, call, actor, func(ctx context.Context, tool string, stored json.RawMessage) (any, error) {
					calls++
					if tool != wantTool || proposalActor(ctx) != actor || !strings.HasPrefix(actor, "owner (CTO proposal p_") {
						t.Fatalf("tool=%s actor=%s", tool, actor)
					}
					var want, got any
					_ = json.Unmarshal(wantArgs, &want)
					_ = json.Unmarshal(stored, &got)
					a, _ := json.Marshal(want)
					b, _ := json.Marshal(got)
					if string(a) != string(b) {
						t.Fatalf("changed call: %s != %s", a, b)
					}
					return map[string]string{"private": "result"}, nil
				})
			}}
			ctx := context.Background()
			result, err := p.propose(ctx, proposalCall{Tool: name, Action: wantAction, Args: json.RawMessage(args)}, "legacy fixture")
			if err != nil {
				t.Fatal(err)
			}
			items, err := p.list()
			if err != nil || len(items) != 1 || calls != 0 || validations != 1 || result != items[0].ID {
				t.Fatalf("items=%v calls=%d validations=%d result=%v err=%v", items, calls, validations, result, err)
			}
			if err := p.decide(ctx, items[0].ID, true); err != nil {
				t.Fatal(err)
			}
			if err := p.decide(ctx, items[0].ID, true); err == nil {
				t.Fatal("approved twice")
			}
			if calls != 1 || validations != 2 {
				t.Fatalf("calls=%d validations=%d", calls, validations)
			}
		})
	}
}

// Every concrete change that existed before part 10 is reachable through one
// merged action, and the merged call reaches the same concrete call and args.
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
		"session_start":           {"session_change", `{"action":"session_start","project":"garden","harness":"claude"}`},
		"session_rename":          {"session_change", `{"action":"rename","session_id":"s_test","name":"planner"}`},
		"session_trust":           {"session_change", `{"action":"trust","session_id":"s_test","on":true}`},
		"session_policy":          {"session_change", `{"action":"policy","session_id":"s_test","policy":{"delegate":"off"}}`},
		"worker_start":            {"worker_change", `{"action":"start","project":"project","task":"test","harness":"claude","touches":["tests"]}`},
		"worker_stop":             {"worker_change", `{"action":"stop","session":"s_test"}`},
		"held_answer":             {"answer", `{"kind":"held","message_id":"m_test","action":"a"}`},
		"inbox_answer":            {"answer", `{"kind":"inbox","id":"i_test","answer":"yes"}`},
		"outcome_overrule":        {"answer", `{"kind":"outcome","reply_id":"m_test","action":"let_stand"}`},
		"tools_plan":              {"tools_change", `{"action":"plan","item":"item","target":{"scope":"global"}}`},
		"tools_apply":             {"tools_change", `{"action":"apply","plan_id":"plan"}`},
		"tools_undo":              {"tools_change", `{"action":"undo","item":"item"}`},
		"mcp_import":              {"tools_change", `{"action":"import","candidate":"candidate"}`},
		"mcp_native_remove":       {"tools_change", `{"action":"native_remove","candidate":"candidate"}`},
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
			if err != nil || (class != ctoProposal && class != ctoMinor) {
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
	for _, name := range []string{"message", "ui", "graph_change", "session_change", "worker_change", "answer", "tools_change", "mcp_change", "settings_change", "project_change"} {
		field := ctoActionField[name]
		if _, err := ctoClass(name, json.RawMessage(`{"`+field+`":"unknown"}`)); err == nil {
			t.Errorf("%s accepted unknown %s", name, field)
		}
	}
}

func TestCTOChangeProposalsValidateAndApply(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	ctx := context.Background()
	for _, call := range []struct{ name, args string }{
		{"graph_change", `{"action":"node_add","name":"proposal-node"}`},
		{"graph_change", `{"action":"settings_set","node":"proposal-node","setting":"merge_authority","value":"ask"}`},
		{"graph_change", `{"action":"settings_set","node":"proposal-node","setting":"merge_authority","value":""}`},
		{"graph_change", `{"action":"node_remove","name":"proposal-node"}`},
		{"settings_change", `{"area":"notifications","disabled":true}`},
		{"settings_change", `{"area":"onboarding"}`},
	} {
		value, err := n.d.runCTOToolRaw(ctx, call.name, json.RawMessage(call.args))
		if err != nil {
			t.Fatalf("%s: %v", call.name, err)
		}
		class, err := ctoClass(call.name, json.RawMessage(call.args))
		if err != nil {
			t.Fatal(err)
		}
		if class == ctoProposal {
			text, ok := value.(string)
			if !ok || !strings.HasPrefix(text, "proposal p_") {
				t.Fatalf("missing proposal: %v", value)
			}
			if err := n.d.proposalRunner().decide(ctx, strings.Fields(text)[1], true); err != nil {
				t.Fatal(err)
			}
		} else {
			assertCTOImmediate(t, value)
		}
	}
	for _, call := range []struct{ name, args string }{
		{"session_change", `{"action":"rename","session_id":"missing","name":"name"}`},
		{"graph_change", `{"action":"settings_set","node":"missing","setting":"merge_authority","value":"ask"}`},
		{"worker_change", `{"action":"stop","session":"missing"}`},
		{"mcp_call", `{"server":"missing","tool":"send"}`},
		{"mcp_change", `{"action":"add","server":{"name":"bad","transport":"stdio"},"on":"missing"}`},
	} {
		if _, err := n.d.runCTOToolRaw(ctx, call.name, json.RawMessage(call.args)); err == nil {
			t.Errorf("accepted invalid %s", call.name)
		}
	}
}

func approveCTOToolResult(t *testing.T, d *Daemon, value any) json.RawMessage {
	t.Helper()
	text, ok := value.(string)
	if !ok || !strings.HasPrefix(text, "proposal p_") {
		t.Fatalf("expected proposal, got %#v", value)
	}
	id := strings.Fields(text)[1]
	if err := d.proposalRunner().decide(context.Background(), id, true); err != nil {
		t.Fatal(err)
	}
	items, err := d.proposalRunner().list()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.ID == id {
			return item.Result
		}
	}
	t.Fatalf("missing %s", id)
	return nil
}

func TestCTOConcreteValidatorCoverage(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	for name, args := range ctoProposalFixtures {
		t.Run(name, func(t *testing.T) {
			if name == "project_change" {
				b, _ := json.Marshal(map[string]any{"action": "add", "paths": []string{newRepo(t, "project")}})
				args = string(b)
			}
			var fields map[string]any
			_ = json.Unmarshal([]byte(args), &fields)
			action, _ := fields[ctoActionField[name]].(string)
			err := n.d.validateProposal(context.Background(), proposalCall{Tool: name, Action: action, Args: json.RawMessage(args)})
			// Missing fixture targets may be refused, but every tool must have a real
			// validator rather than falling through to an unsupported-tool error.
			if err != nil && (strings.Contains(err.Error(), "unsupported proposal tool") || strings.Contains(err.Error(), "unknown CTO tool")) {
				t.Fatal(err)
			}
		})
	}
}

func TestCTOProposalQueuedStopDoesNotCancelUntilApproval(t *testing.T) {
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
	value, err := legacyProposalFixture(context.Background(), w.d, "worker_change", args)
	if err != nil {
		t.Fatal(err)
	}
	if count, _ := w.d.st.queueLen(); count != 1 {
		t.Fatal("validation cancelled queued start")
	}
	approveCTOToolResult(t, w.d, value)
	if count, _ := w.d.st.queueLen(); count != 0 {
		t.Fatal("approved stop did not cancel queued start")
	}
	if _, err := legacyProposalFixture(context.Background(), w.d, "worker_change", args); err == nil {
		t.Fatal("proposed stop of missing queue ID")
	}
}

func TestCTOProposalTrustHistoryActor(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "/usr/bin/pi"})
	_, sid := helloPi(t, n, "trust", "/work/project", true)
	args, _ := json.Marshal(map[string]any{"action": "trust", "session_id": sid, "on": true})
	value, err := legacyProposalFixture(context.Background(), n.d, "session_change", args)
	if err != nil {
		t.Fatal(err)
	}
	approveCTOToolResult(t, n.d, value)
	items, err := n.d.proposalRunner().list()
	if err != nil {
		t.Fatal(err)
	}
	changes, err := g.gw.Store().EdgeHistory("project", 10)
	if err != nil || len(changes) != 1 || changes[0].By != "owner (CTO proposal "+items[0].ID+")" {
		t.Fatalf("history: %v %v", changes, err)
	}
}

func TestCTOProposalMessageRequiresActionBeforeStoring(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "/usr/bin/pi"})
	_, sid := helloPi(t, n, "recipient", "/work/project", true)
	args, _ := json.Marshal(map[string]any{"as_owner": true, "to": sid, "body": "text"})
	if _, err := n.d.runCTOToolRaw(context.Background(), "message", args); err == nil {
		t.Fatal("stored message without an action")
	}
	items, err := n.d.proposalRunner().list()
	if err != nil || len(items) != 0 {
		t.Fatalf("invalid proposal stored: %v %v", items, err)
	}
}

func TestCTOProposalProviderPayloadLimits(t *testing.T) {
	d := &Daemon{cfg: Config{GatewayURL: "https://example.invalid", Token: "throwaway"}}
	var providers []string
	for i := 0; i < 128; i++ {
		providers = append(providers, fmt.Sprintf("%060d", i))
	}
	for _, area := range []string{"worker_providers", "protected_providers"} {
		t.Run(area, func(t *testing.T) {
			fields := map[string]any{"area": area, "providers": providers}
			if area == "protected_providers" {
				fields = map[string]any{"area": area, "protected_providers": providers, "reason": strings.Repeat("r", 4096)}
			}
			raw, _ := json.Marshal(fields)
			if err := d.validateProposal(context.Background(), proposalCall{Tool: "settings_change", Args: raw}); err == nil {
				t.Fatal("accepted payload larger than the settings endpoint limit")
			}
		})
	}
}

package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestCTOAtOnceRouting(t *testing.T) {
	cases := []struct {
		name, args string
		proposal   bool
	}{
		{"graph_change", `{"action":"team_create"}`, false},
		{"graph_change", `{"action":"edge_set","mode":"auto"}`, false},
		{"graph_change", `{"action":"settings_set","setting":"deploy_rule"}`, false},
		{"settings_change", `{"area":"protected_providers"}`, false},
		{"mcp_change", `{"action":"add"}`, false},
		{"session_change", `{"action":"rename"}`, false},
		{"sessions_archive", `{}`, false},
		{"graph_change", `{"action":"node_remove"}`, true},
		{"graph_change", `{"action":"agent_tombstone"}`, true},
		{"graph_change", `{"action":"repo_archive"}`, true},
		{"settings_change", `{"area":"machine_remove"}`, true},
		{"settings_change", `{"area":"machine_rotate"}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name+tc.args, func(t *testing.T) {
			proposed, ran := false, false
			_, err := routeCTOTool(context.Background(), tc.name, json.RawMessage(tc.args), func(context.Context, string, string, json.RawMessage, string) (string, error) {
				proposed = true
				return "p_1", nil
			}, func(context.Context, string, json.RawMessage) (any, error) { ran = true; return nil, nil })
			if err != nil || proposed != tc.proposal || ran == tc.proposal {
				t.Fatalf("proposal=%v ran=%v err=%v", proposed, ran, err)
			}
		})
	}
}

func TestCTOAtOnceGraphInbox(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	if _, err := w.d.call(context.Background(), "hello", map[string]any{"hostname": "fixture", "version": "v0.4.145", "graph_refs": true}); err != nil {
		t.Fatal(err)
	}
	waitGraphCutover(t, g)
	if err := w.d.registerCTOSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	res, err := w.d.runCTOToolRaw(context.Background(), "graph_change", json.RawMessage(`{"action":"team_create","name":"alpha","purpose":"Shared work"}`))
	if err != nil {
		t.Fatal(err)
	}
	assertCTOImmediate(t, res)
	teams, err := g.gw.Store().Teams()
	if err != nil || len(teams) != 1 || !teams[0].Active || teams[0].Name != "alpha" {
		t.Fatalf("team create: %+v %v", teams, err)
	}
}

func TestCTOTeamPurposeInboxUndo(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	if _, err := w.d.call(context.Background(), "hello", map[string]any{"hostname": "fixture", "version": "v0.4.145", "graph_refs": true}); err != nil {
		t.Fatal(err)
	}
	waitGraphCutover(t, g)
	w.cli.mustCall("team_write", map[string]any{"action": "create", "team_id": "forge", "name": "forge", "purpose": "Old purpose"})
	if err := w.d.registerCTOSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	res, err := w.d.runCTOToolRaw(context.Background(), "graph_change", json.RawMessage(`{"action":"team_purpose","team":"forge","purpose":"New purpose","reason":"finget and tenebrae joined"}`))
	if err != nil {
		t.Fatal(err)
	}
	assertCTOImmediate(t, res)
	teams, err := g.gw.Store().Teams()
	if err != nil || len(teams) != 1 || teams[0].Purpose != "New purpose" {
		t.Fatalf("purpose: %+v %v", teams, err)
	}
}

func TestCTOAsOwnerSendsAtOnce(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	_, id := helloPi(t, w.node, "receiver", "/fixture", true)
	if err := w.d.registerCTOSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"to": id, "body": "owner instruction", "as_owner": true})
	result, err := w.d.runCTOToolRaw(context.Background(), "agent_message_send", args)
	if err != nil {
		t.Fatal(err)
	}
	receipts, ok := result.(messageReceipts)
	if !ok || len(receipts.Sent) != 1 {
		t.Fatalf("receipt: %#v", result)
	}
	m, ok, err := g.gw.Store().Message(receipts.Sent[0].MessageID)
	if err != nil || !ok || !m.FromCTO || !m.OwnerApproved {
		t.Fatalf("message: %+v %v", m, err)
	}
	if got := codexFrame(m); !strings.Contains(got, protocol.CTONote) {
		t.Fatalf("missing owner note: %q", got)
	}
}

func TestCTOSettingsAtOnceInboxUndo(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	ctx := context.Background()
	if err := n.d.registerCTOSession(ctx); err != nil {
		t.Fatal(err)
	}
	result, err := n.d.runCTOToolRaw(ctx, "settings_change", json.RawMessage(`{"area":"values_write","changes":[{"key":"workers.auto_continue","level":"network","scope":"network","value":false}],"reason":"test"}`))
	if err != nil {
		t.Fatal(err)
	}
	assertCTOImmediate(t, result)
}

func TestCTOAtOnceActionsLeaveInbox(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	useHelper(t)
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	cli := cliClient(t, n)
	ctx := context.Background()
	_, sid := helloPi(t, n, "rename-test", "/work/test", true)
	cli.mustCall("node_add", map[string]string{"name": "first"})
	cli.mustCall("node_add", map[string]string{"name": "second"})
	if _, err := g.gw.Store().AddProjects(n.cfg.NodeID, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "display-project", Path: "/work/display-project"}}}); err != nil {
		t.Fatal(err)
	}
	if err := n.d.registerCTOSession(ctx); err != nil {
		t.Fatal(err)
	}
	offline, err := g.gw.Store().UpsertSession(n.cfg.NodeID, protocol.SessionInfo{Harness: "pi", NativeID: "offline", Cwd: "/tmp/offline"})
	if err != nil {
		t.Fatal(err)
	}
	if err = g.gw.Store().SetSessionOffline(n.cfg.NodeID, offline); err != nil {
		t.Fatal(err)
	}
	cli.mustCall(protocol.OpProtectedProvidersSet, protocol.ProtectedProviders{Providers: []string{"old"}, Reason: "before"})
	server := map[string]any{"name": "cto-test", "transport": "stdio", "command": os.Args[0], "args": []string{"-test.run=XXX"}, "env": []string{helperEnv, "GORACE"}}
	cases := []struct {
		name string
		args any
		undo string
	}{
		{"graph_change", map[string]any{"action": "edge_set", "from": "first", "to": "second", "kind": "depends", "rule": "auto"}, "graph_undo"},
		{"graph_change", map[string]any{"action": "settings_set", "node": "first", "setting": "deploy_rule", "value": "owner"}, "graph_undo"},
		{"settings_change", map[string]any{"area": "protected_providers", "protected_providers": []string{"fixture"}, "reason": "test"}, "protected_providers_set"},
		{"mcp_change", map[string]any{"action": "add", "server": server}, "mcp_remove"},
		{"mcp_change", map[string]any{"action": "enable", "name": "cto-test", "enabled": false}, "mcp_enable"},
		{"session_change", map[string]any{"action": "rename", "session_id": sid, "name": "renamed"}, "session_rename"},
		{"session_change", map[string]any{"action": "trust", "session_id": sid, "on": true}, "session_trust"},
		{"project_change", map[string]any{"action": "display", "project": "display-project", "name": "First"}, "project_display_set"},
		{"settings_change", map[string]any{"area": "notifications", "disabled": true}, ""},
		{"sessions_archive", map[string]any{"ids": []string{offline}}, "sessions_archive"},
	}
	for _, tc := range cases {
		t.Run(tc.name+tc.undo, func(t *testing.T) {
			args, _ := json.Marshal(tc.args)
			result, err := n.d.runCTOToolRaw(ctx, tc.name, args)
			if err != nil {
				t.Fatal(err)
			}
			assertCTOImmediate(t, result)
		})
	}
	items, err := n.d.proposalRunner().list()
	if err != nil || len(items) != 0 {
		t.Fatalf("proposals %+v %v", items, err)
	}
	if _, ok, err := g.gw.Store().Agent(offline); err != nil || ok {
		t.Fatalf("session not archived: %v %v", ok, err)
	}
	var info protocol.ProtectedProviders
	if err := json.Unmarshal(cli.mustCall(protocol.OpProtectedProvidersGet, nil), &info); err != nil || len(info.Providers) != 1 || info.Providers[0] != "fixture" {
		t.Fatalf("providers %+v %v", info, err)
	}
}

func TestCTOMachineActionsRemainProposals(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	owner := startNode(t, g, nodeOpts{})
	target := startNode(t, g, nodeOpts{})
	ctx := context.Background()
	for _, area := range []string{"machine_remove", "machine_rotate"} {
		args, _ := json.Marshal(map[string]any{"area": area, "node_id": target.cfg.NodeID})
		result, err := owner.d.runCTOToolRaw(ctx, "settings_change", args)
		if err != nil {
			t.Fatal(err)
		}
		text, ok := result.(string)
		if !ok || !strings.HasPrefix(text, "proposal p_") {
			t.Fatalf("%s: %v", area, result)
		}
	}
	items, err := owner.d.proposalRunner().list()
	if err != nil || len(items) != 2 {
		t.Fatalf("proposals: %+v %v", items, err)
	}
	if !target.d.Connected() {
		t.Fatal("target disconnected before owner approval")
	}
}

func TestCTOProfileChangeUndoPreservesExisting(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	ctx := context.Background()
	if err := w.d.registerCTOSession(ctx); err != nil {
		t.Fatal(err)
	}
	if err := g.gw.Store().AddWorkerProfile(protocol.WorkerProfile{Name: "review", Harness: "opencode", Model: "fake/m1", Effort: "low", AddedBy: w.ownerID()}); err != nil {
		t.Fatal(err)
	}
	args := json.RawMessage(`{"area":"worker_profile_add","name":"review","harness":"opencode","model":"fake/m1","effort":"high"}`)
	if _, err := w.d.runCTOToolRaw(ctx, "settings_change", args); err != nil {
		t.Fatal(err)
	}
	profiles, err := g.gw.Store().WorkerProfiles()
	if err != nil || len(profiles) != 1 || profiles[0].Effort != "high" {
		t.Fatalf("profile not updated: %+v %v", profiles, err)
	}
}

// The CTO's direct tools for protected providers and worker profiles change
// what settings_change does, record one action line, and update the gateway.
func TestCTODirectSettingsToolsLeaveInboxUndo(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	ctx := context.Background()
	if err := w.d.registerCTOSession(ctx); err != nil {
		t.Fatal(err)
	}
	if err := g.gw.Store().AddWorkerProfile(protocol.WorkerProfile{Name: "review", Harness: "opencode", Model: "fake/m1", Effort: "low", AddedBy: w.ownerID()}); err != nil {
		t.Fatal(err)
	}
	w.cli.mustCall(protocol.OpProtectedProvidersSet, protocol.ProtectedProviders{Providers: []string{"old"}, Reason: "before"})
	actions := func() int {
		var n int
		if err := w.d.st.db.QueryRow(`SELECT COUNT(*) FROM cto_actions`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	profile := func() protocol.WorkerProfile {
		profiles, err := g.gw.Store().WorkerProfiles()
		if err != nil || len(profiles) != 1 {
			t.Fatalf("profiles %+v %v", profiles, err)
		}
		return profiles[0]
	}
	for _, tc := range []struct {
		tool, args string
		applied    func() bool
	}{
		{"agent_protected_providers_set", `{"protected_providers":["fixture"],"reason":"test"}`,
			func() bool {
				var info protocol.ProtectedProviders
				_ = json.Unmarshal(w.cli.mustCall(protocol.OpProtectedProvidersGet, nil), &info)
				return len(info.Providers) == 1 && info.Providers[0] == "fixture"
			}},
		{"agent_worker_profile_add", `{"name":"review","harness":"opencode","model":"fake/m1","effort":"high"}`,
			func() bool { return profile().Effort == "high" }},
		{"agent_worker_profile_remove", `{"name":"review"}`,
			func() bool { profiles, err := g.gw.Store().WorkerProfiles(); return err == nil && len(profiles) == 0 }},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			beforeActions := actions()
			if _, err := w.d.runCTOTool(ctx, tc.tool, json.RawMessage(tc.args)); err != nil {
				t.Fatal(err)
			}
			if !tc.applied() {
				t.Fatal("the change was not applied as before")
			}
			if n := actions() - beforeActions; n != 1 {
				t.Fatalf("%d action lines, want 1", n)
			}
		})
	}
}

func TestCTOMachineRenameInboxUndo(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	ctx := context.Background()
	if err := n.d.registerCTOSession(ctx); err != nil {
		t.Fatal(err)
	}
	cli := cliClient(t, n)
	cli.mustCall(protocol.OpNodeRename, protocol.NodeRenameRequest{NodeID: n.cfg.NodeID, Name: "old-name"})
	args, _ := json.Marshal(map[string]any{"area": "machine_name", "node_id": n.cfg.NodeID, "name": "new-name"})
	if _, err := n.d.runCTOToolRaw(ctx, "settings_change", args); err != nil {
		t.Fatal(err)
	}
	for _, node := range nodeList(t, cli) {
		if node.NodeID == n.cfg.NodeID && node.DisplayName != "new-name" {
			t.Fatalf("name was not updated: %+v", node)
		}
	}
}

func TestCTOScoreAndIntegrationsInboxUndo(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	oldIntegrations := protocol.Integrations{HintHook: true}
	if err := g.gw.Store().SetIntegrations(oldIntegrations); err != nil {
		t.Fatal(err)
	}
	n := startNode(t, g, nodeOpts{cfg: func(c *Config) {
		c.ApplyIntegrations = func(protocol.Integrations, func(string, ...any)) error { return nil }
	}})
	cli := cliClient(t, n)
	oldScore := 40.0
	cli.mustCall(protocol.OpScoresSet, protocol.ScoreSetRequest{Model: "fixture", Score: &oldScore})
	ctx := context.Background()
	if err := n.d.registerCTOSession(ctx); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		area, args string
	}{
		{"score", `{"area":"score","model":"fixture","score":80}`},
		{"integrations", `{"area":"integrations","peer_messages":true}`},
	} {
		t.Run(tc.area, func(t *testing.T) {
			if _, err := n.d.runCTOToolRaw(ctx, "settings_change", json.RawMessage(tc.args)); err != nil {
				t.Fatal(err)
			}
		})
	}
	var scores []protocol.ModelScore
	if err := json.Unmarshal(cli.mustCall(protocol.OpScoresGet, nil), &scores); err != nil || len(scores) != 1 || scores[0].Score != 80 {
		t.Fatalf("score not updated: %+v %v", scores, err)
	}
	var integrations protocol.IntegrationsResult
	if err := json.Unmarshal(cli.mustCall(protocol.OpIntegrationsGet, nil), &integrations); err != nil || !integrations.Integrations.PeerMessages {
		t.Fatalf("integrations not updated: %+v %v", integrations, err)
	}
}

func TestCTOPlaybookInboxUndo(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	ctx := context.Background()
	if err := n.d.registerCTOSession(ctx); err != nil {
		t.Fatal(err)
	}
	cli := cliClient(t, n)
	pinned := true
	cli.mustCall(protocol.OpPlaybookSet, protocol.PlaybookSetRequest{Model: "fixture", Note: "previous note", Pin: &pinned})
	if _, err := n.d.runCTOToolRaw(ctx, "settings_change", json.RawMessage(`{"area":"playbook","model":"fixture","note":"new note"}`)); err != nil {
		t.Fatal(err)
	}
	note, ok, err := g.gw.Store().PlaybookFor("fixture", "")
	if err != nil || !ok || note.Note != "new note" {
		t.Fatalf("playbook not updated: %+v %v", note, err)
	}
}

// ctoActionCoverage lists what the class table and the routes disagree on: a
// routed action with no class, a class with no route, and a class that is not
// read, minor or proposal. Both tables are the source, so a new action needs no
// test edit.
func ctoActionCoverage(routes map[string]map[string]string, classes map[string]map[string]ctoToolClass) []string {
	tools := map[string]bool{}
	for tool := range routes {
		tools[tool] = true
	}
	for tool := range classes {
		tools[tool] = true
	}
	var names []string
	for tool := range tools {
		names = append(names, tool)
	}
	sort.Strings(names)

	var problems []string
	for _, tool := range names {
		for _, action := range sortedKeys(routes[tool]) {
			class, ok := classes[tool][action]
			if !ok {
				problems = append(problems, fmt.Sprintf("unclassified %s %s", tool, action))
				continue
			}
			switch class {
			case ctoRead, ctoMinor, ctoProposal:
			default:
				problems = append(problems, fmt.Sprintf("unknown class %q for %s %s", class, tool, action))
			}
		}
		for _, action := range sortedKeys(classes[tool]) {
			if _, ok := routes[tool][action]; !ok {
				problems = append(problems, fmt.Sprintf("class without route %s %s", tool, action))
			}
		}
	}
	return problems
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func TestCTOActionClassCoverage(t *testing.T) {
	for _, problem := range ctoActionCoverage(ctoChangeActions, ctoActionClasses) {
		t.Error(problem)
	}
}

// TestCTOActionCoverageSeesNewActions proves the check reads both tables: an
// action in one table alone is reported, and the same action in both passes.
func TestCTOActionCoverageSeesNewActions(t *testing.T) {
	route := map[string]map[string]string{"mcp_change": {"fixture_action": "fixture.call"}}
	class := map[string]map[string]ctoToolClass{"mcp_change": {"fixture_action": ctoMinor}}
	for _, tc := range []struct {
		name    string
		routes  map[string]map[string]string
		classes map[string]map[string]ctoToolClass
		want    string
	}{
		{"route without class", route, map[string]map[string]ctoToolClass{}, "unclassified mcp_change fixture_action"},
		{"class without route", map[string]map[string]string{}, class, "class without route mcp_change fixture_action"},
		{"unknown class", route, map[string]map[string]ctoToolClass{"mcp_change": {"fixture_action": "talk"}}, "unknown class"},
		{"classed and routed", route, class, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			problems := ctoActionCoverage(tc.routes, tc.classes)
			if tc.want == "" {
				if len(problems) != 0 {
					t.Fatalf("want no problem, got %v", problems)
				}
				return
			}
			if len(problems) != 1 || !strings.Contains(problems[0], tc.want) {
				t.Fatalf("want %q, got %v", tc.want, problems)
			}
		})
	}
}

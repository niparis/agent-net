package daemon

import (
	"context"
	"encoding/json"
	"github.com/niparis/agent-net/internal/protocol"
	"os"
	"strings"
	"testing"
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
	_, err := w.d.runCTOToolRaw(context.Background(), "graph_change", json.RawMessage(`{"action":"team_create","name":"alpha","purpose":"Shared work"}`))
	if err != nil {
		t.Fatal(err)
	}
	items, err := g.gw.Store().ListInbox()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || len(items[0].Options) != 2 || items[0].Options[0] != "graph_undo" {
		t.Fatalf("want team creation and undo in inbox: %+v", items)
	}
	var undo map[string]any
	if err := json.Unmarshal([]byte(items[0].Options[1]), &undo); err != nil {
		t.Fatal(err)
	}
	w.cli.mustCall("graph_undo", undo)
	teams, err := g.gw.Store().Teams()
	if err != nil || len(teams) != 1 || teams[0].Active {
		t.Fatalf("team undo: %+v %v", teams, err)
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
	_, err := w.d.runCTOToolRaw(context.Background(), "graph_change", json.RawMessage(`{"action":"team_purpose","team":"forge","purpose":"New purpose","reason":"finget and tenebrae joined"}`))
	if err != nil {
		t.Fatal(err)
	}
	teams, err := g.gw.Store().Teams()
	if err != nil || len(teams) != 1 || teams[0].Purpose != "New purpose" {
		t.Fatalf("purpose: %+v %v", teams, err)
	}
	items, err := g.gw.Store().ListInbox()
	if err != nil || len(items) != 1 || len(items[0].Options) != 2 || items[0].Options[0] != "graph_undo" {
		t.Fatalf("want purpose change and undo in inbox: %+v %v", items, err)
	}
	if !strings.Contains(items[0].Text, "forge") || !strings.Contains(items[0].Text, "finget and tenebrae joined") {
		t.Fatalf("Inbox line: %q", items[0].Text)
	}
	var undo map[string]any
	if err := json.Unmarshal([]byte(items[0].Options[1]), &undo); err != nil {
		t.Fatal(err)
	}
	w.cli.mustCall("graph_undo", undo)
	teams, err = g.gw.Store().Teams()
	if err != nil || len(teams) != 1 || teams[0].Purpose != "Old purpose" {
		t.Fatalf("purpose undo: %+v %v", teams, err)
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
	items, err := g.gw.Store().ListInbox()
	if err != nil || len(items) != 1 || !strings.Contains(items[0].Text, "sent as owner") {
		t.Fatalf("Inbox: %+v %v", items, err)
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
	items, err := g.gw.Store().ListInbox()
	if err != nil || len(items) != 1 || len(items[0].Options) != 2 || items[0].Options[0] != "settings_undo" || !strings.Contains(items[0].Recommendation, "workers.auto_continue") {
		t.Fatalf("missing settings diff and undo: %+v %v", items, err)
	}
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
			before, err := g.gw.Store().ListInbox()
			if err != nil {
				t.Fatal(err)
			}
			args, _ := json.Marshal(tc.args)
			result, err := n.d.runCTOToolRaw(ctx, tc.name, args)
			if err != nil {
				t.Fatal(err)
			}
			assertCTOImmediate(t, result)
			after, err := g.gw.Store().ListInbox()
			if err != nil || len(after) != len(before)+1 {
				t.Fatalf("inbox %+v %v", after, err)
			}
			item := after[len(after)-1]
			if tc.undo != "" && (len(item.Options) != 2 || item.Options[0] != tc.undo) {
				t.Fatalf("undo %+v", item)
			}
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
	items, err := g.gw.Store().ListInbox()
	if err != nil || len(items) != 1 || len(items[0].Options) != 2 || items[0].Options[0] != "worker_profile_add" {
		t.Fatalf("undo: %+v %v", items, err)
	}
	var restore protocol.WorkerProfileRequest
	if err = json.Unmarshal([]byte(items[0].Options[1]), &restore); err != nil {
		t.Fatal(err)
	}
	if restore.Name != "review" || restore.Effort != "low" {
		t.Fatalf("wrong profile restore: %+v", restore)
	}
	w.cli.mustCall(protocol.OpWorkerProfileAdd, restore)
	profiles, err := g.gw.Store().WorkerProfiles()
	if err != nil || len(profiles) != 1 || profiles[0].Effort != "low" {
		t.Fatalf("profile not restored: %+v %v", profiles, err)
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
	items, err := g.gw.Store().ListInbox()
	if err != nil || len(items) != 1 || len(items[0].Options) != 2 || items[0].Options[0] != protocol.OpNodeRename || !strings.Contains(items[0].Recommendation, "old-name -> new-name") {
		t.Fatalf("missing machine rename diff and undo: %+v %v", items, err)
	}
	var undo protocol.NodeRenameRequest
	if err = json.Unmarshal([]byte(items[0].Options[1]), &undo); err != nil || undo.NodeID != n.cfg.NodeID || undo.Name != "old-name" {
		t.Fatalf("wrong rename undo: %+v %v", undo, err)
	}
	cli.mustCall(protocol.OpNodeRename, undo)
	for _, node := range nodeList(t, cli) {
		if node.NodeID == n.cfg.NodeID && node.DisplayName != "old-name" {
			t.Fatalf("name was not restored: %+v", node)
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
		area, method string
		args         string
	}{
		{"score", protocol.OpScoresSet, `{"area":"score","model":"fixture","score":80}`},
		{"integrations", protocol.OpIntegrationsSet, `{"area":"integrations","peer_messages":true}`},
	} {
		t.Run(tc.area, func(t *testing.T) {
			if _, err := n.d.runCTOToolRaw(ctx, "settings_change", json.RawMessage(tc.args)); err != nil {
				t.Fatal(err)
			}
			items, err := g.gw.Store().ListInbox()
			if err != nil || len(items) == 0 {
				t.Fatalf("Inbox: %+v %v", items, err)
			}
			item := items[len(items)-1]
			if len(item.Options) != 2 || item.Options[0] != tc.method || item.Recommendation == "" {
				t.Fatalf("missing %s diff and undo: %+v", tc.area, item)
			}
			cli.mustCall(tc.method, json.RawMessage(item.Options[1]))
		})
	}
	var scores []protocol.ModelScore
	if err := json.Unmarshal(cli.mustCall(protocol.OpScoresGet, nil), &scores); err != nil || len(scores) != 1 || scores[0].Score != oldScore {
		t.Fatalf("score not restored: %+v %v", scores, err)
	}
	var integrations protocol.IntegrationsResult
	if err := json.Unmarshal(cli.mustCall(protocol.OpIntegrationsGet, nil), &integrations); err != nil || integrations.Integrations != oldIntegrations {
		t.Fatalf("integrations not restored: %+v %v", integrations, err)
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
	items, err := g.gw.Store().ListInbox()
	if err != nil || len(items) != 1 || len(items[0].Options) != 2 || items[0].Options[0] != protocol.OpPlaybookSet || !strings.Contains(items[0].Recommendation, "previous note") {
		t.Fatalf("missing playbook diff and undo: %+v %v", items, err)
	}
	cli.mustCall(protocol.OpPlaybookSet, json.RawMessage(items[0].Options[1]))
	note, ok, err := g.gw.Store().PlaybookFor("fixture", "")
	if err != nil || !ok || note.Note != "previous note" || !note.Pinned {
		t.Fatalf("playbook not restored: %+v %v", note, err)
	}
}

func TestCTOActionClassCoverage(t *testing.T) {
	for tool, actions := range ctoChangeActions {
		for action := range actions {
			if tool == "mcp_change" && (action == "apps_list" || action == "public_status") {
				if ctoActionClasses[tool][action] != ctoRead {
					t.Errorf("app read classified as mutation: %s %s", tool, action)
				}
				continue
			}
			if class, ok := ctoActionClasses[tool][action]; !ok || (class != ctoMinor && class != ctoProposal) {
				t.Errorf("unclassified %s %s", tool, action)
			}
		}
		for action := range ctoActionClasses[tool] {
			if _, ok := actions[action]; !ok {
				t.Errorf("class without route %s %s", tool, action)
			}
		}
	}
}

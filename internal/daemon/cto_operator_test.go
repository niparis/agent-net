package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/niparis/agent-net/internal/settings"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestCTOOperatorActionsClassified(t *testing.T) {
	for method, read := range protocol.CTOOperatorMethods() {
		t.Run(method, func(t *testing.T) {
			raw, _ := json.Marshal(map[string]any{"area": "operator", "method": method, "arguments": map[string]any{}})
			got, err := ctoClass("settings_change", raw)
			want := ctoMinor
			if read {
				want = ctoRead
			}
			if err != nil || got != want {
				t.Fatalf("class %s want %s: %v", got, want, err)
			}
		})
	}
	for _, method := range []string{"apps.approve", "session_trust", "cto_graph_window", "proposal_answer", "unknown"} {
		raw, _ := json.Marshal(map[string]any{"area": "operator", "method": method, "arguments": map[string]any{}})
		if _, err := ctoClass("settings_change", raw); err == nil {
			t.Fatalf("unexpected operator route for %s", method)
		}
	}
}

func TestCTOOperatorSettingsUndo(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	ctx := context.Background()
	if err := n.d.registerCTOSession(ctx); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, get string
		args        any
	}{
		{"hidden_set", "hidden_get", map[string]any{"sessions": []string{"fixture"}}},
		{"timezone_set", "timezone_get", map[string]any{"name": "Europe/Paris"}},
		{"quality_set", "quality_get", map[string]any{"enabled": true}},
		{"difficulty_set", "difficulty_get", map[string]any{"hard": 77}},
		{"onboarding_set", "onboarding_get", map[string]any{"step": "see", "state": "done"}},
		{"worker_floors_set", "worker_floors_get", map[string]any{"floors": map[string]float64{"anthropic": 20}}},
	} {
		t.Run(tc.method, func(t *testing.T) {
			cli := cliClient(t, n)
			before := cli.mustCall(tc.get, map[string]any{})
			raw, _ := json.Marshal(map[string]any{"area": "operator", "method": tc.method, "arguments": tc.args})
			if _, err := n.d.runCTOTool(ctx, "settings_change", raw); err != nil {
				t.Fatal(err)
			}
			items, err := inboxAndLog(g.gw.Store())
			if err != nil {
				t.Fatal(err)
			}
			var notice *protocol.InboxItem
			for i := range items {
				if strings.Contains(items[i].Text, "CTO applied "+tc.method) {
					notice = &items[i]
				}
			}
			if notice == nil || len(notice.Options) != 2 {
				t.Fatalf("notice with undo missing: %+v", items)
			}
			cli.mustCall(notice.Options[0], json.RawMessage(notice.Options[1]))
			after := cli.mustCall(tc.get, map[string]any{})
			if string(before) != string(after) {
				t.Fatalf("undo: %s -> %s", before, after)
			}
			var count int
			if err := n.d.st.db.QueryRow(`SELECT COUNT(*) FROM cto_actions WHERE text=?`, "operator "+tc.method).Scan(&count); err != nil || count != 1 {
				t.Fatalf("audit: %d %v", count, err)
			}
		})
	}
}

func TestCTOOperatorEnterNeedsVisibleOwnerTUI(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	if err := n.d.registerCTOSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, sid := helloPi(t, n, "fixture", "/fixture", true)
	raw, _ := json.Marshal(map[string]any{"area": "operator", "method": "session_enter", "arguments": map[string]string{"session_id": sid}})
	_, err := n.d.runCTOTool(context.Background(), "settings_change", raw)
	if err == nil || !strings.Contains(err.Error(), "owner TUI") {
		t.Fatalf("enter without visible TUI: %v", err)
	}
}

// The same contract is exercised for every newly exposed method, including
// methods whose real backend needs a live harness or an imported skill.
func TestCTOOperatorDispatchAuditAndNotice(t *testing.T) {
	for method, read := range protocol.CTOOperatorMethods() {
		t.Run(method, func(t *testing.T) {
			for _, failed := range []bool{false, true} {
				called, audited, notices, undos := 0, 0, 0, 0
				arguments := json.RawMessage(`{"fixture":"preserved"}`)
				_, err := runOwnerOperation(method, arguments, "owner asked", func(got string, args json.RawMessage) (any, error) {
					called++
					if got != method || string(args) != string(arguments) {
						t.Fatalf("changed request %s %s", got, args)
					}
					if failed {
						return nil, fmt.Errorf("fixture failed")
					}
					return map[string]bool{"ok": true}, nil
				}, func(any) ([]ownerUndoCall, error) {
					undos++
					return []ownerUndoCall{undoCall("hidden_set", map[string]any{"sessions": []string{}})}, nil
				}, func(line string) error {
					audited++
					want := "operator " + method
					if failed {
						want = "failed " + want + ": fixture failed"
					}
					if line != want {
						t.Fatalf("audit %q want %q", line, want)
					}
					return nil
				}, func(text string, options []string) error {
					notices++
					want := "CTO applied " + method + ": owner asked"
					if method == "session_enter" || method == "cto_enter" {
						want = "CTO requested " + method + ": owner asked"
					}
					if text != want || len(options) != 2 || options[0] != "hidden_set" {
						t.Fatalf("notice %q %v", text, options)
					}
					return nil
				})
				wantNotices := 1
				if read || failed {
					wantNotices = 0
				}
				if (err != nil) != failed || called != 1 || audited != 1 || notices != wantNotices || undos != wantNotices {
					t.Fatalf("failed=%v calls=%d audits=%d notices=%d undo=%d err=%v", failed, called, audited, notices, undos, err)
				}
			}
		})
	}
}

func TestCTOOperatorMergerUndo(t *testing.T) {
	for _, action := range []string{"set", "clear"} {
		t.Run(action, func(t *testing.T) {
			g := startGateway(t, t.TempDir(), "")
			n := startNode(t, g, nodeOpts{})
			ctx := context.Background()
			if err := n.d.registerCTOSession(ctx); err != nil {
				t.Fatal(err)
			}
			cli := cliClient(t, n)
			_, err := g.gw.Store().AddProjects(n.cfg.NodeID, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "fixture", Path: "/fixture"}}})
			if err != nil {
				t.Fatal(err)
			}
			sid, err := g.gw.Store().UpsertSession(n.cfg.NodeID, protocol.SessionInfo{Harness: "claude", NativeID: "merger-fixture", Cwd: "/fixture", Repo: protocol.RepoInfo{Project: "fixture"}})
			if err != nil {
				t.Fatal(err)
			}
			request := protocol.MergerSetRequest{Project: "fixture", NodeID: n.cfg.NodeID, SessionID: sid, Configure: true}
			if action == "clear" {
				cli.mustCall("merger_set", request)
				request.SessionID = ""
			}
			before := cli.mustCall("merger_get", map[string]string{"project": "fixture"})
			graphBefore := cli.mustCall("graph_get", map[string]bool{"all": true})
			snapshot, err := g.gw.Store().SettingsSnapshot()
			if err != nil {
				t.Fatal(err)
			}
			settingBefore, err := snapshot.Resolve("delivery.pipeline.merge.requires", settings.Where{Repo: "fixture"})
			if err != nil {
				t.Fatal(err)
			}

			raw, _ := json.Marshal(map[string]any{"area": "operator", "method": "merger_set", "arguments": request})
			if _, err := n.d.runCTOTool(ctx, "settings_change", raw); err != nil {
				t.Fatal(err)
			}
			items, err := inboxAndLog(g.gw.Store())
			if err != nil {
				t.Fatal(err)
			}
			var notice *protocol.InboxItem
			for i := range items {
				if strings.HasPrefix(items[i].Text, "CTO applied merger_set ") {
					notice = &items[i]
				}
			}
			if notice == nil || len(notice.Options) != 2 {
				t.Fatalf("undo missing: %+v", items)
			}
			cli.mustCall(notice.Options[0], json.RawMessage(notice.Options[1]))
			after := cli.mustCall("merger_get", map[string]string{"project": "fixture"})
			if string(before) != string(after) {
				t.Fatalf("registrations: %s -> %s", before, after)
			}
			var graphA, graphB protocol.Graph
			_ = json.Unmarshal(graphBefore, &graphA)
			_ = json.Unmarshal(cli.mustCall("graph_get", map[string]bool{"all": true}), &graphB)
			if !reflect.DeepEqual(graphA.Edges, graphB.Edges) {
				t.Fatalf("routes: %+v -> %+v", graphA.Edges, graphB.Edges)
			}
			snapshot, err = g.gw.Store().SettingsSnapshot()
			if err != nil {
				t.Fatal(err)
			}
			settingAfter, err := snapshot.Resolve("delivery.pipeline.merge.requires", settings.Where{Repo: "fixture"})
			if err != nil {
				t.Fatal(err)
			}
			if string(settingBefore.Value) != string(settingAfter.Value) {
				t.Fatalf("pipeline setting: %s -> %s", settingBefore.Value, settingAfter.Value)
			}

		})
	}
}

func TestCTOOperatorUndoRejectsConsentBeforeFirstWrite(t *testing.T) {
	lc := &localConn{}
	_, err := lc.operatorUndo(context.Background(), json.RawMessage(`{"operations":[{"method":"hidden_set","arguments":{}},{"method":"apps.approve","arguments":{"id":"fixture"}}]}`))
	if err == nil || !strings.Contains(err.Error(), "unsupported undo operation") {
		t.Fatalf("consent undo accepted: %v", err)
	}
}

func TestCTOOperatorAllowlistCoversOwnerMethods(t *testing.T) {
	covered := protocol.CTOOperatorMethods()
	for _, routes := range ctoChangeActions {
		for _, method := range routes {
			covered[method] = false
		}
	}
	for name := range ctoToolClasses {
		covered[name] = false
	}
	for method := range ctoOwnerMethods {
		covered[method] = false
	}
	for method := range ctoLocalMethods {
		covered[method] = false
	}
	// Existing tool names differ from these owner-method aliases.
	for _, method := range strings.Fields("settings_write settings_undo scores_set tools_mcp_add tools_mcp_remove tools_mcp_test tools_mcp_list standing_orders") {
		covered[method] = false
	}
	// These are physical owner consent or UI transport, not missing backend
	// operations. The CTO requests consent with app_ask/proposal_ask.
	exceptions := map[string]string{
		"apps.approve":       "app_ask opens the owner's consent ask",
		"proposal_list":      "cto_proposals_find",
		"proposal_answer":    "proposal_ask and the owner dialog/Inbox answer",
		"proposal_start_ack": "the TUI acknowledges its actual terminal start",
		"cto_graph_window":   "the owner declares that they are driving their TUI",
		"desk_settle":        "the TUI marks a Desk log line undone after its own undo",
		"permission_undo":    "only the owner may undo a Claude Code permission approval",
		"nudge_undo":         "only the owner may undo the chief of staff nudge pause or resume",
		"sign_in_open":       "only the owner's click may open a sign-in pane",
	}
	for method := range operatorMethods {
		if _, ok := covered[method]; ok {
			continue
		}
		if exceptions[method] != "" {
			continue
		}
		t.Errorf("owner operation has no CTO route: %s", method)
	}
}

func TestCTOOperatorOwnerReadStaysPrivate(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	ctx := context.Background()
	if err := n.d.registerCTOSession(ctx); err != nil {
		t.Fatal(err)
	}
	caller, _ := helloPi(t, n, "ordinary", "/fixture", true)
	_, err := g.gw.Store().CreateInboxItem(protocol.InboxItem{Kind: "question", SessionID: n.d.ownerSessionID(), Text: "OWNER_OPERATOR_READ_CANARY"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := n.d.runCTOTool(ctx, "settings_change", json.RawMessage(`{"area":"operator","method":"inbox_list","arguments":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result)
	if !strings.Contains(string(raw), "OWNER_OPERATOR_READ_CANARY") {
		t.Fatalf("owner data missing: %s", raw)
	}
	raw, err = caller.call("inbox_list", map[string]any{})
	if err == nil || strings.Contains(string(raw), "OWNER_OPERATOR_READ_CANARY") {
		t.Fatalf("ordinary session saw owner data: %s %v", raw, err)
	}
}

func TestCTOPrivateOperatorRestoresNetworkIdentity(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	n.d.cto.pid.Store(int64(os.Getpid()))
	defer n.d.cto.pid.Store(0)
	cto := &localConn{d: n.d, client: "cto-mcp", peerPID: os.Getpid()}
	if n.d.ctoSessionID() != "" {
		t.Fatal("fixture already has a CTO identity")
	}
	_, err := cto.ctoTool(context.Background(), json.RawMessage(`{"name":"settings_change","arguments":{"area":"operator","method":"inbox_list","arguments":{}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if n.d.ctoSessionID() == "" {
		t.Fatal("private tool left the owner to register the CTO")
	}
}

func TestCTOPrivateRestoreDoesNotMoveHome(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	home := startNode(t, g, nodeOpts{})
	other := startNode(t, g, nodeOpts{})
	if err := home.d.registerCTOSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	other.d.cto.pid.Store(int64(os.Getpid()))
	defer other.d.cto.pid.Store(0)
	cto := &localConn{d: other.d, client: "cto-mcp", peerPID: os.Getpid()}
	_, err := cto.ctoTool(context.Background(), json.RawMessage(`{"name":"settings_change","arguments":{"area":"operator","method":"inbox_list","arguments":{}}}`))
	if err == nil {
		t.Fatal("private identity recovery moved the CTO to another machine")
	}
	sid, err := g.gw.Store().CTOSessionID()
	if err != nil {
		t.Fatal(err)
	}
	agent, found, err := g.gw.Store().Agent(sid)
	if err != nil || !found || agent.NodeID != home.cfg.NodeID {
		t.Fatalf("CTO home changed: %+v %v", agent, err)
	}
}

func TestCTOOperatorArchivePreviewHasNoUndo(t *testing.T) {
	for _, restore := range []bool{false, true} {
		args, _ := json.Marshal(protocol.SessionsArchiveRequest{DryRun: true, Restore: restore})
		audits := 0
		_, err := runOwnerOperation("sessions_archive", args, "preview", func(string, json.RawMessage) (any, error) {
			return protocol.SessionsArchiveResult{Count: 1}, nil
		}, func(any) ([]ownerUndoCall, error) {
			t.Fatal("preview captured an undo")
			return nil, nil
		}, func(string) error { audits++; return nil }, func(string, []string) error {
			t.Fatal("preview created a mutation notice")
			return nil
		})
		if err != nil || audits != 1 {
			t.Fatalf("preview: audits=%d err=%v", audits, err)
		}
	}
}

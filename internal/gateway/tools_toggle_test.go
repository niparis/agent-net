package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/coder/websocket"
	"github.com/niparis/agent-net/internal/inventory"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func toggleFixture(t *testing.T) (*Gateway, string, string) {
	t.Helper()
	g, err := New(Options{DataDir: seededDataDir(t), TempFolders: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.Close() })
	n, _, err := g.store.CreateNode("fixture", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if err = g.store.SetInventory(n, protocol.InventoryReport{State: "ok", Items: []protocol.InventoryItem{{Kind: "skill", Name: "demo", Hash: "abc", State: "ok", Scope: "user", Location: "/skills/demo"}}}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"one", "two"} {
		if _, err = g.store.db.Exec(`INSERT INTO projects(node_id,path,name,added_at,checked_at) VALUES(?,?,?,0,0)`, n, "/repo/"+p, p); err != nil {
			t.Fatal(err)
		}
	}
	return g, n, `["skill","demo","abc"]`
}

func TestToolToggleAssignments(t *testing.T) {
	for _, tc := range []struct {
		name            string
		before          protocol.ToolAssignment
		project         string
		on, newProjects bool
		want            protocol.ToolAssignment
	}{
		{"global off", protocol.ToolAssignment{Scope: "global"}, "one", false, true, protocol.ToolAssignment{Scope: "projects", Projects: []string{"two"}}},
		{"add", protocol.ToolAssignment{Scope: "projects", Projects: []string{"one"}}, "two", true, false, protocol.ToolAssignment{Scope: "projects", Projects: []string{"one", "two"}}},
		{"remove", protocol.ToolAssignment{Scope: "projects", Projects: []string{"one", "two"}}, "one", false, false, protocol.ToolAssignment{Scope: "projects", Projects: []string{"two"}}},
		{"last off", protocol.ToolAssignment{Scope: "projects", Projects: []string{"one"}}, "one", false, false, protocol.ToolAssignment{Scope: "uninstalled"}},
		{"uninstalled on", protocol.ToolAssignment{Scope: "uninstalled"}, "one", true, false, protocol.ToolAssignment{Scope: "projects", Projects: []string{"one"}}},
		{"all global", protocol.ToolAssignment{Scope: "projects", Projects: []string{"one"}}, "two", true, true, protocol.ToolAssignment{Scope: "global"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g, n, id := toggleFixture(t)
			tc.before.Machines = []string{n}
			tc.want.Machines = []string{n}
			if _, err := g.store.AssignTool(id, tc.before, "owner", "test"); err != nil {
				t.Fatal(err)
			}
			if err := g.store.SetToolNewProjects(protocol.ToolNewProjectsRequest{Item: id, On: tc.newProjects}, "owner", false); err != nil {
				t.Fatal(err)
			}
			a, err := g.store.ToolToggleAssignment(protocol.ToolToggleRequest{Item: id, Project: tc.project, On: tc.on})
			if err != nil || !reflect.DeepEqual(a, tc.want) {
				t.Fatalf("got %+v, %v; want %+v", a, err, tc.want)
			}
		})
	}
}

func TestToolToggleReadModelAndDefaults(t *testing.T) {
	g, n, id := toggleFixture(t)
	if _, err := g.store.UpsertSession(n, protocol.SessionInfo{Harness: "claude", NativeID: "read-model", Cwd: "/repo/seen", Repo: protocol.RepoInfo{Root: "/repo/seen", Worktree: "/repo/seen", Project: "seen"}}); err != nil {
		t.Fatal(err)
	}
	rows, err := g.store.ToolProjectStates()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || !rows[0].NewProjects || !reflect.DeepEqual(rows[0].Machines, []string{n}) || len(rows[0].Projects) != 3 || !rows[0].Projects["seen"] || !rows[0].Projects["one"] || !rows[0].Projects["two"] {
		t.Fatalf("rows: %+v", rows)
	}
	if _, err = g.store.AssignTool(id, protocol.ToolAssignment{Scope: "projects", Projects: []string{"one"}}, "owner", "test"); err != nil {
		t.Fatal(err)
	}
	// A user's default is retained when the assignment changes.
	rows, err = g.store.ToolProjectStates()
	if err != nil || !rows[0].NewProjects || rows[0].Projects["two"] {
		t.Fatalf("rows: %+v %v", rows, err)
	}
	for _, op := range []string{"tools.toggle", "tools.new_projects", "query.tools_projects"} {
		if _, err = g.handle(context.Background(), &nodeConn{id: n}, op, []byte(`{}`)); err == nil {
			t.Fatalf("%s accepted missing authority", op)
		}
	}
}

func TestToolToggleStandingChoice(t *testing.T) {
	g, n, id := toggleFixture(t)
	if _, err := g.store.AssignTool(id, protocol.ToolAssignment{Scope: "projects", Projects: []string{"one"}, Machines: []string{n}}, "owner", "test"); err != nil {
		t.Fatal(err)
	}
	if err := g.store.SetToolNewProjects(protocol.ToolNewProjectsRequest{Item: id, On: true}, "owner", false); err != nil {
		t.Fatal(err)
	}
	// Simulate a newly reported project. With an unimported skill the auto plan
	// must refuse; it must not bypass the existing import and conflict checks.
	if _, err := g.store.db.Exec(`INSERT INTO projects(node_id,path,name,added_at,checked_at) VALUES(?,?,?,0,0)`, n, "/repo/three", "three"); err != nil {
		t.Fatal(err)
	}
	if err := g.addNewSkillProjects(context.Background()); err == nil {
		t.Fatal("unimported automatic plan was applied")
	}
	rows, err := g.store.ToolProjectStates()
	if err != nil || rows[0].Projects["three"] {
		t.Fatalf("unsafe auto apply: %+v %v", rows, err)
	}
}

// A node that answers the real gateway RPC path without touching a daemon.
func toggleFakeNode(t *testing.T, g *Gateway, n string, bundle []byte, bundleErr error) {
	toggleFakeNodeProbe(t, g, n, bundle, bundleErr, nil)
}

func toggleFakeNodeProbe(t *testing.T, g *Gateway, n string, bundle []byte, bundleErr error, probe func(protocol.ToolPlanProbe) protocol.ToolPlanProbeResult) {
	t.Helper()
	accepted := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, e := websocket.Accept(w, r, nil)
		if e == nil {
			accepted <- ws
		}
	}))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	client, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	ws := <-accepted
	nc := &nodeConn{id: n, ws: ws, dead: make(chan struct{}), cancel: cancel}
	g.mu.Lock()
	g.conns[n] = nc
	g.mu.Unlock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			_, raw, e := client.Read(ctx)
			if e != nil {
				return
			}
			var h protocol.Header
			_ = json.Unmarshal(raw, &h)
			var data any = map[string]bool{"ok": true}
			var answerErr error
			switch h.Type {
			case "node.skill_bundle":
				data = protocol.SkillBundle{Data: bundle}
				answerErr = bundleErr
			case "node.tool_plan_probe":
				var p protocol.ToolPlanProbe
				_ = json.Unmarshal(raw, &p)
				data = protocol.ToolPlanProbeResult{Destination: filepath.Join(p.Checkout, p.Root, p.Name), Fingerprint: "fixture"}
				if probe != nil {
					data = probe(p)
				}
			}
			if h.ID != "" {
				b, _ := protocol.EncodeResult(h.ID, data, answerErr)
				var reply protocol.Header
				_ = json.Unmarshal(b, &reply)
				nc.answer(reply)
			}
		}
	}()
	t.Cleanup(func() {
		g.mu.Lock()
		delete(g.conns, n)
		g.mu.Unlock()
		cancel()
		client.CloseNow()
		ws.CloseNow()
		<-done
	})
}

func toggleBundle(t *testing.T, g *Gateway, n, body string) (string, []byte) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	hash, err := inventory.SkillHash(dir)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := inventory.PackSkill(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = g.store.SetInventory(n, protocol.InventoryReport{State: "ok", Items: []protocol.InventoryItem{{Kind: "skill", Name: "demo", Hash: hash, State: "ok", Scope: "user", Location: "/skills/demo"}}}); err != nil {
		t.Fatal(err)
	}
	return `["skill","demo","` + hash + `"]`, bundle
}

func TestToolToggleUnmanagedImportAndRefusal(t *testing.T) {
	for _, mode := range []string{"ok", "too large", "secret"} {
		t.Run(mode, func(t *testing.T) {
			g, n, _ := toggleFixture(t)
			body := "# Demo\nA fixture skill.\n"
			if mode == "secret" {
				body += "\nsk-" + strings.Repeat("x", 24)
			}
			id, bundle := toggleBundle(t, g, n, body)
			var refused error
			if mode == "too large" {
				refused = errors.New("skill exceeds the 2 MB import limit")
			}
			toggleFakeNode(t, g, n, bundle, refused)
			before := proposalSnapshot(t, g.store)
			// Validation must check the import without taking it into the library.
			validation := context.WithValue(t.Context(), proposalValidationKey{}, true)
			_, err := g.toggleTool(validation, protocol.ToolToggleRequest{Item: id, Project: "one", On: false}, "owner")
			if !reflect.DeepEqual(before, proposalSnapshot(t, g.store)) {
				t.Fatal("validation changed the database")
			}
			if mode == "ok" && err != nil {
				t.Fatal(err)
			}
			plan, err := g.toggleTool(t.Context(), protocol.ToolToggleRequest{Item: id, Project: "one", On: false}, "owner")
			var count int
			_ = g.store.db.QueryRow(`SELECT COUNT(*) FROM skill_versions WHERE item=?`, id).Scan(&count)
			if mode != "ok" {
				if err == nil || count != 0 {
					t.Fatalf("refused import planned: %+v, %v, versions %d", plan, err, count)
				}
				if mode == "too large" && !strings.Contains(err.Error(), "import limit") {
					t.Fatal(err)
				}
				if mode == "secret" && !strings.Contains(err.Error(), "secret") {
					t.Fatal(err)
				}
				return
			}
			if err != nil || count != 1 || plan.Assignment.Scope != "projects" || !reflect.DeepEqual(plan.Assignment.Projects, []string{"two"}) || !reflect.DeepEqual(plan.Assignment.Machines, []string{n}) {
				t.Fatalf("plan %+v, versions %d, err %v", plan, count, err)
			}
		})
	}
}

func TestToolToggleNewProjectAppliesStandingChoice(t *testing.T) {
	for _, on := range []bool{true, false} {
		t.Run(map[bool]string{true: "on", false: "off"}[on], func(t *testing.T) {
			g, n, _ := toggleFixture(t)
			id, bundle := toggleBundle(t, g, n, "# Demo\nFixture.\n")
			repo, err := g.skillRepository()
			if err != nil {
				t.Fatal(err)
			}
			var hash string
			_ = g.store.db.QueryRow(`SELECT hash FROM tool_identities WHERE id=?`, id).Scan(&hash)
			if _, err = repo.put(id, hash, bundle, n, "/skills/demo"); err != nil {
				t.Fatal(err)
			}
			if _, err = g.store.AssignTool(id, protocol.ToolAssignment{Scope: "projects", Projects: []string{"one"}, Machines: []string{n}}, "owner", "test"); err != nil {
				t.Fatal(err)
			}
			if err = g.store.SetToolNewProjects(protocol.ToolNewProjectsRequest{Item: id, On: on}, "owner", false); err != nil {
				t.Fatal(err)
			}
			// No live node is needed: offline targets remain pending in the journal.
			if _, err = g.store.UpsertSession(n, protocol.SessionInfo{Harness: "claude", NativeID: "fixture", Cwd: "/repo/three", Repo: protocol.RepoInfo{Root: "/repo/three", Worktree: "/repo/three", Project: "three"}}); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			if err = g.addNewSkillProjects(ctx); err != nil {
				t.Fatal(err)
			}
			rows, err := g.store.ToolProjectStates()
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range rows {
				if row.Item == id && row.Projects["three"] != on {
					t.Fatalf("row %+v", row)
				}
			}
			history, err := g.store.ToolHistory(id)
			if err != nil {
				t.Fatal(err)
			}
			if on {
				if len(history) != 2 || history[0].Actor != "standing choice" || history[1].Actor != "standing choice" || history[1].Action != "apply" {
					t.Fatalf("history %+v", history)
				}
			} else if len(history) != 0 {
				t.Fatalf("off rule made a plan: %+v", history)
			}
			if err = g.addNewSkillProjects(ctx); err != nil {
				t.Fatal(err)
			}
			again, _ := g.store.ToolHistory(id)
			if len(again) != len(history) {
				t.Fatal("repeated discovery made another plan")
			}
		})
	}
}

func TestToolToggleNewProjectsPlacement(t *testing.T) {
	g, n, _ := toggleFixture(t)
	id, bundle := toggleBundle(t, g, n, "# Demo\nFixture.\n")
	toggleFakeNode(t, g, n, bundle, nil)
	plan, err := g.setToolNewProjects(t.Context(), protocol.ToolNewProjectsRequest{Item: id, On: false}, "owner")
	if err != nil || plan.Assignment.Scope != "projects" || !reflect.DeepEqual(plan.Assignment.Projects, []string{"one", "two"}) {
		t.Fatalf("turning new projects off: %+v %v", plan, err)
	}
}

func TestToolToggleGatewayOwnerAndStandingActor(t *testing.T) {
	g, n, id := toggleFixture(t)
	ownerID, err := g.store.UpsertSession(n, protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: protocol.OwnerNativeID, Name: "you"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"owner": ownerID})
	if _, err = g.handle(t.Context(), &nodeConn{id: n}, "query.tools_projects", raw); err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"tools.toggle", "tools.new_projects"} {
		raw, _ = json.Marshal(map[string]any{"item": id, "project": "one", "on": false, "owner": ownerID, "actor": "standing choice"})
		if _, err = g.handle(t.Context(), &nodeConn{id: n}, op, raw); err == nil || !strings.Contains(err.Error(), "owner authority") {
			t.Fatalf("forged actor: %s %v", op, err)
		}
	}
}

func TestToolToggleNewProjectsWithoutKnownProjects(t *testing.T) {
	g, n, _ := toggleFixture(t)
	id, bundle := toggleBundle(t, g, n, "# Demo\nFixture.\n")
	toggleFakeNode(t, g, n, bundle, nil)
	if _, err := g.store.db.Exec(`DELETE FROM projects`); err != nil {
		t.Fatal(err)
	}
	plan, err := g.setToolNewProjects(t.Context(), protocol.ToolNewProjectsRequest{Item: id, On: true}, "owner")
	if err != nil || plan.Assignment.Scope != "global" {
		t.Fatalf("global choice without projects: %+v %v", plan, err)
	}
}

func TestToolToggleTwoUnmanagedSkills(t *testing.T) {
	g, n, _ := toggleFixture(t)
	id, bundle := toggleBundle(t, g, n, "# Demo\nA harmless skill.\n")
	var identity []string
	if err := json.Unmarshal([]byte(id), &identity); err != nil {
		t.Fatal(err)
	}
	items := []protocol.InventoryItem{}
	ids := []string{}
	for _, name := range []string{"alpha", "beta"} {
		items = append(items, protocol.InventoryItem{Kind: "skill", Name: name, Hash: identity[2], State: "ok", Scope: "user", Location: "/skills/" + name})
		raw, _ := json.Marshal([]string{"skill", name, identity[2]})
		ids = append(ids, string(raw))
	}
	if err := g.store.SetInventory(n, protocol.InventoryReport{State: "ok", Items: items}); err != nil {
		t.Fatal(err)
	}
	toggleFakeNode(t, g, n, bundle, nil)
	plans := []protocol.ToolPlan{}
	for _, item := range ids {
		a, err := g.store.ToolToggleAssignment(protocol.ToolToggleRequest{Item: item, Project: "one", On: false})
		if err != nil {
			t.Fatal(err)
		}
		p, err := g.planSkillAssignment(context.Background(), item, a, "owner")
		if err != nil {
			t.Fatal(err)
		}
		plans = append(plans, p)
	}
	for _, p := range plans {
		if _, err := g.applyTool(context.Background(), p.ID, "owner"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestToolSnapshotSameItemChangesAndReconnect(t *testing.T) {
	for _, change := range []string{"version", "revision", "reconnect"} {
		t.Run(change, func(t *testing.T) {
			g, n, id := toggleFixture(t)
			p, err := g.store.SaveToolPlan(id, protocol.ToolAssignment{Scope: "global"}, nil, "owner")
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "version":
				_, err = g.store.db.Exec(`INSERT INTO skill_versions(item,hash,commit_id,node_id,location,imported_at) VALUES(?,?,?,?,?,?)`, id, "new", "fixture", n, "fixture", 0)
			case "revision":
				_, err = g.store.AssignTool(id, protocol.ToolAssignment{Scope: "global"}, "owner", "test")
			case "reconnect":
				err = g.store.SetNodeHello(n, "fixture", "")
			}
			if err != nil {
				t.Fatal(err)
			}
			_, err = g.store.ApplyToolPlan(p.ID, "owner")
			if change == "reconnect" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "plan out of date") {
				t.Fatalf("expected stale plan: %v", err)
			}
		})
	}
}

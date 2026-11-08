package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/gateway"
	"github.com/niparis/agent-net/internal/protocol"
)

// This runner never invokes Tailscale or touches the owner's network.
type ctoAppsRunner struct{}

func (ctoAppsRunner) Run(_ context.Context, args ...string) (string, string, error) {
	if args[0] == "status" {
		return `{"BackendState":"Running","Self":{"DNSName":"fixture.example.ts.net.","Capabilities":["funnel"]}}`, "", nil
	}
	if args[0] == "serve" {
		return `{}`, "", nil
	}
	return "", "", nil
}

func TestCTOConnectedApps(t *testing.T) {
	for _, action := range []string{"app_grant", "app_ungrant", "app_rename", "app_revoke", "public_on", "public_off"} {
		t.Run(action, func(t *testing.T) {
			g := startGatewayWith(t, t.TempDir(), "", 0, func(o *gateway.Options) {
				o.AppsListen = "127.0.0.1:0"
				o.FunnelRunner = ctoAppsRunner{}
			})
			n := startNode(t, g, nodeOpts{})
			cli := cliClient(t, n)
			ctx := context.Background()
			if err := n.d.registerCTOSession(ctx); err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", "file:"+filepath.Join(g.dir, "gateway.db")+"?_pragma=busy_timeout(5000)")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			grants := `[]`
			if action == "app_ungrant" {
				grants = `["mcp server fixture"]`
			}
			_, err = db.Exec(`INSERT INTO apps(id,name,kind,client_id,client_name,created_at,grants) VALUES ('app_fixture','Before','bot','fixture','Fixture',1,?)`, grants)
			if err != nil {
				t.Fatal(err)
			}
			if err = g.gw.Store().AddMCPServer(protocol.MCPServer{Name: "fixture", NodeID: n.cfg.NodeID, Transport: "stdio", Command: "/bin/true", Enabled: true}); err != nil {
				t.Fatal(err)
			}
			if action == "public_off" {
				cli.mustCall("apps.public.on", map[string]any{})
			}
			args := map[string]any{"action": action}
			if strings.HasPrefix(action, "app_") {
				args["id"] = "app_fixture"
			}
			if action == "app_grant" || action == "app_ungrant" {
				args["grant"] = "mcp server fixture"
			}
			if action == "app_rename" {
				args["name"] = "After"
			}
			raw, _ := json.Marshal(args)
			result, err := n.d.runCTOTool(ctx, "mcp_change", raw)
			if err != nil {
				t.Fatal(err)
			}
			assertCTOImmediate(t, result)
			apps, err := g.gw.Store().ListApps()
			if err != nil || len(apps) != 1 {
				t.Fatalf("apps: %+v %v", apps, err)
			}
			switch action {
			case "app_grant":
				if len(apps[0].Grants) != 1 {
					t.Fatalf("grant: %+v", apps)
				}
			case "app_ungrant":
				if len(apps[0].Grants) != 0 {
					t.Fatalf("ungrant: %+v", apps)
				}
			case "app_rename":
				if apps[0].Name != "After" {
					t.Fatalf("rename: %+v", apps)
				}
			case "app_revoke":
				if apps[0].RevokedAt == nil {
					t.Fatalf("revoke: %+v", apps)
				}
			case "public_on", "public_off":
				status := cli.mustCall("apps.public.status", map[string]any{})
				var s struct {
					On bool `json:"on"`
				}
				_ = json.Unmarshal(status, &s)
				if s.On != (action == "public_on") {
					t.Fatalf("public: %s", status)
				}
			}
			items, err := inboxAndLog(g.gw.Store())
			if err != nil {
				t.Fatal(err)
			}
			var notice *protocol.InboxItem
			for i := range items {
				if strings.HasPrefix(items[i].Text, "CTO applied apps.") {
					notice = &items[i]
				}
			}
			if notice == nil {
				t.Fatalf("missing CTO notice: %+v", items)
			}
			concrete, _, _, _ := ctoConcreteCall("mcp_change", raw)
			var count int
			if err := n.d.st.db.QueryRow(`SELECT COUNT(*) FROM cto_actions WHERE text=?`, concrete).Scan(&count); err != nil || count != 1 {
				t.Fatalf("audit count %d: %v", count, err)
			}
			log, err := g.gw.Store().DeskLog(20)
			if err != nil || len(log) == 0 {
				t.Fatalf("Desk notice: %+v %v", log, err)
			}
			if action == "app_revoke" {
				if len(notice.Options) != 0 {
					t.Fatalf("revocation cannot restore tokens: %+v", notice)
				}
				return
			}
			if len(notice.Options) != 2 {
				t.Fatalf("missing undo: %+v", notice)
			}
			var undo map[string]any
			if err := json.Unmarshal([]byte(notice.Options[1]), &undo); err != nil {
				t.Fatal(err)
			}
			cli.mustCall(notice.Options[0], undo)
			apps, _ = g.gw.Store().ListApps()
			switch action {
			case "app_grant":
				if len(apps[0].Grants) != 0 {
					t.Fatalf("undo grant: %+v", apps)
				}
			case "app_ungrant":
				if len(apps[0].Grants) != 1 {
					t.Fatalf("undo ungrant: %+v", apps)
				}
			case "app_rename":
				if apps[0].Name != "Before" {
					t.Fatalf("undo rename: %+v", apps)
				}
			case "public_on", "public_off":
				var s struct {
					On bool `json:"on"`
				}
				_ = json.Unmarshal(cli.mustCall("apps.public.status", map[string]any{}), &s)
				if s.On != (action == "public_off") {
					t.Fatalf("undo public: %+v", s)
				}
			}
		})
	}
}

func TestCTOConnectedAppsReads(t *testing.T) {
	g := startGatewayWith(t, t.TempDir(), "", 0, func(o *gateway.Options) { o.FunnelRunner = ctoAppsRunner{} })
	n := startNode(t, g, nodeOpts{})
	for _, action := range []string{"apps_list", "public_status"} {
		raw, _ := json.Marshal(map[string]string{"action": action})
		if class, err := ctoClass("mcp_change", raw); err != nil || class != ctoRead {
			t.Fatalf("%s class %s %v", action, class, err)
		}
		if _, err := n.d.runCTOTool(context.Background(), "mcp_change", raw); err != nil {
			t.Fatal(err)
		}
	}
	items, _ := inboxAndLog(g.gw.Store())
	if len(items) != 0 {
		t.Fatalf("reads created notices: %+v", items)
	}
}

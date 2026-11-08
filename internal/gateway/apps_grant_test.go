package gateway

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func grantFixture(t *testing.T) (*Gateway, App, App, *nodeConn) {
	t.Helper()
	s, _ := newTestStore(t)
	g := &Gateway{store: s}
	node, _, err := s.CreateNode("research", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.AddMCPServer(mcpSpec("ingest-forge", node)); err != nil {
		t.Fatal(err)
	}
	bot, err := s.CreateApp("trade-bot", "bot", "c1", "Client", nil)
	if err != nil {
		t.Fatal(err)
	}
	ownerApp, err := s.CreateApp("phone", "owner_app", "c2", "Client", nil)
	if err != nil {
		t.Fatal(err)
	}
	return g, bot, ownerApp, &nodeConn{id: node}
}

func TestAppGrantAddRemove(t *testing.T) {
	g, bot, _, nc := grantFixture(t)
	call := func(op, grant string) error {
		raw, _ := json.Marshal(map[string]string{"app_id": bot.ID, "grant": grant})
		_, err := g.handle(context.Background(), nc, op, raw)
		return err
	}
	grants := func() []string {
		apps, err := g.store.ListApps()
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range apps {
			if a.ID == bot.ID {
				return a.Grants
			}
		}
		t.Fatal("no app")
		return nil
	}
	if err := call("apps.grant", "mcp server ingest-forge"); err != nil {
		t.Fatal(err)
	}
	// Granting twice keeps one entry.
	if err := call("apps.grant", "mcp server ingest-forge"); err != nil {
		t.Fatal(err)
	}
	if got := grants(); !reflect.DeepEqual(got, []string{"mcp server ingest-forge"}) {
		t.Fatalf("grants = %v", got)
	}
	if err := call("apps.ungrant", "mcp server ingest-forge"); err != nil {
		t.Fatal(err)
	}
	if got := grants(); len(got) != 0 {
		t.Fatalf("grants after removal = %v", got)
	}
}

func TestAppGrantRefusals(t *testing.T) {
	g, bot, ownerApp, nc := grantFixture(t)
	call := func(app, grant string) error {
		raw, _ := json.Marshal(map[string]string{"app_id": app, "grant": grant})
		_, err := g.handle(context.Background(), nc, "apps.grant", raw)
		return err
	}
	if err := call(bot.ID, "mcp server nothing"); err == nil || !strings.Contains(err.Error(), "no MCP server") {
		t.Fatalf("unknown server: %v", err)
	}
	if err := call(bot.ID, "start workers"); err == nil || !strings.Contains(err.Error(), "unknown grant") {
		t.Fatalf("unknown grant kind: %v", err)
	}
	if err := call(ownerApp.ID, "mcp server ingest-forge"); err == nil || !strings.Contains(err.Error(), "bot") {
		t.Fatalf("owner app: %v", err)
	}
	if err := call("app_missing", "mcp server ingest-forge"); err == nil {
		t.Fatal("missing app accepted")
	}
	// A stale grant, for a server that was removed, can still be taken away.
	if err := call(bot.ID, "mcp server ingest-forge"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.store.RemoveMCPServer("ingest-forge", nc.id, ""); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]string{"app_id": bot.ID, "grant": "mcp server ingest-forge"})
	if _, err := g.handle(context.Background(), nc, "apps.ungrant", raw); err != nil {
		t.Fatal(err)
	}
	apps, _ := g.store.ListApps()
	for _, a := range apps {
		if a.ID == bot.ID && len(a.Grants) != 0 {
			t.Fatalf("stale grant stays: %v", a.Grants)
		}
	}
}

func TestMCPServerGrantNames(t *testing.T) {
	if got := protocol.MCPServerGrant("ingest-forge"); got != "mcp server ingest-forge" {
		t.Fatal(got)
	}
	if name, ok := protocol.ParseMCPServerGrant("mcp server ingest-forge"); !ok || name != "ingest-forge" {
		t.Fatal(name, ok)
	}
	for _, bad := range []string{"", "mcp server ", "mcp server a b", "start workers", "mcp server Bad_Name"} {
		if _, ok := protocol.ParseMCPServerGrant(bad); ok {
			t.Errorf("%q parsed", bad)
		}
	}
}

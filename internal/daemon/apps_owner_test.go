package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/niparis/agent-net/internal/operator"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConnectedAppsOwnerGate(t *testing.T) {
	d := &Daemon{inSession: func(int) bool { return false }}
	for _, method := range []string{"apps.list", "apps.pending", "apps.approve", "apps.refuse", "apps.revoke", "apps.rename", "apps.public.status", "apps.public.on", "apps.public.off", "apps.public.url"} {
		for _, client := range []string{"pi", "opencode", "mcp"} {
			lc := &localConn{d: d, client: client}
			_, handled, err := lc.dispatchOperator(context.Background(), method, json.RawMessage(`{}`))
			if !handled || err == nil || !strings.Contains(err.Error(), "only available to the owner") {
				t.Errorf("%s %s: %v %v", method, client, handled, err)
			}
		}
	}
	for _, method := range []string{"apps.list", "apps.pending", "apps.approve", "apps.refuse", "apps.revoke", "apps.rename", "apps.public.status", "apps.public.on", "apps.public.off", "apps.public.url"} {
		lc := &localConn{d: &Daemon{inSession: func(int) bool { return true }}, client: "cli"}
		_, handled, err := lc.dispatchOperator(context.Background(), method, json.RawMessage(`{}`))
		if !handled || err == nil || !strings.Contains(err.Error(), "inside an agent session") {
			t.Errorf("%s: %v %v", method, handled, err)
		}
	}
}

func TestConnectedAppsSocketPath(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNodeCfg(t, g, nodeOpts{}, nil)
	cli := cliClient(t, n)
	raw, err := cli.call("apps.list", map[string]any{})
	if err != nil || !strings.Contains(string(raw), "apps") {
		t.Fatalf("list: %s %v", raw, err)
	}
	for i := 0; i < 5; i++ {
		_, err = cli.call("apps.pending", map[string]string{"code": "WRONGX"})
		if err == nil {
			t.Fatal("bad code accepted")
		}
	}
	if !strings.Contains(err.Error(), "too many tries") {
		t.Fatalf("missing block: %v", err)
	}
	for _, op := range []string{"apps.approve", "apps.refuse", "apps.revoke", "apps.rename"} {
		_, err = cli.call(op, map[string]string{"id": "missing", "kind": "bot", "name": "test"})
		if (err == nil && op != "apps.revoke") || (err != nil && strings.Contains(err.Error(), "unknown")) {
			t.Fatalf("%s: %v", op, err)
		}
	}
}

func TestConnectedAppsOperatorToGateway(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNodeCfg(t, g, nodeOpts{}, nil)
	db, err := sql.Open("sqlite", filepath.Join(g.dir, "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now()
	_, err = db.Exec(`INSERT INTO oauth_clients (client_id,client_name,redirect_uris,grant_types,response_types,token_endpoint_auth_method,created_at) VALUES ('c','Cursor','["https://client.example/cb"]','[]','[]','none',?)`, now.UnixNano())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []struct{ id, code string }{{"p1", "ABC234"}, {"p2", "XYZ234"}} {
		_, err = db.Exec(`INSERT INTO oauth_pending (id,user_code,client_id,redirect_uri,code_challenge,state,resource,created_at,expires_at,status) VALUES (?,?,'c','https://client.example/cb','','','',?,?,'pending')`, p.id, p.code, now.UnixNano(), now.Add(time.Minute).UnixNano())
		if err != nil {
			t.Fatal(err)
		}
	}
	c := operator.New(n.sock)
	defer c.Close()
	call := func(method string, p any) json.RawMessage {
		t.Helper()
		raw, err := c.Call(context.Background(), method, p)
		if err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		return raw
	}
	raw := call("apps.pending", map[string]string{"code": "abc-234"})
	if !strings.Contains(string(raw), "client.example") {
		t.Fatalf("pending %s", raw)
	}
	call("apps.approve", map[string]string{"id": "p1", "kind": "owner_app", "name": "phone"})
	raw = call("apps.list", struct{}{})
	var result struct {
		Apps []struct{ ID, Name, Kind string }
	}
	if err = json.Unmarshal(raw, &result); err != nil || len(result.Apps) != 1 || result.Apps[0].Name != "phone" || result.Apps[0].Kind != "owner_app" {
		t.Fatalf("apps %s %v", raw, err)
	}
	call("apps.rename", map[string]string{"id": result.Apps[0].ID, "name": "renamed phone"})
	raw = call("apps.list", struct{}{})
	if err = json.Unmarshal(raw, &result); err != nil || result.Apps[0].Name != "renamed phone" {
		t.Fatalf("renamed apps %s %v", raw, err)
	}
	call("apps.revoke", map[string]string{"id": result.Apps[0].ID})
	call("apps.refuse", map[string]string{"id": "p2"})
	var status string
	if err = db.QueryRow(`SELECT status FROM oauth_pending WHERE id='p2'`).Scan(&status); err != nil || status != "refused" {
		t.Fatalf("status %q %v", status, err)
	}
	apps, err := g.gw.Store().ListApps()
	if err != nil || len(apps) != 1 || apps[0].RevokedAt == nil {
		t.Fatalf("revoked %v %v", apps, err)
	}
}

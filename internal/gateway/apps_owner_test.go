package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestConnectedAppsOwnerOperations(t *testing.T) {
	s, _, h := oauthFixture(t)
	client := oauthRegister(t, h)
	code, id := oauthPage(t, oauthAuthorize(h, client, nil))
	g := &Gateway{store: s}
	node, _, err := s.CreateNode("example", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpsertSession(node, owner("example")); err != nil {
		t.Fatal(err)
	}
	call := func(op string, v any) (any, error) {
		raw, _ := json.Marshal(v)
		return g.handle(context.Background(), &nodeConn{id: node}, op, raw)
	}
	if _, err := call("apps.pending", map[string]string{"code": code}); err != nil {
		t.Fatal(err)
	}
	if _, err := call("apps.approve", map[string]string{"app_id": id, "kind": "bot", "name": "test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := call("apps.list", struct{}{}); err != nil {
		t.Fatal(err)
	}
	apps, err := s.ListApps()
	if err != nil || len(apps) != 1 {
		t.Fatalf("apps: %v %v", apps, err)
	}
	if _, err := call("apps.revoke", map[string]string{"app_id": apps[0].ID}); err != nil {
		t.Fatal(err)
	}
	_, id = oauthPage(t, oauthAuthorize(h, client, nil))
	if _, err := call("apps.refuse", map[string]string{"app_id": id}); err != nil {
		t.Fatal(err)
	}
}

func TestConnectedAppsBlockRemaining(t *testing.T) {
	s, c := newTestStore(t)
	g := &Gateway{store: s}
	raw := []byte(`{"code":"WRONGX"}`)
	for i := 0; i < 5; i++ {
		_, _ = g.handle(context.Background(), &nodeConn{}, "apps.pending", raw)
	}
	c.t = c.t.Add(7 * time.Minute)
	_, err := g.handle(context.Background(), &nodeConn{}, "apps.pending", raw)
	if err == nil || !strings.Contains(err.Error(), "too many tries: 3 minutes") {
		t.Fatalf("remaining: %v", err)
	}
}

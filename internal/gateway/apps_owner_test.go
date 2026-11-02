package gateway

import (
	"context"
	"encoding/json"
	"testing"
)

func TestConnectedAppsOwnerOperations(t *testing.T) {
	s, _, h := oauthFixture(t)
	client := oauthRegister(t, h)
	code, id := oauthPage(t, oauthAuthorize(h, client, nil))
	g := &Gateway{store: s}
	call := func(op string, v any) (any, error) {
		raw, _ := json.Marshal(v)
		return g.handle(context.Background(), &nodeConn{}, op, raw)
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

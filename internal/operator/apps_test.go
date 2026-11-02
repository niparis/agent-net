package operator

import (
	"context"
	"encoding/json"
	"testing"
)

func TestConnectedAppsCalls(t *testing.T) {
	_, path := startFake(t, func(method string, params json.RawMessage) (any, string) {
		if method == "hello" {
			return map[string]any{}, ""
		}
		switch method {
		case "apps.list", "apps.pending", "apps.approve", "apps.refuse", "apps.revoke":
			return map[string]any{"ok": true}, ""
		}
		return nil, "unknown"
	})
	c := New(path)
	defer c.Close()
	for _, op := range []string{"apps.list", "apps.pending", "apps.approve", "apps.refuse", "apps.revoke"} {
		raw, err := c.Call(context.Background(), op, map[string]string{"id": "test"})
		if err != nil || !json.Valid(raw) {
			t.Fatalf("%s: %s %v", op, raw, err)
		}
	}
}

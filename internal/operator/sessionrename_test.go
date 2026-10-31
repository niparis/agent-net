package operator

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestSessionRenameUsesTheOperatorMethod(t *testing.T) {
	d, path := startFake(t, func(method string, params json.RawMessage) (any, string) {
		switch method {
		case "hello":
			return map[string]any{}, ""
		case protocol.OpSessionRename:
			return protocol.SessionRenameResult{SessionID: "s_x", Name: "planner"}, ""
		}
		return nil, "unknown method"
	})
	c := New(path)
	defer c.Close()
	out, err := c.RenameSession(context.Background(), "s_x", "planner")
	if err != nil || out.SessionID != "s_x" || out.Name != "planner" {
		t.Fatalf("rename: %+v %v", out, err)
	}
	var got protocol.SessionRenameRequest
	if err := json.Unmarshal(d.params[protocol.OpSessionRename], &got); err != nil {
		t.Fatal(err)
	}
	if got.SessionID != "s_x" || got.Name != "planner" {
		t.Errorf("params: %+v", got)
	}
}

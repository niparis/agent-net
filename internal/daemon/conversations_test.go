package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestConversationsIsOwnerOnly(t *testing.T) {
	d := &Daemon{inSession: func(int) bool { return false }}
	for _, client := range []string{"mcp", "pi", "opencode", ""} {
		lc := &localConn{d: d, client: client}
		for _, method := range []string{protocol.OpConversations, protocol.OpConversationMessages} {
			_, handled, err := lc.dispatchOperator(context.Background(), method, json.RawMessage(`{"conversation_id":"c_1"}`))
			if !handled {
				t.Fatalf("client %q: %s is not handled", client, method)
			}
			if err == nil || !strings.Contains(err.Error(), "only available to the owner's command line or TUI") {
				t.Fatalf("client %q: %s was not owner-gated: %v", client, method, err)
			}
		}
	}
}

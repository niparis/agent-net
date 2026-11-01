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
		_, handled, err := lc.dispatchOperator(context.Background(), protocol.OpConversations, json.RawMessage(`{}`))
		if !handled {
			t.Fatalf("client %q: conversations method is not handled", client)
		}
		if err == nil || !strings.Contains(err.Error(), "only available to the owner's command line or TUI") {
			t.Fatalf("client %q: conversations was not owner-gated: %v", client, err)
		}
	}
}

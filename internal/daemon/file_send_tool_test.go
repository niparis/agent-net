package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestFileSendToolRequiresBoundSession(t *testing.T) {
	lc := &localConn{d: &Daemon{}, client: "mcp"}
	_, err := lc.dispatch(context.Background(), "file_send", json.RawMessage(`{"to":"s_b","paths":["/tmp/a"],"note":"x"}`))
	if err == nil || !strings.Contains(err.Error(), "bound session") {
		t.Fatalf("unbound call: %v", err)
	}
}

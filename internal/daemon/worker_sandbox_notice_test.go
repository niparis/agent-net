package daemon

import (
	"encoding/json"
	"testing"
)

func TestCodexSandboxDenialNotification(t *testing.T) {
	cc := &codexConn{thread: "t"}
	params, _ := json.Marshal(map[string]any{"threadId": "t", "item": map[string]any{"type": "commandExecution", "status": "failed", "aggregatedOutput": "sandbox denied: Operation not permitted"}})
	cc.notification(codexMsg{Method: "item/completed", Params: params})
	if !cc.sandboxDenied {
		t.Fatal("sandbox denial not recorded")
	}
	cc.sandboxDenied = false
	params = json.RawMessage(`{"threadId":"t","turn":{"status":"failed","error":{"message":"failed","codexErrorInfo":"sandboxError"}}}`)
	cc.notification(codexMsg{Method: "turn/completed", Params: params})
	if !cc.sandboxDenied {
		t.Fatal("structured sandboxError not recorded")
	}
	if sandboxDenial("permission denied") {
		t.Fatal("generic permission error is not proof of a sandbox denial")
	}
}

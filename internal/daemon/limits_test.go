package daemon

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func limitOf(t *testing.T, n *node, id string) protocol.AgentView {
	t.Helper()
	cli := dialLocal(t, n.sock)
	cli.mustCall("hello", map[string]any{"client": "cli"})
	res := cli.mustCall("get", map[string]any{"session_id": id})
	var r struct {
		Agent protocol.AgentView `json:"agent"`
	}
	if err := json.Unmarshal(res, &r); err != nil {
		t.Fatal(err)
	}
	return r.Agent
}

const piLimitText = "Error: You have hit your ChatGPT usage limit (plus plan). Try again in ~102 min."

// The Pi plugin reports a failed turn. The gateway then shows the limit, and
// it ends when the session completes a turn without an error.
func TestPluginUsageLimitReachesTheGatewayAndClearsAfterASuccessfulTurn(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	pi, id := helloPi(t, n, "nb", "/work/bob", false)

	pi.mustCall("status", map[string]any{"status": "busy"})
	before := time.Now()
	pi.mustCall("turn_error", map[string]any{"text": piLimitText})
	pi.mustCall("status", map[string]any{"status": "idle"})
	eventually(t, "the limit reaches the gateway", func() bool { return limitOf(t, n, id).LimitedUntil != 0 })
	a := limitOf(t, n, id)
	if a.LimitedReason != "ChatGPT usage limit" {
		t.Fatalf("reason = %q", a.LimitedReason)
	}
	if d := time.Unix(a.LimitedUntil, 0).Sub(before); d < 101*time.Minute || d > 103*time.Minute {
		t.Fatalf("limited for %v, want about 102 minutes", d)
	}

	// Going idle after the failed turn is not a success.
	time.Sleep(50 * time.Millisecond)
	if limitOf(t, n, id).LimitedUntil == 0 {
		t.Fatal("limit cleared by the idle that ended the failed turn")
	}

	// A turn without an error ends the limit.
	pi.mustCall("status", map[string]any{"status": "busy"})
	pi.mustCall("status", map[string]any{"status": "idle"})
	eventually(t, "a successful turn clears the limit", func() bool { return limitOf(t, n, id).LimitedUntil == 0 })
}

// An error that is no usage limit does not limit the session.
func TestPluginOtherTurnErrorsDoNotLimit(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	pi, id := helloPi(t, n, "nb", "/work/bob", false)
	pi.mustCall("turn_error", map[string]any{"text": "model not found"})
	pi.mustCall("status", map[string]any{"status": "idle"})
	time.Sleep(50 * time.Millisecond)
	if a := limitOf(t, n, id); a.LimitedUntil != 0 {
		t.Fatalf("limited: %+v", a)
	}
}

// Pi's own retry may succeed after an error; turn_ok clears the limit at once.
func TestPluginTurnOKClearsTheLimit(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	pi, id := helloPi(t, n, "nb", "/work/bob", false)
	pi.mustCall("turn_error", map[string]any{"text": piLimitText})
	eventually(t, "limit set", func() bool { return limitOf(t, n, id).LimitedUntil != 0 })
	pi.mustCall("turn_ok", map[string]any{})
	eventually(t, "limit cleared", func() bool { return limitOf(t, n, id).LimitedUntil == 0 })
}

// Codex reports a failed turn in turn/completed. A usage limit limits the
// thread; a completed turn clears it.
func TestCodexTurnCompletedSetsAndClearsTheLimit(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	n.d.SetSessions("codex", []protocol.SessionInfo{{NativeID: "t-open", Cwd: "/work/api", Status: "idle"}})
	var id string
	eventually(t, "thread registered", func() bool { id = n.d.SessionID("codex", "t-open"); return id != "" })
	a := &codexAdapter{d: n.d}
	a.onNote("turn/completed", json.RawMessage(`{"threadId":"t-open","turn":{"status":"failed","error":{"message":"You've hit your usage limit. Try again in 30 minutes."}}}`))
	eventually(t, "limit reaches the gateway", func() bool { return limitOf(t, n, id).LimitedUntil != 0 })
	if got := limitOf(t, n, id).LimitedReason; got != "usage limit" {
		t.Fatalf("reason %q", got)
	}
	a.onNote("turn/completed", json.RawMessage(`{"threadId":"t-open","turn":{"status":"completed"}}`))
	eventually(t, "completed turn clears it", func() bool { return limitOf(t, n, id).LimitedUntil == 0 })
}

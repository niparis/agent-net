package daemon

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// stateDaemon is a daemon over a state folder with no gateway and no socket,
// so it runs where unix sockets are blocked. Calling it again over the same
// folder is a daemon restart: only daemon.db carries over.
func stateDaemon(t *testing.T, state, claudeSessions string) *Daemon {
	t.Helper()
	d, err := New(Config{StateDir: state, ClaudeSessionsDir: claudeSessions, SocketPath: filepath.Join(state, "unused.sock")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.claudeChecks.Wait(); d.st.close() })
	return d
}

// holdStart saves the start: the next daemon holds it again, with what the
// answer needs. The answer drops it, so a later daemon does not hold it again.
func TestHeldStartIsLoadedAfterARestart(t *testing.T) {
	state := t.TempDir()
	// The gateway takes every call; the start report of the answer is one.
	gateway := func(context.Context, string, any) (json.RawMessage, bool, error) {
		return json.RawMessage(`{}`), true, nil
	}
	d := stateDaemon(t, state, "")
	d.cfg.testGatewayCall = gateway
	req := overrideRequest()
	req.Reason = ""
	res, err := d.holdStart(t.Context(), "s_caller", req, "a second opinion", true, "/code/shop", true)
	if err != nil {
		t.Fatal(err)
	}
	d.st.close()

	d = stateDaemon(t, state, "")
	d.heldMu.Lock()
	h := d.heldStarts[res.StartID]
	d.heldMu.Unlock()
	if h == nil {
		t.Fatal("the restarted daemon does not hold the start")
	}
	if h.caller != "s_caller" || !h.callerProject || h.callerPath != "/code/shop" || !h.cto || h.req.Model != "fake/m2" || h.req.Harness != "opencode" || h.req.Task != req.Task {
		t.Fatalf("held start after a restart: %+v", h)
	}
	d.cfg.testGatewayCall = gateway
	d.ctoCtx = t.Context()
	d.onStartDecision(protocol.StartDecision{Ref: protocol.StartOverrideRefPrefix + res.StartID, Session: "s_caller", Answer: "dismiss"})
	d.st.close()

	d = stateDaemon(t, state, "")
	d.heldMu.Lock()
	left := len(d.heldStarts)
	d.heldMu.Unlock()
	if left != 0 {
		t.Fatalf("%d held starts after the answer and a restart", left)
	}
}

package daemon

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

// sendAgyHook sends one agy hook event for the conversation "main" from pid.
func sendAgyHook(t *testing.T, d *Daemon, pid int, cwd, event string) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"conversationId": "main", "event": event, "workspacePaths": []string{cwd}, "fullyIdle": event == "Stop"})
	if _, err := d.antigravityHook(raw, pid); err != nil {
		t.Fatal(err)
	}
}

// agySessionID waits for d to register "main" with the gateway, without a
// manual refresh, and returns its gateway session ID.
func agySessionID(t *testing.T, d *Daemon) string {
	t.Helper()
	var id string
	eventually(t, "agy main session registered", func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		if ls := d.sessions[key("agy", "main")]; ls != nil {
			id = ls.sessionID
		}
		return id != ""
	})
	return id
}

// agyAnnounce binds an MCP connection from pid and runs a call that needs a
// bound session.
func agyAnnounce(d *Daemon, pid int) error {
	lc := &localConn{d: d}
	if _, err := lc.helloInteractiveAgy(helloParams{NativeID: "main"}, pid); err != nil {
		return err
	}
	_, err := lc.dispatch(context.Background(), "announce", json.RawMessage(`{"task":"check the restart"}`))
	return err
}

// newOfflineDaemon makes a daemon that is not started, so it needs no socket
// or gateway. restart makes a new one on the same state folder.
func newOfflineDaemon(t *testing.T) (d *Daemon, restart func() *Daemon) {
	t.Helper()
	state := t.TempDir()
	cfg := Config{StateDir: state, SocketPath: filepath.Join(state, "unused.sock")}
	open := func() *Daemon {
		d, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(d.Stop)
		return d
	}
	d = open()
	return d, func() *Daemon {
		d.Stop()
		d = open()
		return d
	}
}

func TestAgyMainSessionSurvivesDaemonRestart(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	cwd := t.TempDir()
	cmd := fakeInteractiveAgy(t, cwd, nil)
	sendAgyHook(t, n.d, cmd.Process.Pid, cwd, "SessionStart")
	before := agySessionID(t, n.d)
	if err := agyAnnounce(n.d, cmd.Process.Pid); err != nil {
		t.Fatalf("bound call before restart: %v", err)
	}
	n.restart()
	if after := agySessionID(t, n.d); after != before {
		t.Fatalf("session ID changed across restart: %s, then %s", before, after)
	}
	if err := agyAnnounce(n.d, cmd.Process.Pid); err != nil {
		t.Fatalf("bound call after restart: %v", err)
	}
}

func TestAgyMainSessionEndedBeforeRestartIsNotRestored(t *testing.T) {
	d, restart := newOfflineDaemon(t)
	cwd := t.TempDir()
	cmd := fakeInteractiveAgy(t, cwd, nil)
	sendAgyHook(t, d, cmd.Process.Pid, cwd, "SessionStart")
	d.adapters["agy"].(*agyAdapter).refresh()
	if rows, err := d.st.agyMains(); err != nil || len(rows) != 1 {
		t.Fatalf("saved before restart: %+v %v", rows, err)
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	d = restart()
	rows, err := d.st.agyMains()
	if err != nil {
		t.Fatal(err)
	}
	d.mu.Lock()
	_, kept := d.agyEvents["main"]
	d.mu.Unlock()
	if len(rows) != 0 || kept {
		t.Fatalf("ended agy session restored: rows=%+v in memory=%v", rows, kept)
	}
}

// A session that began before the plugin was enabled, or before this daemon
// knew it, reports PreInvocation first.
func TestAgyPreInvocationRegistersUnknownSession(t *testing.T) {
	d, _ := newOfflineDaemon(t)
	cwd := t.TempDir()
	cmd := fakeInteractiveAgy(t, cwd, nil)
	sendAgyHook(t, d, cmd.Process.Pid, cwd, "PreInvocation")
	a := d.adapters["agy"].(*agyAdapter)
	a.refresh()
	if !a.has("main", cmd.Process.Pid) {
		t.Fatal("PreInvocation from a live agy did not register it")
	}
	lc := &localConn{d: d}
	if _, err := lc.helloInteractiveAgy(helloParams{NativeID: "main"}, cmd.Process.Pid); err != nil || lc.ls == nil {
		t.Fatalf("MCP not bound: %v", err)
	}
	rows, err := d.st.agyMains()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ConversationID != "main" || rows[0].PID != cmd.Process.Pid {
		t.Fatalf("saved: %+v", rows)
	}
}

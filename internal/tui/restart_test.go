package tui

import (
	"testing"
	"time"
)

func TestRestartOnBinaryChangeWaitsForFormAndKeepsTab(t *testing.T) {
	m := New(nil)
	m.tab = graphTab
	m.restart = &restartState{changed: func() bool { return true }}
	m.mode = modeSessionForm
	next, _ := m.update(tickMsg{})
	if next.(Model).restart.requested {
		t.Fatal("restart while form open")
	}
	m = next.(Model)
	m.mode = modeNormal
	next, _ = m.update(tickMsg{})
	got := next.(Model)
	if !got.restart.requested || got.tab != graphTab {
		t.Fatal("restart did not preserve tab")
	}
}

func TestRestartCheckThrottle(t *testing.T) {
	calls := 0
	now := time.Now()
	m := New(nil)
	m.now = func() time.Time { return now }
	m.restart = &restartState{changed: func() bool { calls++; return false }}
	m.update(tickMsg{})
	// State copies retain the check time.
	next, _ := m.update(tickMsg{})
	next.(Model).update(tickMsg{})
	if calls != 1 {
		t.Fatalf("checked %d times", calls)
	}
}

func TestRestartOnNewerDaemon(t *testing.T) {
	m := New(nil)
	m.restart = &restartState{version: "v0.4.184", changed: func() bool { return false }}
	m.snap.Version = "v0.4.186"
	next, _ := m.update(tickMsg{})
	if !next.(Model).restart.requested {
		t.Fatal("newer daemon did not trigger restart")
	}
}

func TestRestartExecPreservesArgumentsAndTab(t *testing.T) {
	m := New(nil)
	m.tab = graphTab
	m.restart = &restartState{target: "v1.2.3"}
	called := false
	err := m.execUpdate("/fake/agent-net", []string{"agent-net", "tui"}, func(path string, args, env []string) error {
		called = path == "/fake/agent-net" && len(args) == 2
		for _, e := range env {
			if e == "AGENT_NET_TUI_RESTART_TAB=1" {
				return nil
			}
		}
		t.Error("tab missing from environment")
		return nil
	})
	if err != nil || !called {
		t.Fatal("exec was not called", err)
	}
}

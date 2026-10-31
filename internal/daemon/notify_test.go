package daemon

import (
	"encoding/json"
	"github.com/niparis/agent-net/internal/protocol"
	"testing"
	"time"
)

func TestNotifyNewAskAndRateLimit(t *testing.T) {
	var n notificationTracker
	now := time.Unix(1000, 0)
	a := protocol.AgentView{SessionID: "s_test", Online: true, Waiting: &protocol.Waiting{Kind: "question", Text: "private", Since: 1000, Explicit: true}}
	if got := n.observe([]protocol.AgentView{a}, now, true); len(got) != 1 {
		t.Fatalf("new: %v", got)
	}
	if got := n.observe([]protocol.AgentView{a}, now.Add(time.Minute), true); len(got) != 0 {
		t.Fatalf("repeat: %v", got)
	}
	n.observe(nil, now.Add(2*time.Minute), true)
	a.Waiting.Since++
	if got := n.observe([]protocol.AgentView{a}, now.Add(2*time.Minute), true); len(got) != 0 {
		t.Fatalf("rate limit: %v", got)
	}
	n.observe(nil, now.Add(5*time.Minute), true)
	if got := n.observe([]protocol.AgentView{a}, now.Add(5*time.Minute), true); len(got) != 1 {
		t.Fatalf("after limit: %v", got)
	}
	n.observe(nil, now.Add(11*time.Minute), true)
	if got := n.observe([]protocol.AgentView{a}, now.Add(11*time.Minute), false); len(got) != 0 {
		t.Fatalf("disabled: %v", got)
	}
}

func TestNotifySettingIsOwnerOnlyAndPersists(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	pi, _ := helloPi(t, n, "nb", "/work/bob", false)
	if _, err := pi.call("notification_settings", map[string]any{"disabled": true}); err == nil {
		t.Fatal("agent changed notification settings")
	}
	cli := dialLocal(t, n.sock)
	cli.mustCall("hello", map[string]any{"client": "cli"})
	cli.mustCall("notification_settings", map[string]any{"disabled": true})
	var result struct {
		Disabled bool `json:"disabled"`
	}
	if err := json.Unmarshal(cli.mustCall("notification_settings", nil), &result); err != nil || !result.Disabled {
		t.Fatalf("read: %+v %v", result, err)
	}
	if disabled, err := n.d.notificationsDisabled(); err != nil || !disabled {
		t.Fatalf("stored: %v %v", disabled, err)
	}
}

func TestNotifyRemoteAskOnTUIRefresh(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	sender := startNode(t, g, nodeOpts{cmdline: "pi"})
	receiver := startNode(t, g, nodeOpts{cmdline: "pi"})
	notified := make(chan struct{}, 4)
	receiver.d.notifySend = func() { notified <- struct{}{} }
	pi, id := helloPi(t, sender, "nb", "/work/bob", false)
	tui := dialLocal(t, receiver.sock)
	tui.mustCall("hello", map[string]any{"client": "cli", "tui": true})
	pi.mustCall("waiting", map[string]any{"kind": "question", "text": "secret question", "explicit": true})
	eventually(t, "network wait", func() bool { w := waitingOf(t, receiver, id); return w != nil && w.Explicit })
	tui.mustCall("list", map[string]any{"include_owners": true})
	select {
	case <-notified:
	case <-time.After(time.Second):
		t.Fatal("no notification")
	}
	tui.mustCall("list", map[string]any{"include_owners": true})
	select {
	case <-notified:
		t.Fatal("duplicate notification")
	default:
	}
}

func TestNotifyRateLimitSurvivesStateReopen(t *testing.T) {
	dir := t.TempDir()
	st, err := openState(dir)
	if err != nil {
		t.Fatal(err)
	}
	d := &Daemon{st: st}
	for _, tc := range []struct {
		at   int64
		want bool
	}{{1000, true}, {1100, false}, {1300, true}} {
		if got, err := d.reserveNotification("s_test", time.Unix(tc.at, 0)); err != nil || got != tc.want {
			t.Fatalf("at %d: %v %v", tc.at, got, err)
		}
		if err := st.close(); err != nil {
			t.Fatal(err)
		}
		st, err = openState(dir)
		if err != nil {
			t.Fatal(err)
		}
		d.st = st
	}
	st.close()
}

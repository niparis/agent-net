package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// attachedClosedRig is the dotfiles case: an OpenCode session whose plugin is
// still connected to the daemon (the conversation lives in the owner's shared
// service), while the gateway holds it closed and offline. The gateway ignores
// the plugin's registration until OpenCode reports newer activity.
func attachedClosedRig(t *testing.T) (n *workerNode, pi *localClient, plugin *localClient, id string, g *testGateway) {
	t.Helper()
	g = startGateway(t, t.TempDir(), "")
	n = startWorkerNode(t, g, shortSet, nil)
	id, dir := offlineOwnerSession(t, g, n.node, "opencode", "ses_dot", protocol.AutonomyOff)
	plugin, got := helloOpenCode(t, n.node, "ses_dot", dir)
	if got != id {
		t.Fatalf("plugin registered as %s, want %s", got, id)
	}
	if err := g.gw.Store().SetSessionClosed(n.node.d.NodeID(), id, true); err != nil {
		t.Fatal(err)
	}
	pi, _ = helloPi(t, n.node, "np", "/work/pi", true)
	return n, pi, plugin, id, g
}

func gatewayOnline(t *testing.T, n *workerNode, id string) bool {
	t.Helper()
	a, err := n.node.d.agentView(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return a.Online
}

// A message to a session the daemon holds live but the gateway holds offline
// must reach it: the wake registers the session again instead of answering
// "already live; nothing to start".
func TestWakeOfLiveSessionTheGatewayHoldsClosedRegistersItAgain(t *testing.T) {
	n, pi, plugin, id, _ := attachedClosedRig(t)
	eventually(t, "the gateway shows the closed session offline", func() bool { return !gatewayOnline(t, n, id) })
	m := sendResult(t, pi.mustCall("send", map[string]any{"to": id, "body": "are you there"}))
	select {
	case push := <-plugin.Pushes:
		if push.ID != m.ID {
			t.Fatalf("delivered %+v, want %s", push, m.ID)
		}
	case <-time.After(scaled(10 * time.Second)):
		t.Fatalf("message not delivered; state %s", messageState(t, pi, m.ID))
	}
	if !gatewayOnline(t, n, id) {
		t.Fatal("the gateway still shows the session offline")
	}
}

// Resume of such a session reattaches it and says so. It is not reported as a
// start of OpenCode.
func TestResumeOfLiveSessionTheGatewayHoldsClosedReportsAttached(t *testing.T) {
	n, _, _, id, _ := attachedClosedRig(t)
	start, err := n.node.d.ctoResume(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if !start.Attached || start.Status != "started" || start.SessionID != id {
		t.Fatalf("resume result %+v, want attached", start)
	}
	if !gatewayOnline(t, n, id) {
		t.Fatal("the gateway still shows the session offline after the resume")
	}
}

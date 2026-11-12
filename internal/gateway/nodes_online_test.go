package gateway

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/niparis/agent-net/internal/protocol"
)

// newStateGateway is a gateway whose clock the test moves, with one enrolled
// machine, so a link stays open while the offline and silent windows pass.
func newStateGateway(t *testing.T, c *clock) (*Gateway, string, string, string) {
	t.Helper()
	g, err := New(Options{DataDir: seededDataDir(t), Now: c.now,
		KeepalivePingInterval: testLinkPing, KeepaliveTimeout: testLinkTimeout})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.Close() })
	id, token, err := g.store.CreateNode("quiet-mac", "darwin")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(g.Handler())
	t.Cleanup(srv.Close)
	return g, "ws" + strings.TrimPrefix(srv.URL, "http") + "/v1/node", id, token
}

// sendSessionSync is normal traffic from a quiet machine: one agent session
// and nothing else to say.
func sendSessionSync(t *testing.T, ws *websocket.Conn, n int) {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"type": "session.sync", "id": "q" + strconv.Itoa(n),
		"sessions": []protocol.SessionInfo{{Harness: "fake", NativeID: "quiet-session", Name: "quiet"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := ws.Write(context.Background(), websocket.MessageText, b); err != nil {
		t.Fatal(err)
	}
}

// findMachine is one machine as the nodes list reads it. It is safe to call
// while polling: the machine is not listed until its hello is recorded.
func findMachine(t *testing.T, g *Gateway, node string) (protocol.NodeView, bool) {
	t.Helper()
	info, err := g.NodesInfo()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range info.Nodes {
		if n.NodeID == node {
			return n, true
		}
	}
	return protocol.NodeView{}, false
}

// listedMachine is findMachine for a machine the test has already seen.
func listedMachine(t *testing.T, g *Gateway, node string) protocol.NodeView {
	t.Helper()
	n, ok := findMachine(t, g, node)
	if !ok {
		t.Fatalf("machine %s is not listed", node)
	}
	return n
}

// silenceRows counts the outage rows the gateway kept for a machine.
func silenceRows(t *testing.T, g *Gateway, node string) int {
	t.Helper()
	var n int
	if err := g.store.db.QueryRow(`SELECT COUNT(*) FROM node_silence WHERE node_id=?`, node).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// storedLastSeen is the last frame time the gateway stored for a machine, as
// it reads after the link is gone.
func storedLastSeen(t *testing.T, g *Gateway, node string) int64 {
	t.Helper()
	nodes, err := g.store.Nodes()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range nodes {
		if n.NodeID == node {
			return n.LastSeen
		}
	}
	return 0
}

// TestQuietConnectedNodeStaysOnline is the owner's Mac: a machine that stays
// connected and sends only normal frames for longer than the offline and
// silent windows is still online, with a last seen that follows its frames.
func TestQuietConnectedNodeStaysOnline(t *testing.T) {
	c := &clock{t: time.Date(2026, 11, 12, 3, 23, 9, 0, time.UTC)}
	g, url, node, token := newStateGateway(t, c)
	ws := dialLinkPeer(t, url, token, true, new(atomic.Int32))
	sendLinkSession(t, ws)
	waitLink(t, "the machine to connect", func() bool { return nodeConnected(g, node) })
	waitLink(t, "the machine hello", func() bool { _, ok := findMachine(t, g, node); return ok })

	// Four normal frames over 20 minutes: past the 10 s offline grace and past
	// the 15 minute silent window, with the link open the whole time.
	for i := 1; i <= 4; i++ {
		c.advance(5 * time.Minute)
		sendSessionSync(t, ws, i)
		want := c.t.Unix()
		waitLink(t, "the frame to be seen", func() bool {
			n, ok := findMachine(t, g, node)
			return ok && n.LastSeen >= want
		})
	}

	n := listedMachine(t, g, node)
	if !n.Online || n.DownState != "" {
		t.Fatalf("after 20 quiet minutes: online=%v state=%q, want online with no down state", n.Online, n.DownState)
	}
	if n.LastSeen < c.t.Unix() {
		t.Fatalf("last seen is %s old, want the frame at %d", c.t.Sub(time.Unix(n.LastSeen, 0)), c.t.Unix())
	}
	if asks := machineAsks(t, g.store, node); asks != 0 {
		t.Fatalf("%d Desk items about a connected machine, want none", asks)
	}

	// agent_machine_get and agent_machines_find read the same state.
	m, err := g.MachineGet(context.Background(), protocol.MachineGetRequest{Machine: node, Include: []string{"track_record"}})
	if err != nil {
		t.Fatal(err)
	}
	if !m.Machine.Online || m.Machine.State != "online" {
		t.Fatalf("agent_machine_get: online=%v state=%q, want online", m.Machine.Online, m.Machine.State)
	}
	if got, want := m.Machine.LastSeen, rfc3339(n.LastSeen); got != want {
		t.Fatalf("agent_machine_get last seen %q, nodes list %q", got, want)
	}
	page, err := g.FindMachines(protocol.MachinesFindRequest{Online: "any"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || !page.Items[0].Online || page.Items[0].State != "online" {
		t.Fatalf("agent_machines_find: %+v, want one online machine", page.Items)
	}
}

// TestQuickReconnectEndsOnline is the drop at 03:23:07 and the connect at
// 03:23:09: a machine that disconnects and reconnects within two seconds ends
// online, and the late cleanup of the old link records no outage on top of it.
func TestQuickReconnectEndsOnline(t *testing.T) {
	c := &clock{t: time.Date(2026, 11, 12, 3, 23, 7, 0, time.UTC)}
	g, url, node, token := newStateGateway(t, c)
	first := dialLinkPeer(t, url, token, true, new(atomic.Int32))
	sendLinkSession(t, first)
	waitLink(t, "the machine to connect", func() bool { return nodeConnected(g, node) })
	waitLink(t, "the machine hello", func() bool { _, ok := findMachine(t, g, node); return ok })

	c.advance(5 * time.Minute)
	sendSessionSync(t, first, 1)
	frameAt := c.t.Unix()
	waitLink(t, "the last frame", func() bool {
		n, ok := findMachine(t, g, node)
		return ok && n.LastSeen >= frameAt
	})

	if err := first.Close(websocket.StatusNormalClosure, "restart"); err != nil {
		t.Fatal(err)
	}
	waitLink(t, "the link to drop", func() bool { return !nodeConnected(g, node) })
	// The outage row is the last thing the old link's cleanup writes. Wait for
	// it, so the return below cannot overtake that cleanup.
	waitLink(t, "the drop to be recorded", func() bool { return silenceRows(t, g, node) == 1 })

	// The outage is dated by the last frame, so the machine's last seen
	// survives the drop instead of freezing at its connect time.
	if got := storedLastSeen(t, g, node); got != frameAt {
		t.Fatalf("last seen after the drop = %d, want the last frame at %d", got, frameAt)
	}

	c.advance(2 * time.Second)
	second := dialLinkPeer(t, url, token, true, new(atomic.Int32))
	sendLinkSession(t, second)
	waitLink(t, "the machine to return", func() bool { return nodeConnected(g, node) })
	waitLink(t, "the return to clear the outage", func() bool { return silenceRows(t, g, node) == 0 })

	n := listedMachine(t, g, node)
	if !n.Online || n.DownState != "" || n.LastSeen < c.t.Unix() {
		t.Fatalf("after a two second reconnect: online=%v state=%q last seen %d, want online with a fresh last seen",
			n.Online, n.DownState, n.LastSeen)
	}

	// The old connection's cleanup can run after the new one said hello. Its
	// drop is older than that hello, so it must not record an outage.
	if err := g.store.NodeLinkDown(node, time.Unix(frameAt, 0)); err != nil {
		t.Fatal(err)
	}
	if n := listedMachine(t, g, node); !n.Online || n.DownState != "" {
		t.Fatalf("after a late drop from the old link: online=%v state=%q, want online", n.Online, n.DownState)
	}
	if silence := silenceRows(t, g, node); silence != 0 {
		t.Fatalf("%d silence rows after the return, want none", silence)
	}
}

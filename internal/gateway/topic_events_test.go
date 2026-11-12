package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/niparis/agent-net/internal/protocol"
)

func TestTopicVersionsFollowCommittedTables(t *testing.T) {
	s, _ := newTestStore(t)
	before := s.topicVersions()
	if len(before) != len(protocol.Topics) {
		t.Fatalf("initial versions: %v", before)
	}
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`INSERT INTO owner_settings(key,value,updated_at) VALUES('topic-test','one',1)`); err != nil {
		t.Fatal(err)
	}
	during := s.topicVersions()
	if during[protocol.TopicAgents] != before[protocol.TopicAgents] {
		t.Fatal("version moved before commit")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if s.topicVersions()[protocol.TopicAgents] != before[protocol.TopicAgents] {
		t.Fatal("version moved on rollback")
	}
	if _, err = s.db.Exec(`INSERT INTO owner_settings(key,value,updated_at) VALUES('topic-test','two',1)`); err != nil {
		t.Fatal(err)
	}
	after := s.topicVersions()
	if after[protocol.TopicAgents] == before[protocol.TopicAgents] {
		t.Fatal("agents version did not move")
	}
	if after[protocol.TopicLearned] != before[protocol.TopicLearned] {
		t.Fatal("unrelated learned version moved")
	}
}

func TestUsageTopicAndQuotaVersion(t *testing.T) {
	s, _ := newTestStore(t)
	g := &Gateway{store: s}
	before := g.topicVersions()
	g.usage.set("node", time.Now(), protocol.UsageReport{})
	first := g.topicVersions()
	if first[protocol.TopicUsage] <= before[protocol.TopicUsage] {
		t.Fatal("usage report did not move usage")
	}
	g.usage.set("node", time.Now(), protocol.UsageReport{})
	again := g.topicVersions()
	if again[protocol.TopicUsage] <= first[protocol.TopicUsage] {
		t.Fatal("second usage report did not move usage")
	}
	if again[protocol.TopicNodes] != first[protocol.TopicNodes] {
		t.Fatal("unchanged quotas moved nodes")
	}
	g.usage.set("node", time.Now(), protocol.UsageReport{Quotas: &protocol.Quotas{}})
	if g.topicVersions()[protocol.TopicNodes] <= again[protocol.TopicNodes] {
		t.Fatal("changed quotas did not move nodes")
	}
}

func TestTopicEventsFollowNodeConnections(t *testing.T) {
	g, url, subscriberID, subscriberToken := newQuietLinkGateway(t, time.Second)
	targetID, targetToken, err := g.store.CreateNode("other-test-host", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if err := g.store.AddMCPServer(mcpSpec("test-files", targetID)); err != nil {
		t.Fatal(err)
	}
	if err := g.store.SetNodeHello(targetID, "other-test-host", "test"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	observer, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + subscriberToken}}})
	if err != nil {
		t.Fatal(err)
	}
	defer observer.CloseNow()
	frames := make(chan []byte, 100)
	go func() {
		for {
			_, raw, err := observer.Read(ctx)
			if err != nil {
				return
			}
			select {
			case frames <- raw:
			case <-ctx.Done():
				return
			}
		}
	}()
	write := func(frame any) {
		t.Helper()
		raw, err := json.Marshal(frame)
		if err != nil {
			t.Fatal(err)
		}
		if err := observer.Write(ctx, websocket.MessageText, raw); err != nil {
			t.Fatal(err)
		}
	}
	result := func(id string) protocol.Header {
		t.Helper()
		for {
			select {
			case raw := <-frames:
				var h protocol.Header
				if json.Unmarshal(raw, &h) == nil && h.Type == "result" && h.ID == id {
					if !h.OK {
						t.Fatalf("request %s: %s", id, h.Error)
					}
					return h
				}
			case <-ctx.Done():
				t.Fatalf("no response for %s", id)
				return protocol.Header{}
			}
		}
	}
	connectTarget := func() *websocket.Conn {
		t.Helper()
		target, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + targetToken}}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { target.CloseNow() })
		go func() {
			for {
				if _, _, err := target.Read(ctx); err != nil {
					return
				}
			}
		}()
		waitLink(t, "target connected", func() bool { return nodeConnected(g, targetID) })
		return target
	}
	checkMCP := func(want bool) {
		t.Helper()
		nodes, err := g.nodesWithState()
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, node := range nodes {
			if node.NodeID == targetID {
				found = true
				if node.Online != want {
					t.Fatalf("node online=%t, want %t", node.Online, want)
				}
			}
		}
		if !found {
			t.Fatal("target missing from machines")
		}
		servers, err := g.mcpList()
		if err != nil || len(servers) != 1 || servers[0].HostOnline != want {
			t.Fatalf("MCP online want %t: %+v %v", want, servers, err)
		}
		items, err := g.store.ToolsItems(protocol.ToolsFilter{Kind: "mcp"}, func(id string) bool { return g.conn(id) != nil })
		if err != nil || len(items) != 1 || items[0].HostOnline != want {
			t.Fatalf("tool online want %t: %+v %v", want, items, err)
		}
	}
	target := connectTarget()
	checkMCP(true)
	waitLink(t, "subscriber connected", func() bool { return nodeConnected(g, subscriberID) })
	write(map[string]any{"type": "hello", "id": "h", "hostname": "test-host", "graph_refs": true})
	result("h")
	write(map[string]any{"type": protocol.FrameTopicsSubscribe, "id": "sub"})
	response := result("sub")
	var versions map[string]uint64
	if err := json.Unmarshal(response.Data, &versions); err != nil {
		t.Fatal(err)
	}

	waitEvents := func(label string) {
		t.Helper()
		want := map[string]bool{protocol.TopicNodes: true, protocol.TopicAgents: true, protocol.TopicWorkers: true, protocol.TopicProjects: true}
		deadline := time.NewTimer(2 * time.Second)
		defer deadline.Stop()
		for len(want) > 0 {
			select {
			case raw := <-frames:
				var h protocol.Header
				var ev protocol.TopicEvent
				if json.Unmarshal(raw, &h) == nil && h.Type == protocol.FrameTopicChanged && json.Unmarshal(raw, &ev) == nil && ev.Version > versions[ev.Topic] {
					versions[ev.Topic] = ev.Version
					delete(want, ev.Topic)
				}
			case <-deadline.C:
				t.Fatalf("%s: missing topic events %v", label, want)
			}
		}
	}
	if err := target.CloseNow(); err != nil {
		t.Fatal(err)
	}
	waitLink(t, "target disconnected", func() bool { return !nodeConnected(g, targetID) })
	waitEvents("disconnect")
	checkMCP(false)
	target = connectTarget()
	waitEvents("reconnect")
	checkMCP(true)
}

func TestTopicConnectionVersionMovesOnMachineRemoval(t *testing.T) {
	g, url, ownerNode, _ := newQuietLinkGateway(t, time.Second)
	ownerID, err := g.store.UpsertSession(ownerNode, owner("test-host"))
	if err != nil {
		t.Fatal(err)
	}
	targetID, targetToken, err := g.store.CreateNode("target-test-host", "linux")
	if err != nil {
		t.Fatal(err)
	}
	var pings atomic.Int32
	dialLinkPeer(t, url, targetToken, true, &pings)
	waitLink(t, "target connected", func() bool { return nodeConnected(g, targetID) })
	before := g.connVersion.Load()
	raw, err := json.Marshal(map[string]string{"node_id": targetID, "owner": ownerID, "actor": "owner"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.machineAction(context.Background(), &nodeConn{id: ownerNode}, "machine.remove", raw); err != nil {
		t.Fatal(err)
	}
	if after := g.connVersion.Load(); after != before+1 {
		t.Fatalf("connection version = %d, want %d", after, before+1)
	}
	if nodeConnected(g, targetID) {
		t.Fatal("removed target still connected")
	}
}

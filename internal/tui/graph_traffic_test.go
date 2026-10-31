package tui

import (
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestGraphTrafficUsesFeedSessionProjects(t *testing.T) {
	now := time.Unix(100, 0)
	m := Model{}
	m.applyMap(mapMsg{view: protocol.GraphView{Nodes: []protocol.Node{{Name: "alpha", Sessions: []protocol.GraphSession{{ID: "s1"}}}, {Name: "beta", Sessions: []protocol.GraphSession{{ID: "s2"}}}}}})
	m.graphEvents([]protocol.FeedEvent{{Event: protocol.Event{ID: 1, Kind: "msg.stored", Time: now, MessageID: "m1"}, From: "s1", To: "s2", Type: "request"}}, 0, now)
	comets, _ := m.graph.Traffic(now.Add(time.Second))
	if len(comets) != 1 || comets[0].From != "alpha" || comets[0].To != "beta" {
		t.Fatalf("comets: %+v", comets)
	}
}

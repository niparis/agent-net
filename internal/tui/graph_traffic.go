package tui

import (
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func (m *Model) graphEvents(events []protocol.FeedEvent, lastID int64, now time.Time) {
	if m.graph == nil {
		return
	}
	projects := make(map[string]string)
	for _, n := range m.graph.Nodes {
		for _, s := range n.Sessions {
			projects[s.ID] = n.Name
		}
	}
	for _, e := range events {
		if e.ID <= lastID || e.Kind != "msg.stored" {
			continue
		}
		at := e.Time
		if at.IsZero() || at.Before(now.Add(-3*time.Second)) {
			at = now
		}
		m.graph.Message(projects[e.From], projects[e.To], e.Type, at)
	}
}

package daemon

import (
	"github.com/niparis/agent-net/internal/gateway"
	"github.com/niparis/agent-net/internal/protocol"
)

// inboxAndLog is what the owner was told: the open Inbox rows, then the lines
// of the Desk log in the shape of the old CTO notices (decision 0090).
func inboxAndLog(s *gateway.Store) ([]protocol.InboxItem, error) {
	items, err := s.ListInbox()
	if err != nil {
		return nil, err
	}
	log, err := s.DeskLog(500)
	if err != nil {
		return nil, err
	}
	for i := len(log) - 1; i >= 0; i-- {
		if log[i].Source != "cto" {
			continue
		}
		items = append(items, protocol.InboxItem{Kind: "check", Node: "cto", Text: log[i].Text, Recommendation: log[i].Detail, Options: log[i].Options, Status: "open"})
	}
	return items, nil
}

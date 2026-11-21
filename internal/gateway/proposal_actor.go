package gateway

import (
	"strconv"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// Enrolled daemons supply actors after their owner gate has checked approval.
func validChangeActor(actor string) bool {
	if actor == "owner" || actor == "cto" {
		return true
	}
	const prefix = "owner (CTO proposal p_"
	if !strings.HasPrefix(actor, prefix) || !strings.HasSuffix(actor, ")") {
		return false
	}
	n, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimPrefix(actor, prefix), ")"), 10, 64)
	return err == nil && n > 0
}

// validToolOwner says the owner's agent id belongs to an online owner session
// on the node that sent the frame.
func (g *Gateway) validToolOwner(nc *nodeConn, id string) bool {
	if id == "" {
		return false
	}
	a, ok, err := g.store.Agent(id)
	return err == nil && ok && a.NodeID == nc.id && a.Harness == protocol.OwnerHarness && a.Online
}

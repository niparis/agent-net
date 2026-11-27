package gateway

import "github.com/niparis/agent-net/internal/protocol"

// validChangeActor accepts the actors an enrolled daemon supplies after its
// own owner checks: the owner, the CTO, and the CTO on the owner's verified
// words for an owner-only action (decision 0165).
func validChangeActor(actor string) bool {
	return actor == "owner" || actor == "cto" || actor == protocol.CTOOwnerWordsActor
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

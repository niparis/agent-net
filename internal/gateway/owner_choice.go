package gateway

import (
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// workerStartOwnerChoiceMigration added the owner's choice to the start record
// (decision 0191). The column is no longer written: only the owner approves a
// selection now, on the one ask of an exceptional start. The migration stays
// appended, because a shipped migration is never edited (B5).
const workerStartOwnerChoiceMigration = `SELECT 1 /* worker start owner choice column */;`

// ownerSaid tells whether the start's selection fields are the owner's own:
// his command line or TUI, or his words found by the caller's daemon
// (owner_selected). The CTO's start without his words is not.
func ownerSaid(p protocol.WorkerStartFrame, owner protocol.AgentView) bool {
	return p.OwnerSelected || (protocol.IsOwner(owner.Harness) && !p.FromCTO)
}

// modelNamed tells whether a model field names a model: not empty, not auto.
func modelNamed(m string) bool {
	m = strings.TrimSpace(m)
	return m != "" && !strings.EqualFold(m, "auto")
}

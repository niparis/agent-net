package daemon

import (
	"context"
	"encoding/json"

	"github.com/niparis/agent-net/internal/protocol"
)

// ctoActorKey carries who a CTO change acts as: "cto" for the CTO's own
// changes, protocol.CTOOwnerWordsActor for an owner-only action the owner's
// verified words approve. The owner's own calls carry none.
type ctoActorKey struct{}

// ctoRelayedStartKey marks a CTO session start that another machine relayed
// here. That machine tells the owner about the start, so this one does not.
type ctoRelayedStartKey struct{}

func ctoActor(ctx context.Context) string {
	s, _ := ctx.Value(ctoActorKey{}).(string)
	return s
}

// ctoProjectRemove translates the CTO's machine field to the registry API.
// The preview and the removal use the same request.
func ctoProjectRemove(raw json.RawMessage) (protocol.ProjectRemove, error) {
	var p struct {
		protocol.ProjectRemove
		NodeID string `json:"node_id"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p.ProjectRemove, err
	}
	if p.NodeID != "" {
		p.On = p.NodeID
	}
	if p.Path != "" {
		var err error
		if p.Path, err = expandPath(p.Path); err != nil {
			return p.ProjectRemove, err
		}
	}
	return p.ProjectRemove, nil
}

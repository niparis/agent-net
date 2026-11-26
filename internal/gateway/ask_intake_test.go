package gateway

import (
	"context"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// An import for the owner is a request to the chief, not a stored ask
// (decision 0196). The turn still counts as imported, so a second import of
// it, even in other words, sends nothing new.
func TestAskIntakeImportedTurnIsTheChiefRequest(t *testing.T) {
	s, _, chief, asker, _ := chiefRig(t)
	g := &Gateway{store: s}
	turn := protocol.AskIntakeSourceRef(asker, "turn-1")
	importTurn := func(text string) protocol.AskIntakeAsk {
		t.Helper()
		res, err := g.askIntakeImport(context.Background(), protocol.AskIntakeRequest{AskIntakeArgs: protocol.AskIntakeArgs{
			Action: protocol.AskIntakeImport, Session: asker, Turn: turn, Text: text}})
		if err != nil || res.Ask == nil {
			t.Fatalf("import %q: %+v %v", text, res, err)
		}
		return *res.Ask
	}
	first := importTurn("Rotate the stale vault backup now?")
	if first.State != protocol.AskIntakeCreated || first.ID == "" || first.Next != chief {
		t.Fatalf("first import: %+v", first)
	}
	second := importTurn("Rotate the vault backup today?")
	if second.State != protocol.AskIntakeAlreadyImported || second.ID != first.ID {
		t.Fatalf("second import: %+v, first %+v", second, first)
	}
	if id, _, err := g.askIntakeImportedAsk(asker, turn); err != nil || id != first.ID {
		t.Fatalf("imported ask of the turn: %q %v, want %q", id, err, first.ID)
	}
}

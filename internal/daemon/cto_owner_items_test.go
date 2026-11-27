package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// The CTO keeps no proposals, so its owner item read has no proposal kind and
// no row points at proposal_ask. The default lists every kind.
func TestCTOOwnerItemsFindHasNoProposals(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	_, err := w.d.runCTOToolRaw(context.Background(), "cto_owner_items_find", json.RawMessage(`{"kind":"proposal"}`))
	if err == nil || strings.Contains(err.Error(), "proposal,") || !strings.Contains(err.Error(), "kind must be all") {
		t.Fatalf("kind proposal: %v", err)
	}
	out, err := w.d.runCTOToolRaw(context.Background(), "cto_owner_items_find", nil)
	if err != nil {
		t.Fatal(err)
	}
	if body, _ := json.Marshal(out); strings.Contains(string(body), "proposal_ask") {
		t.Fatalf("a row points at proposal_ask: %s", body)
	}
}

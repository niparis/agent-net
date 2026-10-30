package gateway

import (
	"path/filepath"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
	"testing"
)

func TestPeerTrustMigratesOnceToWildcardRule(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	ids, _ := graphSessions(t, s, "consumer", "provider")
	if _, err = s.db.Exec(`UPDATE sessions SET peer_trust=1 WHERE id=?`, ids["provider"]); err != nil {
		t.Fatal(err)
	}

	// The trust migration is no longer the last one: request outcomes follow
	// it, and their tables cannot be replayed. Run the trust migration itself
	// again, as a gateway that never applied it would.
	trust := ""
	for _, m := range migrations {
		if strings.Contains(m, "INSERT OR IGNORE INTO graph_edges") {
			trust = m
		}
	}
	if trust == "" {
		t.Fatal("no trust migration")
	}
	if _, err = s.db.Exec(trust); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	rule, err := s.RequestRule(ids["consumer"], ids["provider"])
	if err != nil || rule != "auto" {
		t.Fatalf("migrated rule=%q %v", rule, err)
	}
	if err = s.SetEdge(protocol.Edge{From: "*", To: "provider", Kind: "depends", Rule: "ask"}, "owner"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rule, err = s.RequestRule(ids["consumer"], ids["provider"])
	if err != nil || rule != "ask" {
		t.Fatalf("restart changed rule: %q %v", rule, err)
	}
}

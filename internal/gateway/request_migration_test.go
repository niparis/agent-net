package gateway

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
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

	// The request-rule migration is no longer the last one, so rewind to the
	// migration before it and let OpenStore apply it (and the later ones)
	// again. The later migrations are idempotent for this.
	ruleMigration := 0
	for i, m := range migrations {
		if strings.Contains(m, "INSERT OR IGNORE INTO graph_edges") {
			ruleMigration = i + 1
		}
	}
	if ruleMigration == 0 {
		t.Fatal("the request-rule migration is gone")
	}
	if _, err = s.db.Exec(`DELETE FROM schema_version WHERE version >= ?`, ruleMigration); err != nil {
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

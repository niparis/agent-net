package gateway

import (
	"path/filepath"

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

	if _, err = s.db.Exec(`DELETE FROM schema_version WHERE version=(SELECT MAX(version) FROM schema_version)`); err != nil {
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

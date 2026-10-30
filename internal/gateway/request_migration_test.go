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

	// Recreate the schema just before the peer-trust migration. Later inbox
	// migrations also need to run when this database is reopened.
	for _, statement := range []string{
		`DROP TABLE inbox_items`,
		`ALTER TABLE messages DROP COLUMN approval_note`,
		`DELETE FROM schema_version WHERE version >= ?`,
	} {
		if statement == `DELETE FROM schema_version WHERE version >= ?` {
			_, err = s.db.Exec(statement, len(migrations)-2)
		} else {
			_, err = s.db.Exec(statement)
		}
		if err != nil {
			t.Fatal(err)
		}
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

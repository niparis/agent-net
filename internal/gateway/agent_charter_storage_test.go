package gateway

import (
	"path/filepath"
	"testing"
)

func TestAgentCharterMigrationReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.db")
	for i := 0; i < 2; i++ {
		s, err := OpenStore(path)
		if err != nil {
			t.Fatal(err)
		}
		var count int
		if err := s.db.QueryRow(`SELECT count(*) FROM agent_charter`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("agent charter starts nonempty: %d", count)
		}
		if err := s.db.QueryRow(`SELECT count(*) FROM agent_charter_history`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("agent charter history starts nonempty: %d", count)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

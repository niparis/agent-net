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
		// Only the CTO's default charter is there, seeded once (decision 0159).
		if count != len(ctoDefaultCharter) {
			t.Fatalf("agent charter starts with %d rows, want the %d CTO defaults", count, len(ctoDefaultCharter))
		}
		if err := s.db.QueryRow(`SELECT count(*) FROM agent_charter_history`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != len(ctoDefaultCharter) {
			t.Fatalf("agent charter history starts with %d rows, want the %d CTO defaults", count, len(ctoDefaultCharter))
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAgentCharterHistory(t *testing.T) {
	r := newDeliveryRig(t)
	for _, value := range []string{"first", "second", ""} {
		if err := r.s.SetAgentCharter("demo", "purpose", value, "owner"); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := r.s.db.QueryRow(`SELECT count(*) FROM agent_charter_history WHERE agent_id='demo'`).Scan(&count); err != nil || count != 3 {
		t.Fatalf("history %d: %v", count, err)
	}
	if err := r.s.db.QueryRow(`SELECT count(*) FROM agent_charter WHERE agent_id='demo'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("current %d: %v", count, err)
	}
}

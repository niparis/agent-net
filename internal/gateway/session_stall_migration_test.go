package gateway

import (
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A partially rewound schema can retain either stall column. Replaying the
// migration must add only what is missing and preserve existing observations.
func TestSessionStallMigrationReplay(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		keepActivity, keepLost bool
	}{
		{"both", true, true},
		{"activity_only", true, false},
		{"lost_only", false, true},
		{"neither", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, c := newTestStore(t)
			node, _, err := s.CreateNode("test-host", "linux")
			if err != nil {
				t.Fatal(err)
			}
			id, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "test", Status: "busy"})
			if err != nil {
				t.Fatal(err)
			}
			old := c.t.Add(-45 * time.Minute).UnixNano()
			if _, err := s.db.Exec(`UPDATE sessions SET harness_activity=?,harness_lost=1 WHERE id=?`, old, id); err != nil {
				t.Fatal(err)
			}
			if !tc.keepActivity {
				if _, err := s.db.Exec(`ALTER TABLE sessions DROP COLUMN harness_activity`); err != nil {
					t.Fatal(err)
				}
			}
			if !tc.keepLost {
				if _, err := s.db.Exec(`ALTER TABLE sessions DROP COLUMN harness_lost`); err != nil {
					t.Fatal(err)
				}
			}
			version := migrationVersion(t, "Session harness activity and unexpected loss")
			if version != 129 {
				t.Fatalf("stall migration = %d, want 129", version)
			}
			replay := func() {
				t.Helper()
				if _, err := s.db.Exec(`DELETE FROM schema_version WHERE version>=?`, version); err != nil {
					t.Fatal(err)
				}
				if err := s.migrate(); err != nil {
					t.Fatal(err)
				}
			}
			replay()
			var active, lost int64
			if err := s.db.QueryRow(`SELECT harness_activity,harness_lost FROM sessions WHERE id=?`, id).Scan(&active, &lost); err != nil {
				t.Fatal(err)
			}
			wantActive := old
			if !tc.keepActivity {
				wantActive = c.t.UnixNano()
			}
			wantLost := int64(0)
			if tc.keepLost {
				wantLost = 1
			}
			if active != wantActive || lost != wantLost {
				t.Fatalf("after replay: activity=%d lost=%d; want %d %d", active, lost, wantActive, wantLost)
			}
			// A second replay must not reinitialize the already present columns.
			if _, err := s.db.Exec(`UPDATE sessions SET harness_activity=?,harness_lost=1 WHERE id=?`, old, id); err != nil {
				t.Fatal(err)
			}
			replay()
			if err := s.db.QueryRow(`SELECT harness_activity,harness_lost FROM sessions WHERE id=?`, id).Scan(&active, &lost); err != nil {
				t.Fatal(err)
			}
			if active != old || lost != 1 {
				t.Fatalf("second replay overwrote observations: %d %d", active, lost)
			}
		})
	}
}

package gateway

import (
	"testing"
)

// An offline or harness-lost chief says so on the status line.
func TestChiefOfflineStatusReasons(t *testing.T) {
	s, _, chief, _, _ := chiefRig(t)
	if _, err := s.db.Exec(`UPDATE sessions SET online=0 WHERE id=?`, chief); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.ChiefStatus(); st.Down != "session offline" {
		t.Fatalf("down = %q, want session offline", st.Down)
	}
	if _, err := s.db.Exec(`UPDATE sessions SET harness_lost=1 WHERE id=?`, chief); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.ChiefStatus(); st.Down != "harness lost" {
		t.Fatalf("down = %q, want harness lost", st.Down)
	}
}

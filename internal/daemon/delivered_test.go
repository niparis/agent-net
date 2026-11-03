package daemon

import (
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The delivered table is only a duplicate check. Rows stay for the message
// expiry plus a week, so a message that is still alive can never be delivered
// twice, and older rows go.
func TestPruneDeliveredKeepsRecentRows(t *testing.T) {
	st, err := openState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()

	now := time.Now()
	cutoff := now.Add(-(protocol.DefaultTTL + 7*24*time.Hour))
	rows := []struct {
		id string
		at time.Time
	}{
		{"old", cutoff.Add(-time.Hour)},
		{"just-expired", cutoff.Add(-time.Second)},
		{"kept-old", cutoff.Add(time.Hour)},
		{"recent", now.Add(-time.Hour)},
	}
	for _, row := range rows {
		if _, err := st.db.Exec(`INSERT INTO delivered (message_id, at) VALUES (?,?)`, row.id, row.at.Unix()); err != nil {
			t.Fatal(err)
		}
	}

	n, err := st.pruneDelivered(now)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("pruned %d rows, want 2", n)
	}
	for _, id := range []string{"old", "just-expired"} {
		if st.isDelivered(id) {
			t.Errorf("%s was kept", id)
		}
	}
	for _, id := range []string{"kept-old", "recent"} {
		if !st.isDelivered(id) {
			t.Errorf("%s was pruned", id)
		}
	}
}

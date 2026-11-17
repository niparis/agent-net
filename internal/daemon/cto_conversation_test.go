package daemon

import (
	"testing"
	"time"
)

// Decision 0171: the CTO's conversations live in session_conversations. The
// migration copies the two old columns once, and the copies count as stale.
func TestCTOConversationMigrationCopiesOnce(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	st, err := openState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	d := &Daemon{st: st}
	// An install from before the table: the CTO's conversations are in the old
	// columns, and the migration has not run.
	if _, err := st.db.Exec(`DROP TABLE session_conversations`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE cto SET session_id='old-opencode', claude_session_id='old-claude', conversations_migrated=0 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if err := migrateCTOState(st.db); err != nil {
		t.Fatal(err)
	}
	for harness, want := range map[string]string{"opencode": "old-opencode", "claude": "old-claude"} {
		got, err := d.ctoConversation(harness)
		if err != nil || got != want {
			t.Fatalf("%s conversation after migration: %q %v, want %q", harness, got, err, want)
		}
		var updated int64
		if err := st.db.QueryRow(`SELECT updated_at FROM session_conversations WHERE session_id=? AND harness=?`, ctoConversationSession, harness).Scan(&updated); err != nil || updated != 0 {
			t.Fatalf("%s copy is not stale: updated_at %d %v", harness, updated, err)
		}
	}

	// Ending a conversation must stick: a later start does not copy the old
	// column back in.
	if err := d.setCTOConversation("opencode", ""); err != nil {
		t.Fatal(err)
	}
	if err := migrateCTOState(st.db); err != nil {
		t.Fatal(err)
	}
	if got, _ := d.ctoConversation("opencode"); got != "" {
		t.Fatalf("a second migration copied the old column back: %q", got)
	}
}

// A switch resumes a harness's conversation used in the last 24 hours. An older
// one is dropped, so the new conversation starts from the handover note.
func TestCTOSwitchResumesOnlyFreshConversations(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	st, err := openState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	start := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	now := start
	d := &Daemon{st: st, ctoNow: func() time.Time { return now }}

	if err := d.setCTOConversation("claude", "recent"); err != nil {
		t.Fatal(err)
	}
	now = start.Add(23 * time.Hour)
	if err := d.ctoSwitchConversation("claude"); err != nil {
		t.Fatal(err)
	}
	if got, _ := d.ctoConversation("claude"); got != "recent" {
		t.Fatalf("a conversation used 23 hours ago was dropped: %q", got)
	}

	// Last used 23 hours in, so 25 hours after that it is stale.
	if err := d.setCTOConversation("claude", "old"); err != nil {
		t.Fatal(err)
	}
	now = start.Add(48 * time.Hour)
	if err := d.ctoSwitchConversation("claude"); err != nil {
		t.Fatal(err)
	}
	if got, _ := d.ctoConversation("claude"); got != "" {
		t.Fatalf("a conversation used 25 hours ago was resumed: %q", got)
	}
}

// A turn keeps a conversation fresh: a switch within a day after the last turn
// resumes it, however old the start was.
func TestCTOTurnRefreshesConversationAge(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	st, err := openState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	start := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	now := start
	d := &Daemon{st: st, ctoNow: func() time.Time { return now }}
	if err := d.setCTOConversation("opencode", "long-running"); err != nil {
		t.Fatal(err)
	}
	now = start.Add(30 * time.Hour)
	if err := d.touchCTOConversation("opencode"); err != nil {
		t.Fatal(err)
	}
	now = start.Add(40 * time.Hour)
	if err := d.ctoSwitchConversation("opencode"); err != nil {
		t.Fatal(err)
	}
	if got, _ := d.ctoConversation("opencode"); got != "long-running" {
		t.Fatalf("a turn 10 hours before the switch did not keep the conversation: %q", got)
	}
}

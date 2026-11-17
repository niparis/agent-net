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

// openConversationState opens a daemon state for a conversation test.
func openConversationState(t *testing.T) *state {
	t.Helper()
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	st, err := openState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.close() })
	return st
}

func setCTONetworkID(t *testing.T, st *state, id string) {
	t.Helper()
	if _, err := st.db.Exec(`INSERT INTO cto_network(id,session_id) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET session_id=excluded.session_id`, id); err != nil {
		t.Fatal(err)
	}
}

// The CTO's conversations are keyed by its network session ID, the key every
// other session uses (decision 0171, phase 2), not by a name only the CTO has.
func TestCTOConversationIsKeyedByTheNetworkSession(t *testing.T) {
	st := openConversationState(t)
	d := &Daemon{st: st}
	setCTONetworkID(t, st, "s_cto_net")
	if err := d.setCTOConversation("claude", "native-1"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM session_conversations WHERE session_id='s_cto_net' AND harness='claude' AND native_id='native-1'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("rows under the network session ID: %d %v, want 1", n, err)
	}
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM session_conversations WHERE session_id=?`, ctoConversationSession).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rows under the old key: %d %v, want 0", n, err)
	}
	if got, _ := d.ctoConversation("claude"); got != "native-1" {
		t.Fatalf("conversation = %q, want native-1", got)
	}
}

// A conversation made before the CTO had a network ID moves to that ID when it
// is registered, with its age, so a switch back still resumes it. A conversation
// already under the new ID is not overwritten by the old one.
func TestCTORegistrationMovesTheEarlierConversations(t *testing.T) {
	st := openConversationState(t)
	d := &Daemon{st: st}
	if err := d.setCTOConversation("claude", "before-registration"); err != nil {
		t.Fatal(err)
	}
	if err := d.setCTOConversation("opencode", "older-opencode"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`INSERT INTO session_conversations(session_id,harness,native_id,updated_at) VALUES('s_cto_net','opencode','newer-opencode',5)`); err != nil {
		t.Fatal(err)
	}
	var claudeAge int64
	if err := st.db.QueryRow(`SELECT updated_at FROM session_conversations WHERE session_id=? AND harness='claude'`, ctoConversationSession).Scan(&claudeAge); err != nil {
		t.Fatal(err)
	}
	if err := d.rekeyCTOConversations("s_cto_net"); err != nil {
		t.Fatal(err)
	}
	var native string
	var updated int64
	if err := st.db.QueryRow(`SELECT native_id,updated_at FROM session_conversations WHERE session_id='s_cto_net' AND harness='claude'`).Scan(&native, &updated); err != nil || native != "before-registration" || updated != claudeAge {
		t.Fatalf("moved claude row = %q %d %v, want before-registration with its age %d", native, updated, err, claudeAge)
	}
	if err := st.db.QueryRow(`SELECT native_id FROM session_conversations WHERE session_id='s_cto_net' AND harness='opencode'`).Scan(&native); err != nil || native != "newer-opencode" {
		t.Fatalf("opencode row = %q %v, want the row already under the new ID", native, err)
	}
	var left int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM session_conversations WHERE session_id=?`, ctoConversationSession).Scan(&left); err != nil || left != 0 {
		t.Fatalf("rows left under the old key: %d %v", left, err)
	}
}

// Opening the state of an install that already has a network ID and rows under
// the old key moves them once, with their age.
func TestCTOStateOpenMovesRowsUnderTheOldKey(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	dir := t.TempDir()
	st, err := openState(dir)
	if err != nil {
		t.Fatal(err)
	}
	setCTONetworkID(t, st, "s_cto_net")
	if _, err := st.db.Exec(`INSERT INTO session_conversations(session_id,harness,native_id,updated_at) VALUES(?,'claude','kept',42)`, ctoConversationSession); err != nil {
		t.Fatal(err)
	}
	st.close()
	st, err = openState(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	var native string
	var updated int64
	if err := st.db.QueryRow(`SELECT native_id,updated_at FROM session_conversations WHERE session_id='s_cto_net' AND harness='claude'`).Scan(&native, &updated); err != nil || native != "kept" || updated != 42 {
		t.Fatalf("row after reopen = %q %d %v, want kept with age 42", native, updated, err)
	}
}

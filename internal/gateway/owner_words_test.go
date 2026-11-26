package gateway

import (
	"testing"
	"time"
)

// A quote the owner typed in any session counts for the window, ignoring case
// and white space, and names the session and the time he typed it.
func TestOwnerWordsFoundInAnySessionWithinTheWindow(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	typed := c.now().Add(-time.Hour)
	if err := s.KeepOwnerWords(a, "fix the login page", typed); err != nil {
		t.Fatal(err)
	}
	if err := s.KeepOwnerWords(b, "Start a  Codex\nworker on M2Max for uadd3", typed); err != nil {
		t.Fatal(err)
	}
	got, err := s.FindOwnerWords("codex worker on m2max")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Found || got.Session != b || got.At != typed.UnixNano() {
		t.Fatalf("found %+v, want session %s at %d", got, b, typed.UnixNano())
	}

	// His words live in memory only: gateway.db, and so every backup of it,
	// never holds them.
	var tables int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE sql LIKE '%owner_words (%' OR name='owner_words'`).Scan(&tables); err != nil || tables != 0 {
		t.Fatalf("gateway.db has %d owner words tables (err %v)", tables, err)
	}

	// Four hours after he typed it, the quote no longer counts.
	c.advance(3 * time.Hour)
	if got, _ := s.FindOwnerWords("codex worker on m2max"); got.Found {
		t.Fatalf("words typed four hours ago were found: %+v", got)
	}
	// Expired words are dropped from memory, not only hidden.
	s.ownerWords.mu.Lock()
	kept := len(s.ownerWords.by)
	s.ownerWords.mu.Unlock()
	if kept != 0 {
		t.Fatalf("%d sessions still hold expired words", kept)
	}
	// Words that were already older than the window are not kept at all.
	if err := s.KeepOwnerWords(a, "use a codex worker for this", c.now().Add(-4*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.FindOwnerWords("use a codex worker"); got.Found {
		t.Fatalf("old words were kept: %+v", got)
	}
}

// A turn a harness injected is never the owner's words.
func TestOwnerWordsDropInjectedTurns(t *testing.T) {
	s, c := newTestStore(t)
	a, _ := twoSessions(t, s)
	if err := s.KeepOwnerWords(a, "<task-notification>use a codex worker for this</task-notification>", c.now()); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.FindOwnerWords("use a codex worker"); got.Found {
		t.Fatalf("an injected turn counted as the owner's words: %+v", got)
	}
}

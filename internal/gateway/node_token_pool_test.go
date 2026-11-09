package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Every HTTP request of a daemon (the CTO settings, the protected providers,
// the worker floors and the other settings the TUI reads each refresh) checks
// its token with NodeByToken. The check only reads, but it ran in a
// transaction on the one writer connection, so each of those requests waited
// behind every write the gateway was doing. A slow write then held up all of
// them at once. A token that needs no change must be checked on the read pool.
func TestNodeByTokenDoesNotWaitForTheWriterConnection(t *testing.T) {
	s, _ := newTestStore(t)
	if s.db.r.Load() == nil {
		t.Skip("the read pool is off")
	}
	id, token, err := s.CreateNode("box", "linux")
	if err != nil {
		t.Fatal(err)
	}
	w, err := s.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	type answer struct {
		id string
		ok bool
	}
	done := make(chan answer, 1)
	go func() {
		got, ok, _ := s.NodeByToken(token)
		done <- answer{got, ok}
	}()
	select {
	case a := <-done:
		if !a.ok || a.id != id {
			t.Fatalf("NodeByToken = %+v, want %s", a, id)
		}
	case <-time.After(time.Second):
		t.Fatal("NodeByToken waited for the writer connection")
	}
}

// A staged token still becomes the token when the daemon shows it, and a
// removed machine or an unknown token is still refused.
func TestNodeByTokenStillPromotesAStagedToken(t *testing.T) {
	s, _ := newTestStore(t)
	id, _, err := s.CreateNode("box", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.stageNodeToken(id, "staged-secret"); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.NodeByToken("staged-secret")
	if err != nil || !ok || got != id {
		t.Fatalf("staged token: %s %v %v", got, ok, err)
	}
	var pending string
	if err := s.db.QueryRow(`SELECT pending_token_hash FROM nodes WHERE id=?`, id).Scan(&pending); err != nil || pending != "" {
		t.Fatalf("pending token not promoted: %q %v", pending, err)
	}
	if _, ok, _ := s.NodeByToken("staged-secret"); !ok {
		t.Fatal("promoted token refused")
	}
	if _, ok, _ := s.NodeByToken("nope"); ok {
		t.Fatal("unknown token accepted")
	}
}

// query.nodes is read by every TUI refresh and by every proposals list. It
// only reads, but PlaybookNotes began with an UPDATE that rarely changed a
// row. Even an UPDATE that changes nothing takes the writer connection, so
// every nodes read waited behind each write.
func TestNodesInfoDoesNotWaitForTheWriterConnection(t *testing.T) {
	s, _ := newTestStore(t)
	if s.db.r.Load() == nil {
		t.Skip("the read pool is off")
	}
	g := &Gateway{store: s, conns: map[string]*nodeConn{}, reqs: newRequestStats(time.Now())}
	id, _, err := s.CreateNode("box", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetNodeHello(id, "box", "1"); err != nil {
		t.Fatal(err)
	}
	if _, err = g.NodesInfo(); err != nil { // warm the read caches
		t.Fatal(err)
	}
	w, err := s.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	done := make(chan error, 1)
	go func() {
		_, err := g.NodesInfo()
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("NodesInfo waited for the writer connection")
	}
}

// A stale automatic note is still hidden once it is old enough, and a pinned
// or fresh note stays.
func TestPlaybookNotesStillHideStaleAutoNotes(t *testing.T) {
	s, clk := newTestStore(t)
	insert := func(subject string, source string, pinned int, updated time.Time) {
		t.Helper()
		if _, err := s.db.Exec(`INSERT INTO playbook (subject_kind, subject, harness, note, source, support, track_line, pinned, stale, created_at, updated_at)
			VALUES ('model', ?, 'claude', 'n', ?, 0, '', ?, 0, ?, ?)`, subject, source, pinned, updated.Unix(), updated.Unix()); err != nil {
			t.Fatal(err)
		}
	}
	old := clk.now().Add(-100 * 24 * time.Hour)
	insert("old-auto", protocol.PlaybookSourceAuto, 0, old)
	insert("old-pinned", protocol.PlaybookSourceAuto, 1, old)
	insert("fresh-auto", protocol.PlaybookSourceAuto, 0, clk.now())
	notes, err := s.PlaybookNotes()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, n := range notes {
		got[n.Subject] = true
	}
	if got["old-auto"] || !got["old-pinned"] || !got["fresh-auto"] {
		t.Fatalf("notes shown: %v", got)
	}
}

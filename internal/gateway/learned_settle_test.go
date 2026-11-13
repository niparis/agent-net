package gateway

import (
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// majorEdgeAsk makes a ready major learned edge suggestion (app depends on
// ops) and returns its id and the open ask for it.
func majorEdgeAsk(t *testing.T, s *Store) (int64, protocol.Ask) {
	t.Helper()
	id := insertLearned(t, s, "app", "edge", "depends", "add", "ops", "flow")
	if _, err := s.db.Exec(`UPDATE learned_suggestions SET severity='major' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	if err := s.reconcileLearnedAsks(); err != nil {
		t.Fatal(err)
	}
	asks, err := s.ListAsks()
	if err != nil {
		t.Fatal(err)
	}
	open := askByKind(asks, protocol.AskKindLearned)
	if len(open) != 1 {
		t.Fatalf("want one learned ask, got %+v", open)
	}
	return id, open[0]
}

func learnedStatus(t *testing.T, s *Store, id int64) string {
	t.Helper()
	var status string
	if err := s.db.QueryRow(`SELECT status FROM learned_suggestions WHERE id=?`, id).Scan(&status); err != nil {
		t.Fatal(err)
	}
	return status
}

func noOpenLearnedAsk(t *testing.T, s *Store) {
	t.Helper()
	asks, err := s.ListAsks()
	if err != nil {
		t.Fatal(err)
	}
	if open := askByKind(asks, protocol.AskKindLearned); len(open) != 0 {
		t.Fatalf("a new learned ask was raised: %+v", open)
	}
}

// The CTO sets the edge itself and answers the ask. The suggestion must not
// stay pending, or the next sweep asks again.
func TestLearnedEdgeAlreadySetIsAppliedAndNotAskedAgain(t *testing.T) {
	s, _, _, _ := learnedProjects(t)
	id, ask := majorEdgeAsk(t, s)
	if err := s.SetEdge(protocol.Edge{From: "app", To: "ops", Kind: "depends"}, "cto"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE inbox_items SET status='answered',answer='Done' WHERE id=?`, ask.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.ProcessLearned(); err != nil {
		t.Fatal(err)
	}
	if err := s.reconcileLearnedAsks(); err != nil {
		t.Fatal(err)
	}
	if got := learnedStatus(t, s, id); got != "applied" {
		t.Fatalf("status %s, want applied", got)
	}
	noOpenLearnedAsk(t, s)
}

// An answer from the CTO decides the suggestion behind the ask, with the
// option words or with free text.
func TestAnsweringALearnedAskSettlesTheSuggestion(t *testing.T) {
	for _, c := range []struct {
		name, answer, want string
		edge               bool
	}{
		{"accept option", "accept", "applied", true},
		{"reject option", "reject", "rejected", false},
		{"free text carried out", "Done: depends edge set", "applied", false},
		{"free text refusal", "No, leave it", "rejected", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, _, _, _ := learnedProjects(t)
			id, ask := majorEdgeAsk(t, s)
			if _, err := s.AnswerInboxAs(ask.ID, c.answer, "cto"); err != nil {
				t.Fatal(err)
			}
			if got := learnedStatus(t, s, id); got != c.want {
				t.Fatalf("status %s, want %s", got, c.want)
			}
			if err := s.reconcileLearnedAsks(); err != nil {
				t.Fatal(err)
			}
			noOpenLearnedAsk(t, s)
			g, err := s.Graph()
			if err != nil {
				t.Fatal(err)
			}
			if c.edge != (len(g.Edges) == 1) {
				t.Fatalf("edges %+v, want edge %v", g.Edges, c.edge)
			}
		})
	}
}

// The owner's own answer path needs no change: validate-only answers never
// settle anything.
func TestValidatingALearnedAnswerChangesNothing(t *testing.T) {
	s, _, _, _ := learnedProjects(t)
	id, ask := majorEdgeAsk(t, s)
	if _, err := s.AnswerInboxAs(ask.ID, "accept", "cto", true); err != nil {
		t.Fatal(err)
	}
	if got := learnedStatus(t, s, id); got != "pending" {
		t.Fatalf("validate changed the suggestion: %s", got)
	}
}

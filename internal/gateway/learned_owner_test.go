package gateway

import (
	"context"
	"testing"
	"time"
)

func TestLearnedOwnerReviewAndUndo(t *testing.T) {
	s, c, _, _ := learnedProjects(t)
	id := insertLearned(t, s, "app", "refuses", "", "add", "deploy changes", "outcome")
	if err := s.ProcessLearned(); err != nil {
		t.Fatal(err)
	}
	list, err := s.OwnerLearned()
	if err != nil || len(list) != 1 || list[0].Status != "pending" || list[0].Severity != "major" {
		t.Fatalf("pending %+v %v", list, err)
	}
	if err = s.DecideLearned(id, "accept"); err != nil {
		t.Fatal(err)
	}
	h, err := s.CharterHistory("app", 10)
	if err != nil || len(h) != 1 || h[0].By != "owner" || h[0].NewValue != "deploy changes" {
		t.Fatalf("history %+v %v", h, err)
	}
	if err = s.GraphUndo(h[0].ID, "owner"); err != nil {
		t.Fatal(err)
	}
	list, err = s.OwnerLearned()
	if err != nil || len(list) != 1 || list[0].Status != "undone" {
		t.Fatalf("undo %+v %v", list, err)
	}
	c.t = c.t.Add(time.Second)
	id = insertLearned(t, s, "app", "refuses", "", "add", "other work", "outcome")
	if err = s.ProcessLearned(); err != nil {
		t.Fatal(err)
	}
	if err = s.DecideLearned(id, "reject"); err != nil {
		t.Fatal(err)
	}
	if err = s.DecideLearned(id, "accept"); err == nil {
		t.Fatal("accepted rejected suggestion")
	}
}
func TestLearnedViewedIsPerNodeAndMonotonic(t *testing.T) {
	s, _, _, _ := learnedProjects(t)
	a := insertLearned(t, s, "app", "needs", "", "add", "ops backups", "owner")
	insertLearned(t, s, "ops", "needs", "", "add", "app docs", "owner")
	if err := s.ProcessLearned(); err != nil {
		t.Fatal(err)
	}
	v, err := s.OwnerLearned()
	if err != nil || len(v) != 2 || !v[0].Unseen || !v[1].Unseen {
		t.Fatalf("unseen %+v %v", v, err)
	}
	if err = s.MarkLearnedViewed("app", a); err != nil {
		t.Fatal(err)
	}
	v, err = s.OwnerLearned()
	if err != nil || v[0].Unseen || !v[1].Unseen {
		t.Fatalf("viewed %+v %v", v, err)
	}
	if err = s.MarkLearnedViewed("ops", a); err == nil {
		t.Fatal("marked another node's change")
	}
}
func TestLearnedGatewayRequiresOwner(t *testing.T) {
	s, _ := newTestStore(t)
	a, _ := twoSessions(t, s)
	nc := &nodeConn{id: mustNode(t, s, a)}
	g := &Gateway{store: s}
	for _, method := range []string{"learned.list", "learned.answer", "learned.view"} {
		if _, err := g.handle(context.Background(), nc, method, mustJSON(t, map[string]any{"owner": a, "id": 1, "node": "app", "action": "accept"})); err == nil {
			t.Fatalf("%s accepted agent", method)
		}
	}
}

func TestLearnedViewedTracksApplicationOrder(t *testing.T) {
	s, c, _, _ := learnedProjects(t)
	early := insertLearned(t, s, "app", "needs", "", "add", "later item", "owner")
	if _, err := s.db.Exec(`UPDATE learned_suggestions SET ready_at=? WHERE id=?`, c.t.Add(time.Minute).UnixNano(), early); err != nil {
		t.Fatal(err)
	}
	later := insertLearned(t, s, "app", "needs", "", "add", "first item", "owner")
	if err := s.ProcessLearned(); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkLearnedViewed("app", later); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(2 * time.Minute)
	if err := s.ProcessLearned(); err != nil {
		t.Fatal(err)
	}
	v, err := s.OwnerLearned()
	if err != nil || len(v) != 2 || !v[0].Unseen || v[1].Unseen {
		t.Fatalf("application order: %+v %v", v, err)
	}
}

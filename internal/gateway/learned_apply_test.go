package gateway

import (
	"context"
	"fmt"
	"io"
	"log"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestLearnedClassify(t *testing.T) {
	cases := []struct{ field, kind, edge, origin, want string }{
		{"accepts", "add", "", "owner", "minor"}, {"accepts", "add", "", "peer", "major"},
		{"needs", "add", "", "peer", "minor"}, {"consumers", "add", "", "peer", "minor"},
		{"edge", "add", "depends", "peer", "minor"}, {"edge", "add", "governs", "peer", "major"}, {"edge", "add", "deploys", "peer", "major"},
		{"needs", "remove", "", "owner", "major"}, {"needs", "narrow", "", "owner", "major"},
		{"refuses", "add", "", "owner", "major"}, {"escalate", "add", "", "owner", "major"}, {"maturity", "add", "", "owner", "major"},
		{"deploy_rules", "add", "", "owner", "major"}, {"request_rules", "add", "", "owner", "major"}, {"unknown", "add", "", "owner", "major"},
	}
	for _, c := range cases {
		t.Run(fmt.Sprint(c), func(t *testing.T) {
			v := LearnedSuggestion{Field: c.field, Kind: c.kind, EdgeKind: c.edge, Origin: c.origin}
			if got := learnedClass(v); got != c.want {
				t.Fatalf("got %s want %s", got, c.want)
			}
		})
	}
}
func insertLearned(t *testing.T, s *Store, node, field, edge, kind, phrase, origin string) int64 {
	t.Helper()
	res, err := s.db.Exec(`INSERT INTO learned_suggestions(node,field,edge_kind,kind,phrase,evidence,status,severity,created_at,ready_at) VALUES(?,?,?,?,?,'[]','pending','minor',?,?)`, node, field, edge, kind, phrase, s.now().UnixNano(), s.now().UnixNano())
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO learned_suggestion_details(suggestion_id,origin) VALUES(?,?)`, id, origin); err != nil {
		t.Fatal(err)
	}
	return id
}
func TestLearnedApplyHistoryAndUndo(t *testing.T) {
	s, _, _, _ := learnedProjects(t)
	id := insertLearned(t, s, "app", "needs", "", "add", "ops for backups", "owner")
	edgeID := insertLearned(t, s, "app", "edge", "depends", "add", "ops", "flow")
	if err := s.ProcessLearned(); err != nil {
		t.Fatal(err)
	}
	v := learnedRead(t, s, false)
	if len(v) != 2 || v[0].Status != "applied" || v[1].Status != "applied" {
		t.Fatalf("suggestions: %+v", v)
	}
	h, err := s.CharterHistory("app", 10)
	if err != nil || len(h) != 1 || h[0].By != "learned" || h[0].NewValue != "ops for backups" {
		t.Fatalf("history=%+v err=%v", h, err)
	}
	eh, err := s.EdgeHistory("app", 10)
	if err != nil || len(eh) != 1 || eh[0].By != "learned" {
		t.Fatalf("edges=%+v err=%v", eh, err)
	}
	if err = s.GraphUndo(h[0].ID, "owner"); err != nil {
		t.Fatal(err)
	}
	if err = s.GraphUndo(eh[0].ID, "owner"); err != nil {
		t.Fatal(err)
	}
	for _, x := range learnedRead(t, s, false) {
		if (x.ID == id || x.ID == edgeID) && x.Status != "undone" {
			t.Fatalf("undo status: %+v", x)
		}
	}
	g, err := s.Graph()
	if err != nil || len(g.Edges) != 0 {
		t.Fatalf("graph %+v err %v", g, err)
	}
}
func TestLearnedCriticalAndCap(t *testing.T) {
	s, c, _, _ := learnedProjects(t)
	if err := s.SetCharter("ops", "maturity", "critical - production", "owner"); err != nil {
		t.Fatal(err)
	}
	critical := insertLearned(t, s, "ops", "needs", "", "add", "help", "owner")
	for i := 0; i < 4; i++ {
		insertLearned(t, s, "app", "needs", "", "add", fmt.Sprintf("item %d", i), "owner")
	}
	if err := s.ProcessLearned(); err != nil {
		t.Fatal(err)
	}
	v := learnedRead(t, s, false)
	var applied, major int
	for _, x := range v {
		if x.ID == critical && (x.Status != "pending" || x.Severity != "major") {
			t.Fatalf("critical %+v", x)
		}
		if x.Node == "app" && x.Status == "applied" {
			applied++
		}
		if x.Node == "app" && x.Severity == "major" {
			major++
		}
	}
	if applied != 3 || major != 1 {
		t.Fatalf("cap: %+v", v)
	}
	c.t = c.t.Add(24*time.Hour + time.Nanosecond)
	// A promoted suggestion stays major, even after the rolling window expires.
	if err := s.ProcessLearned(); err != nil {
		t.Fatal(err)
	}
	v = learnedRead(t, s, false)
	if v[4].Status != "pending" {
		t.Fatalf("promoted suggestion applied: %+v", v)
	}
	insertLearned(t, s, "app", "consumers", "", "add", "new client", "owner")
	if err := s.ProcessLearned(); err != nil {
		t.Fatal(err)
	}
	v = learnedRead(t, s, false)
	if v[len(v)-1].Status != "applied" {
		t.Fatalf("rolling cap: %+v", v)
	}
}
func TestLearnedRejectedNeverOfferedAgain(t *testing.T) {
	s, _, a, b := learnedProjects(t)
	if err := s.RememberOwnerPrompt(a, "ops: backups are his job"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := send(s, a, b, "", ""); err != nil {
		t.Fatal(err)
	}
	v := learnedRead(t, s, false)
	p := learnedFind(v, "ops", "accepts", "backups")
	if p == nil {
		t.Fatal(v)
	}
	if err := s.RejectLearnedSuggestion(p.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := send(s, a, b, "", ""); err != nil {
		t.Fatal(err)
	}
	v = learnedRead(t, s, false)
	count := 0
	for _, x := range v {
		if x.Node == "ops" && x.Field == "accepts" && x.Phrase == "backups" {
			count++
			if x.Status != "rejected" {
				t.Fatalf("reoffered: %+v", v)
			}
		}
	}
	if count != 1 {
		t.Fatalf("reoffered: %+v", v)
	}
}
func TestLearnedApplyRollback(t *testing.T) {
	s, _, _, _ := learnedProjects(t)
	insertLearned(t, s, "app", "needs", "", "add", "ops for backups", "owner")
	if _, err := s.db.Exec(`CREATE TRIGGER fail_learned BEFORE UPDATE OF status ON learned_suggestions WHEN NEW.status='applied' BEGIN SELECT RAISE(FAIL,'test'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.ProcessLearned(); err == nil {
		t.Fatal("expected failure")
	}
	h, err := s.CharterHistory("app", 10)
	if err != nil || len(h) != 0 {
		t.Fatalf("partial update: %+v %v", h, err)
	}
}

func TestLearnedEdgeCriticalTargetAndExistingRule(t *testing.T) {
	s, _, _, _ := learnedProjects(t)
	if err := s.SetCharter("ops", "maturity", "critical", "owner"); err != nil {
		t.Fatal(err)
	}
	insertLearned(t, s, "app", "edge", "depends", "add", "ops", "flow")
	if err := s.ProcessLearned(); err != nil {
		t.Fatal(err)
	}
	v := learnedRead(t, s, false)
	if len(v) != 1 || v[0].Severity != "major" || v[0].Status != "pending" {
		t.Fatalf("critical endpoint: %+v", v)
	}
	if err := s.SetCharter("ops", "maturity", "mature", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEdge(protocol.Edge{From: "app", To: "ops", Kind: "depends", Rule: "never"}, "owner"); err != nil {
		t.Fatal(err)
	}
	insertLearned(t, s, "app", "edge", "depends", "add", "ops", "flow")
	if err := s.ProcessLearned(); err != nil {
		t.Fatal(err)
	}
	g, err := s.Graph()
	if err != nil || len(g.Edges) != 1 || g.Edges[0].Rule != "never" {
		t.Fatalf("rule overwritten: %+v %v", g.Edges, err)
	}
}
func TestLearnedRejectionAlsoSuppressesPendingSamePhrase(t *testing.T) {
	s, _, _, _ := learnedProjects(t)
	id := insertLearned(t, s, "app", "edge", "depends", "add", "ops", "flow")
	insertLearned(t, s, "app", "edge", "governs", "add", "ops", "flow")
	if err := s.RejectLearnedSuggestion(id); err != nil {
		t.Fatal(err)
	}
	for _, v := range learnedRead(t, s, false) {
		if v.Status != "rejected" {
			t.Fatalf("still offered: %+v", v)
		}
	}
}

func TestLearnedOwnerAndMajorApply(t *testing.T) {
	s, c, a, b := learnedProjects(t)
	if err := s.RememberOwnerPrompt(a, "ops: backups are his job"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := send(s, a, b, "", ""); err != nil {
		t.Fatal(err)
	}
	insertLearned(t, s, "app", "refuses", "", "add", "deployments", "outcome")
	c.t = c.t.Add(2 * time.Minute)
	if err := s.ProcessLearned(); err != nil {
		t.Fatal(err)
	}
	v := learnedRead(t, s, false)
	for _, x := range v {
		if x.Field == "refuses" && (x.Status != "pending" || x.Severity != "major") {
			t.Fatalf("major: %+v", x)
		}
		if (x.Field == "needs" || x.Field == "accepts") && x.Status != "applied" {
			t.Fatalf("owner additions: %+v", x)
		}
	}
}
func TestLearnedRunLearningProcessesOnStartup(t *testing.T) {
	s, _, _, _ := learnedProjects(t)
	insertLearned(t, s, "app", "needs", "", "add", "ops for backups", "owner")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	g := &Gateway{store: s, logger: log.New(io.Discard, "", 0)}
	g.RunLearning(ctx)
	v := learnedRead(t, s, false)
	if len(v) != 1 || v[0].Status != "applied" {
		t.Fatalf("startup processing: %+v", v)
	}
}

func TestLearnedInvalidCandidateDoesNotBlockOthers(t *testing.T) {
	s, _, _, _ := learnedProjects(t)
	insertLearned(t, s, "app", "edge", "depends", "add", "missing", "flow")
	insertLearned(t, s, "app", "needs", "", "add", "ops", "owner")
	if err := s.ProcessLearned(); err != nil {
		t.Fatal(err)
	}
	v := learnedRead(t, s, false)
	if len(v) != 2 || v[0].Severity != "major" || v[0].Status != "pending" || v[1].Status != "applied" {
		t.Fatalf("stale candidate: %+v", v)
	}
}

func TestLearnedNarrowingWordsRequireReview(t *testing.T) {
	for _, phrase := range []string{"only backups", "backups except production", "never deploy", "restricted to staging"} {
		v := LearnedSuggestion{Field: "accepts", Kind: "add", Origin: "owner", Phrase: phrase}
		if got := learnedClass(v); got != "major" {
			t.Errorf("%q classified %s", phrase, got)
		}
	}
}

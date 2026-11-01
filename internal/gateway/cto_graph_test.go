package gateway

import (
	"encoding/json"
	"testing"
)

func TestCTOGraphClass(t *testing.T) {
	s, _, _, _ := learnedProjects(t)
	for _, c := range []struct{ tool, args, want string }{
		{"charter_set", `{"node":"app","attr":"purpose","value":"Build useful things"}`, "minor"},
		{"charter_set", `{"node":"app","attr":"accepts","value":"feature work"}`, "minor"},
		{"charter_set", `{"node":"app","attr":"needs","value":"help"}`, "minor"},
		{"charter_set", `{"node":"app","attr":"consumers","value":"clients"}`, "minor"},
		{"charter_set", `{"node":"app","attr":"notes","value":"Only local work"}`, "major"},
		{"charter_set", `{"node":"app","attr":"needs","value":""}`, "major"},
		{"charter_set", `{"node":"app","attr":"refuses","value":"deploys"}`, "major"},
		{"charter_set", `{"node":"app","attr":"escalate","value":"owner"}`, "major"},
		{"charter_set", `{"node":"app","attr":"maturity","value":"mature"}`, "major"},
		{"node_add", `{"name":"advisor"}`, "minor"},
		{"node_remove", `{"name":"ops"}`, "major"},
		{"node_settings_set", `{"node":"app","setting":"requests","value":"auto"}`, "major"},
		{"edge_set", `{"from":"app","to":"ops","kind":"depends"}`, "minor"},
		{"edge_remove", `{"from":"app","to":"ops","kind":"depends"}`, "minor"},
		{"edge_set", `{"from":"app","to":"ops","kind":"governs","mode":"owner"}`, "major"},
		{"edge_set", `{"from":"app","to":"ops","kind":"deploys"}`, "major"},
	} {
		t.Run(c.tool+c.args, func(t *testing.T) {
			got, err := s.CTOGraphClass(c.tool, json.RawMessage(c.args))
			if err != nil || got != c.want {
				t.Fatalf("class=%s err=%v want=%s", got, err, c.want)
			}
		})
	}
	if err := s.SetCharter("ops", "maturity", "critical - production", "owner"); err != nil {
		t.Fatal(err)
	}
	for _, args := range []string{`{"from":"app","to":"ops","kind":"depends"}`, `{"from":"ops","to":"app","kind":"depends"}`} {
		if got, err := s.CTOGraphClass("edge_set", json.RawMessage(args)); err != nil || got != "major" {
			t.Fatalf("critical edge: %s %v", got, err)
		}
	}
	if got, err := s.CTOGraphClass("charter_set", json.RawMessage(`{"node":"ops","attr":"purpose","value":"help"}`)); err != nil || got != "major" {
		t.Fatalf("critical: %s %v", got, err)
	}
	// The transaction enforces classification even if the daemon preflight was stale.
	if err := s.SetCharter("ops", "purpose", "help", "CTO (minor)"); err == nil {
		t.Fatal("minor actor changed critical node")
	}
	if err := s.SetCharter("app", "refuses", "work", "CTO (minor)"); err == nil {
		t.Fatal("minor actor changed refuses")
	}
	if err := s.SetCharter("app", "purpose", "help", "CTO (minor)"); err != nil {
		t.Fatal(err)
	}
	h, err := s.CTOGraphHistory(0)
	if err != nil || len(h) != 1 {
		t.Fatalf("history: %+v %v", h, err)
	}
	if got, err := s.CTOGraphClass("graph_undo", json.RawMessage(`{"id":0}`)); err != nil || got != "major" {
		t.Fatalf("undo class %s %v", got, err)
	}
	if err := s.GraphUndo(h[0].ID, "owner"); err != nil {
		t.Fatal(err)
	}
	g, err := s.Graph()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range g.Nodes {
		if n.Name == "app" && n.Charter.Purpose != "" {
			t.Fatal("undo failed")
		}
	}
}

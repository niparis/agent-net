package tui

import (
	"github.com/niparis/agent-net/internal/protocol"
	"strings"
	"testing"
)

func TestCharterFormLevelAndFields(t *testing.T) {
	src := &fakeSource{snap: fakeSnapshot()}
	m := New(src)
	m.startCharterForm(protocol.Node{Name: "demo", Ref: &protocol.GraphRef{Kind: "repo", ID: "demo"}, Charter: protocol.Charter{Purpose: "Inherited purpose"}})
	if m.charterForm.level != "repo" {
		t.Fatal(m.charterForm.level)
	}
	view := strings.Join(m.charterFormView(100, 24), "\n")
	for _, want := range []string{"repo", "maturity_note", "rules"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %s: %s", want, view)
		}
	}
	for _, old := range []string{"consumers", "needs", "notes"} {
		if strings.Contains(view, old) {
			t.Fatalf("dropped field %s", old)
		}
	}
}

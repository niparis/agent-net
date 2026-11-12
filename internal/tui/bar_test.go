package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
)

// A project's card lists its bar under the charter. A project without one
// shows no section.
func TestMapCardShowsTheProjectBar(t *testing.T) {
	v := protocol.GraphView{Nodes: []protocol.Node{
		{Name: "shop", Kind: "component", Bar: []protocol.BarRule{{ID: "B1", Text: "Performance first."}, {ID: "B2", Text: "Migrations are append-only."}}},
		{Name: "plain", Kind: "component"},
	}}
	m := mapModel(120, 40, v)
	m.applyMap(mapMsg{view: v})
	view := func(name string) string {
		m.mp.selected = name
		return ansi.Strip(strings.Join(m.mapFocusView(120, 40), "\n"))
	}
	got := view("shop")
	for _, want := range []string{"Project rules", "B1", "Performance first.", "B2", "Migrations are append-only."} {
		if !strings.Contains(got, want) {
			t.Fatalf("card lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(view("plain"), "Project rules") {
		t.Fatal("a project without a bar shows the section")
	}
}

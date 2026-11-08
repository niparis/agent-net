package graphview

import (
	"strings"
	"testing"
)

func TestGroupedOverview(t *testing.T) {
	s := Scene{Nodes: []SceneNode{{Name: "a", Label: "api"}, {Name: "b", Label: "builder"}, {Name: "c", Label: "shop"}}, Groups: []Group{{Name: "forge", Label: "forge", Members: []string{"a", "b"}, Lead: "b"}, {Name: "other", Label: "unassigned", Members: []string{"c"}}}, Edges: []SceneEdge{{FromName: "a", ToName: "c", Kind: "depends"}, {FromName: "b", ToName: "c", Kind: "depends"}}}
	s = ArrangeGroups(s, Size{80, 16})
	out := (Braille{}).Render(s, Size{80, 16})
	text := strings.Join(out.Lines, "\n")
	for _, want := range []string{"forge", "★ builder", "unassigned", "2 depends", "api", "shop"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q:\n%s", want, text)
		}
	}
	for _, name := range []string{"a", "b", "c", "forge", "other"} {
		found := false
		for _, hit := range out.Hits {
			found = found || hit == name
		}
		if !found {
			t.Errorf("%s not selectable", name)
		}
	}
}

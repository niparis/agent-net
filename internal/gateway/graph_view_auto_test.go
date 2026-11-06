package gateway

import (
	"github.com/niparis/agent-net/internal/protocol"
	"testing"
)

func TestGraphViewAutoTypedCutover(t *testing.T) {
	s, _ := newTestStore(t)
	graphSessions(t, s, "shop")
	g := &Gateway{store: s}
	for _, on := range []bool{false, true} {
		s.graphRefsOn.Store(on)
		out, err := g.handleGraphView([]byte(`{"auto_typed":true}`))
		if err != nil {
			t.Fatal(err)
		}
		v := out.(protocol.GraphView)
		if len(v.Nodes) == 0 {
			t.Fatal("no nodes")
		}
		for _, n := range v.Nodes {
			if (n.Ref != nil) != on {
				t.Fatalf("cutover %v: %+v", on, n)
			}
		}
	}
}

package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/protocol"
)

// richGraph has a node with all nine charter fields, a long purpose, a
// wildcard edge, a deploys edge and a changed rule.
func richGraph() protocol.GraphView {
	g := fakeGraph()
	for i := range g.Nodes {
		if g.Nodes[i].Name == "tenebrae" {
			g.Nodes[i].Charter = protocol.Charter{
				Purpose:         "Research platform that turns papers, figures and strategies into a searchable library for the trading desk, with citations kept for every claim.",
				Maturity:        "mature - stable since spring",
				Accepts:         "New document sources, retrieval fixes, and schema changes that keep old ids working.",
				Refuses:         "Anything that writes to the trading accounts; one-off client reports.",
				FeatureRequests: "Open an issue in the repo; the architect triages them every Monday.",
				Consumers:       "cogforge, shop, the nightly report job",
				Escalate:        "Data loss, or a change of the public ids.",
				Needs:           "MCP: tenebrae-library; a GPU box for embeddings.",
				Notes:           "Deploys go through homelab. Keep the library read only on laptops.",
			}
		}
	}
	g.Edges = append(g.Edges,
		protocol.Edge{From: "*", To: "tenebrae", Kind: "depends", Rule: "normal"},
		protocol.Edge{From: "homelab", To: "tenebrae", Kind: "deploys", Rule: "auto"},
		protocol.Edge{From: "tenebrae", To: "homelab", Kind: "depends", Rule: "ask"},
	)
	return g
}

func TestMapCardShowsWildcardAsAllProjectsAndRuleOnlyWhenNotNormal(t *testing.T) {
	m := openMap(t, mapModel(120, 34, richGraph()))
	m.mp.selected = "tenebrae"
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "all projects ──▶ depends") {
		t.Fatal(v)
	}
	if strings.Contains(v, "normal") || strings.Contains(v, " * ") {
		t.Fatalf("rule normal or the raw wildcard is shown:\n%s", v)
	}
	if !strings.Contains(v, "homelab ──▶ deploys auto") || !strings.Contains(v, "──▶ homelab depends ask") {
		t.Fatal(v)
	}
}

func TestMapCardGovernedByAppearsOnlyWithAnArchitect(t *testing.T) {
	m := openMap(t, mapModel(120, 34, richGraph()))
	m.mp.selected = "tenebrae"
	if v := ansi.Strip(m.View()); !strings.Contains(v, "governed by: wbg-architect (advisor)") {
		t.Fatal(v)
	}
	m.mp.selected = "homelab"
	v := ansi.Strip(m.View())
	if strings.Contains(v, "governed by") || strings.Contains(strings.ToLower(v), "add architect") {
		t.Fatal(v)
	}
}

func TestMapCardCharterShowsAllNineFieldsWrappedAndNeverCut(t *testing.T) {
	m := openMap(t, mapModel(120, 40, richGraph()))
	m.mp.selected = "tenebrae"
	v := ansi.Strip(m.View())
	for _, want := range []string{"purpose", "maturity", "accepts", "refuses", "feature_requests", "consumers", "escalate", "needs", "notes", "for the trading desk", "working.", "embeddings."} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q:\n%s", want, v)
		}
	}
}

func TestMapCardNeverWiderThanTheScreenAndKeepsRecentLast(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}, {60, 20}, {100, 16}} {
		m := openMap(t, mapModel(size[0], size[1], richGraph()))
		for _, name := range []string{"tenebrae", "cogforge", "homelab", "trading-architect"} {
			m.mp.selected = name
			out := m.mapFocusWithList(size[0], size[1]-6)
			if len(out) != size[1]-6 {
				t.Fatalf("%v %s: %d rows, want %d", size, name, len(out), size[1]-6)
			}
			for _, l := range out {
				if ansi.StringWidth(l) > size[0] {
					t.Errorf("%v %s: line too wide: %q", size, name, ansi.Strip(l))
				}
			}
			if last := ansi.Strip(out[len(out)-1]); !strings.Contains(last, "RECENT") {
				t.Errorf("%v %s: last row %q", size, name, last)
			}
		}
	}
}

func TestMapCardCharterScrolls(t *testing.T) {
	m := openMap(t, mapModel(80, 24, richGraph()))
	m.mp.selected = "tenebrae"
	before := ansi.Strip(m.View())
	if !strings.Contains(before, "[ ] scroll") || strings.Contains(before, "embeddings.") {
		t.Fatal(before)
	}
	for i := 0; i < 8; i++ {
		m = keys(m, "]")
	}
	if after := ansi.Strip(m.View()); !strings.Contains(after, "embeddings.") {
		t.Fatal(after)
	}
	// Another node starts at the top again.
	m.mp.selected = "cogforge"
	if v := ansi.Strip(m.View()); !strings.Contains(v, "purpose") {
		t.Fatal(v)
	}
}

func TestMapListMarksProjectsWithLiveSessions(t *testing.T) {
	m := openMap(t, mapModel(120, 34, richGraph()))
	var shop string
	for _, l := range lines(ansi.Strip(m.View())) {
		if left, _, ok := strings.Cut(l, "│"); ok && strings.Contains(left, " shop") {
			shop = left
		}
	}
	if !strings.Contains(shop, "●") {
		t.Fatalf("shop has a live session but no dot: %q", shop)
	}
}

func TestGoldenMapCard(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m := openMap(t, mapModel(size[0], size[1], richGraph()))
		m.mp.selected = "tenebrae"
		golden(t, fmt.Sprintf("map_card_%dx%d", size[0], size[1]), m.View())
	}
}

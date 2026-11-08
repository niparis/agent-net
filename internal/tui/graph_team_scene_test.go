package tui

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"

	"github.com/niparis/agent-net/internal/protocol"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"

	"github.com/niparis/agent-net/internal/tui/graphview"
)

func TestTeamOverviewSchedulesPixelFrame(t *testing.T) {
	m := mapModel(130, 38, declutterFixture())
	m.applyMap(mapMsg{view: declutterFixture(), at: t0})
	m.tab = graphTab
	m.mp.showDormant = true
	m.applyTeamView(true)
	m.graphPixel.graphics = true
	m.graphPixel.profile = colorprofile.TrueColor
	m.graphPixel.cellW, m.graphPixel.cellH = 10, 20
	scene := m.graphScene(m.graphState, graphview.Size{Width: 126, Height: 30})
	if len(scene.Groups) == 0 {
		t.Fatal("fixture has no team groups")
	}
	if cmd := m.scheduleGraphRender(); cmd == nil {
		t.Fatal("team overview must schedule the pixel renderer")
	}
	text := strings.Join((graphview.Braille{}).Render(scene, graphview.Size{Width: 126, Height: 30}).Lines, "\n")
	if strings.Contains(text, "★ builder") && strings.Contains(text, "  api") {
		t.Fatal("overview is still a list of members")
	}
}

func mockTeamsModel(w, h int) Model {
	ref := func(kind, id string) *protocol.GraphRef { return &protocol.GraphRef{Kind: kind, ID: id} }
	v := protocol.GraphView{Nodes: []protocol.Node{
		{Name: "forge", Ref: ref("team", "forge"), Lead: ref("agent", "strategist")},
		{Name: "ops", Ref: ref("team", "ops"), Lead: ref("repo", "server")},
		{Name: "media", Ref: ref("team", "media"), Lead: ref("repo", "press")},
		{Name: "strategist", Ref: ref("agent", "strategist"), Charter: protocol.Charter{Purpose: "Strategy research", Maturity: "growing", Accepts: "studies", Refuses: "live trades"}, Settings: map[string]string{protocol.SettingMergeAuthority: "ask"}},
		{Name: "ingest", Ref: ref("repo", "ingest")}, {Name: "finance", Ref: ref("repo", "finance")}, {Name: "server", Ref: ref("repo", "server")}, {Name: "press", Ref: ref("repo", "press")},
	}}
	member := func(k, id, team string) {
		v.Edges = append(v.Edges, protocol.Edge{FromRef: ref(k, id), ToRef: ref("team", team), Kind: "member_of"})
	}
	member("agent", "strategist", "forge")
	member("repo", "ingest", "forge")
	member("repo", "finance", "forge")
	member("repo", "server", "ops")
	member("repo", "press", "media")
	v.Edges = append(v.Edges, protocol.Edge{FromRef: ref("agent", "strategist"), ToRef: ref("repo", "ingest"), Kind: "depends", Rule: "auto"}, protocol.Edge{FromRef: ref("repo", "server"), ToRef: ref("repo", "ingest"), Kind: "deploys"}, protocol.Edge{FromRef: ref("repo", "press"), ToRef: ref("repo", "server"), Kind: "depends"})
	m := mapModel(w, h, v)
	m.snap.Agents = []protocol.AgentView{
		{SessionID: "lead", AgentID: "strategist", Profile: protocol.Profile{Name: "strategy lead"}, Hostname: "desk", Online: true, Status: "busy", RepoLeadExplicit: true},
		{SessionID: "other", AgentID: "strategist", Profile: protocol.Profile{Name: "strategy second"}, Hostname: "desk", Online: true, Status: "idle"},
		{SessionID: "old", AgentID: "strategist", Profile: protocol.Profile{Name: "strategy old"}, Online: false, LastActive: t0.Unix()},
		{SessionID: "worker", AgentID: "strategist", Profile: protocol.Profile{Name: "worker hidden"}, Worker: &protocol.WorkerInfo{}, Online: true, Status: "busy"},
		{SessionID: "merge", Type: "merger", Profile: protocol.Profile{Name: "server merger"}, Repo: protocol.RepoInfo{Project: "server"}, Online: true, Status: "busy"},
		{SessionID: "ingest", Repo: protocol.RepoInfo{Project: "ingest"}, Online: true, Status: "idle"},
		{SessionID: "finance", Repo: protocol.RepoInfo{Project: "finance"}, Online: true, Status: "idle"},
		{SessionID: "server", Repo: protocol.RepoInfo{Project: "server"}, Online: true, Status: "idle"},
		{SessionID: "press", Repo: protocol.RepoInfo{Project: "press"}, Online: true, Status: "idle"},
	}
	m.applyMap(mapMsg{view: v, at: t0})
	m.tab = graphTab
	m.graphState, _ = graphview.Update(m.graphState, graphview.SelectNode{Name: graphRefKey(ref("team", "forge"))})
	return m
}

func TestTeamGraphNavigationAndSessions(t *testing.T) {
	m := mockTeamsModel(130, 38)
	s := m.graphScene(m.graphState, graphview.Size{Width: 130, Height: 38})
	if len(s.Frames) != 3 {
		t.Fatalf("overview frames: %d", len(s.Frames))
	}
	rendered := strings.Join((graphview.Braille{}).Render(s, graphview.Size{Width: 130, Height: 38}).Lines, "\n")
	for _, want := range []string{"★ strategist", "2 projects · 4 sessions", "⇄ merger"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "worker hidden") {
		t.Fatal("worker visible")
	}
	m = keys(m, "enter")
	if m.teamFilter != "forge" {
		t.Fatalf("enter did not focus team: %q", m.teamFilter)
	}
	m = keys(m, "f")
	if !m.mp.teamInside {
		t.Fatal("f did not zoom inside")
	}
	s = m.graphScene(m.graphState, graphview.Size{Width: 130, Height: 38})
	for _, n := range s.Nodes {
		if n.Name == graphRefKey(&protocol.GraphRef{Kind: "agent", ID: "strategist"}) && n.Card.Sessions != 2 {
			t.Fatalf("sessions = %d", n.Card.Sessions)
		}
	}
	rendered = strings.Join((graphview.Braille{}).Render(s, graphview.Size{Width: 130, Height: 38}).Lines, "\n")
	for _, want := range []string{"★ strategy lead", "strategy second", "+ 1 offline", "CHARTER", "YOUR CHOICES"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("inside missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "worker hidden") {
		t.Fatal("worker visible in card")
	}
	m.graphState, _ = graphview.Update(m.graphState, graphview.SelectNode{Name: graphRefKey(&protocol.GraphRef{Kind: "team", ID: "ops"})})
	m = keys(m, "enter")
	if m.teamFilter != "ops" || m.mp.teamInside {
		t.Fatalf("team tag did not jump: %q", m.teamFilter)
	}
	m = keys(m, "tab")
	if m.teamFilter == "ops" {
		t.Fatal("tab did not move team")
	}
}

func TestGraphFrameGoldens(t *testing.T) {
	for _, view := range []string{"teams", "focus", "inside"} {
		for _, sz := range [][2]int{{80, 24}, {120, 34}, {130, 38}} {
			size := graphview.Size{Width: sz[0], Height: sz[1]}
			m := mockTeamsModel(sz[0], sz[1])
			if view != "teams" {
				m = keys(m, "enter")
			}
			if view == "inside" {
				m = keys(m, "f")
			}
			scene := m.graphScene(m.graphState, size)
			if sz[0] != 130 {
				golden(t, fmt.Sprintf("team_graph_%s_%dx%d", view, sz[0], sz[1]), strings.Join((graphview.Braille{}).Render(scene, size).Lines, "\n"))
				continue
			}
			img := graphview.Rasterize(scene, size, graphview.PixelMetrics{CellWidth: 10, CellHeight: 20})
			// The terminal overlays labels on the bitmap. Draw them here too
			// so the PNG is useful as a review screenshot.
			labels := graphview.PixelLabels(scene, size, graphview.PixelMetrics{CellWidth: 10, CellHeight: 20})
			for pos, c := range labels {
				if c.Rune == ' ' {
					continue
				}
				d := font.Drawer{Dst: img, Src: image.NewUniform(color.RGBA{215, 220, 229, 255}), Face: basicfont.Face7x13, Dot: fixed.P(pos.X*10, pos.Y*20+15)}
				d.DrawString(string(c.Rune))
			}
			path := filepath.Join("testdata", "team_graph_"+view+"_130x38.png")
			var buf bytes.Buffer
			if err := png.Encode(&buf, img); err != nil {
				t.Fatal(err)
			}
			if *update {
				if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
					t.Fatal(err)
				}
				continue
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(want, buf.Bytes()) {
				t.Fatalf("pixel golden differs: %s", path)
			}
		}
	}
}

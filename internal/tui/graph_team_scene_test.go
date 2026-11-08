package tui

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"sort"

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
		{Name: "forge", Ref: ref("team", "forge"), Lead: ref("agent", "strategist"), AuthorityMode: "charter_first", HeldBack: "deploy to production; schema changes", Charter: protocol.Charter{Purpose: "Shared forge work", Rules: "Keep the API stable.", Escalate: "ask the team lead"}},
		{Name: "ops", Ref: ref("team", "ops"), Lead: ref("repo", "server"), AuthorityMode: "absolute"},
		{Name: "media", Ref: ref("team", "media"), Lead: ref("repo", "press"), AuthorityMode: "negotiate"},
		{Name: "strategist", Ref: ref("agent", "strategist"), Charter: protocol.Charter{Purpose: "Strategy research", Maturity: "growing", Accepts: "studies", Refuses: "live trades"}, Settings: map[string]string{protocol.SettingMergeAuthority: "ask"}, Place: &protocol.NodePlace{
			Team: "forge", TeamLead: "tenebrae-strats", Mode: "charter_first",
			Repo: "ingest", RepoLead: "research", ReportsTo: "the forge lead",
			HeldBack: []protocol.PlaceHeldBack{{Text: "deploy to production", From: "team"}, {Text: "schema changes", From: "team"}},
		}},
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
		{SessionID: "cto-session", Harness: "cto", Online: true, Status: "busy", Profile: protocol.Profile{Name: "CTO"}},
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
	m = keys(m, "f")
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
				m = keys(m, "f")
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
				drawPreviewGlyph(img, pos.X*10, pos.Y*20, c.Rune, color.RGBA{215, 220, 229, 255})
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

// drawPreviewGlyph draws one terminal cell into the preview image. The bitmap
// font only covers ASCII, so symbols are drawn as small shapes instead of
// letting the font fall back to a box or a zero.
func drawPreviewGlyph(img *image.RGBA, x, y int, r rune, col color.RGBA) {
	set := func(px, py int) {
		if image.Pt(px, py).In(img.Bounds()) {
			img.SetRGBA(px, py, col)
		}
	}
	fillCircle := func(cx, cy, radius float64) {
		for py := int(math.Floor(cy - radius)); py <= int(math.Ceil(cy+radius)); py++ {
			for px := int(math.Floor(cx - radius)); px <= int(math.Ceil(cx+radius)); px++ {
				if math.Hypot(float64(px)-cx, float64(py)-cy) <= radius {
					set(px, py)
				}
			}
		}
	}
	fillPoly := func(pts []image.Point) {
		if len(pts) < 3 {
			return
		}
		minY, maxY := pts[0].Y, pts[0].Y
		for _, p := range pts {
			minY, maxY = min(minY, p.Y), max(maxY, p.Y)
		}
		for py := minY; py <= maxY; py++ {
			var xs []int
			for i := range pts {
				a, b := pts[i], pts[(i+1)%len(pts)]
				if a.Y <= py && b.Y > py || b.Y <= py && a.Y > py {
					t := float64(py-a.Y) / float64(b.Y-a.Y)
					xs = append(xs, int(math.Round(float64(a.X)+t*float64(b.X-a.X))))
				}
			}
			sort.Ints(xs)
			for i := 0; i+1 < len(xs); i += 2 {
				for px := xs[i]; px <= xs[i+1]; px++ {
					set(px, py)
				}
			}
		}
	}
	switch r {
	case '·':
		fillCircle(float64(x)+5, float64(y)+11, 1.4)
		return
	case '…':
		for _, dx := range []int{3, 5, 7} {
			fillCircle(float64(x+dx), float64(y)+14, 1)
		}
		return
	case '★':
		pts := make([]image.Point, 0, 10)
		for i := 0; i < 10; i++ {
			a := -math.Pi/2 + float64(i)*math.Pi/5
			radius := 4.8
			if i%2 == 1 {
				radius = 2.1
			}
			pts = append(pts, image.Pt(int(math.Round(float64(x)+5+math.Cos(a)*radius)), int(math.Round(float64(y)+9+math.Sin(a)*radius))))
		}
		fillPoly(pts)
		return
	case '◆':
		fillPoly([]image.Point{{x + 5, y + 4}, {x + 9, y + 9}, {x + 5, y + 14}, {x + 1, y + 9}})
		return
	case '▸':
		fillPoly([]image.Point{{x + 3, y + 5}, {x + 8, y + 9}, {x + 3, y + 13}})
		return
	case '⇄':
		for _, yy := range []int{7, 12} {
			for px := x + 2; px <= x+8; px++ {
				set(px, y+yy)
			}
		}
		fillPoly([]image.Point{{x + 8, y + 5}, {x + 8, y + 9}, {x + 10, y + 7}})
		fillPoly([]image.Point{{x + 2, y + 10}, {x + 2, y + 14}, {x, y + 12}})
		return
	case '●':
		fillCircle(float64(x)+5, float64(y)+9, 3)
		return
	case '○':
		for i := 0; i < 360; i++ {
			a := float64(i) * math.Pi / 180
			set(int(math.Round(float64(x)+5+math.Cos(a)*3.2)), int(math.Round(float64(y)+9+math.Sin(a)*3.2)))
		}
		return
	}
	if r < 32 || r > 126 {
		r = '?'
	}
	d := font.Drawer{Dst: img, Src: image.NewUniform(col), Face: basicfont.Face7x13, Dot: fixed.P(x, y+15)}
	d.DrawString(string(r))
}

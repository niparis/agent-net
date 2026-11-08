package tui

import (
	"image"
	"image/color"
	"math"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/tui/graphview"
)

func teamSceneNode(t *testing.T, s graphview.Scene, name string) graphview.SceneNode {
	t.Helper()
	for _, n := range s.Nodes {
		if n.Name == name {
			return n
		}
	}
	t.Fatalf("node %q not in scene", name)
	return graphview.SceneNode{}
}

// A focused team places its lead in the middle and spreads members over the
// frame instead of stacking them in one corner.
func TestTeamFocusPlacementUsesFrame(t *testing.T) {
	const w, h = 120, 34
	m := mockTeamsModel(w, h)
	m = keys(m, "enter")
	s := m.graphScene(m.graphState, graphview.Size{Width: w, Height: h})
	if len(s.Frames) == 0 {
		t.Fatal("no frames")
	}
	f := s.Frames[0]
	lead := teamSceneNode(t, s, graphKeyFor("agent", "strategist"))
	lx, ly := lead.Position.X*float64(w), lead.Position.Y*float64(h)
	cx, cy := float64(f.X)+float64(f.Width)/2, float64(f.Y)+float64(f.Height)/2
	if math.Abs(lx-cx) > float64(f.Width)/6 || math.Abs(ly-cy) > float64(f.Height)/4 {
		t.Fatalf("lead at %.1f,%.1f not near frame centre %.1f,%.1f", lx, ly, cx, cy)
	}
	minX, maxX, minY, maxY := math.Inf(1), math.Inf(-1), math.Inf(1), math.Inf(-1)
	for _, n := range s.Nodes {
		if n.Kind == "team" || n.Kind == "tag" {
			continue
		}
		x, y := n.Position.X*float64(w), n.Position.Y*float64(h)
		if x < float64(f.X) || x > float64(f.X+f.Width-1) || y < float64(f.Y) || y > float64(f.Y+f.Height-1) {
			t.Fatalf("node %q outside frame: %.1f,%.1f", n.Name, x, y)
		}
		minX, maxX = math.Min(minX, x), math.Max(maxX, x)
		minY, maxY = math.Min(minY, y), math.Max(maxY, y)
	}
	if maxX-minX < float64(f.Width)/3 || maxY-minY < float64(f.Height)/3 {
		t.Fatalf("members crammed into %.1fx%.1f of a %dx%d frame", maxX-minX, maxY-minY, f.Width, f.Height)
	}
	if f.Height >= h {
		t.Fatalf("frame height %d not sized to its content", f.Height)
	}
}

func graphKeyFor(kind, id string) string {
	return graphRefKey(&protocol.GraphRef{Kind: kind, ID: id})
}

// Edges are curves through open space: every edge carries a path, and no
// interior point runs along a frame border.
func TestTeamEdgesCurveClearOfFrameBorders(t *testing.T) {
	const w, h = 120, 34
	m := mockTeamsModel(w, h)
	m = keys(m, "enter")
	s := m.graphScene(m.graphState, graphview.Size{Width: w, Height: h})
	if len(s.Edges) == 0 {
		t.Fatal("no edges")
	}
	for _, e := range s.Edges {
		if len(e.Path) < 3 {
			t.Fatalf("edge %s -> %s is not curved", e.FromName, e.ToName)
		}
		// Crossing a frame border once is how an edge enters or leaves a box.
		// Running along it shows up as several consecutive points on the same
		// border line.
		for _, f := range s.Frames {
			runX, runY := 0, 0
			for _, p := range e.Path[1 : len(e.Path)-1] {
				x, y := int(math.Round(p.X*float64(w))), int(math.Round(p.Y*float64(h)))
				onX := x == f.X || x == f.X+f.Width-1
				onY := y == f.Y || y == f.Y+f.Height-1
				if onX {
					runX++
				} else {
					runX = 0
				}
				if onY {
					runY++
				} else {
					runY = 0
				}
				if runX >= 8 || runY >= 8 {
					t.Fatalf("edge %s -> %s runs along a frame border at %d,%d", e.FromName, e.ToName, x, y)
				}
			}
		}
	}
}

// A team with an edge into the focused team gets its own small frame beside
// the main frame; a team without edges only appears in the footer.
func TestFocusNeighbourFramesBesideMainFrame(t *testing.T) {
	const w, h = 120, 34
	m := mockTeamsModel(w, h)
	m = keys(m, "enter")
	s := m.graphScene(m.graphState, graphview.Size{Width: w, Height: h})
	if len(s.Frames) != 2 {
		t.Fatalf("frames = %d, want main + one connected team", len(s.Frames))
	}
	main, side := s.Frames[0], s.Frames[1]
	if side.X >= main.X && side.X+side.Width <= main.X+main.Width {
		t.Fatalf("neighbour frame overlaps the main frame: %+v vs %+v", side, main)
	}
	var footer []string
	for _, tx := range s.Texts {
		if tx.Y >= h-2 {
			footer = append(footer, tx.Text)
		}
	}
	joined := strings.Join(footer, " ")
	if !strings.Contains(joined, "media") {
		t.Fatalf("footer missing team without edges: %q", joined)
	}
	if strings.Contains(joined, "ops") {
		t.Fatalf("footer lists a connected team: %q", joined)
	}
}

// The all-teams view draws one curved edge per team link with its count at the
// midpoint, not a bus line under the boxes.
func TestAllTeamsDrawsCurvedLinksNotBusLine(t *testing.T) {
	const w, h = 130, 38
	m := mockTeamsModel(w, h)
	s := m.graphScene(m.graphState, graphview.Size{Width: w, Height: h})
	for _, tx := range s.Texts {
		if strings.Contains(tx.Text, "────") {
			t.Fatalf("bus line still drawn: %q", tx.Text)
		}
	}
	if len(s.Edges) == 0 {
		t.Fatal("no team links")
	}
	for _, e := range s.Edges {
		if len(e.Path) < 3 {
			t.Fatalf("link %s -> %s is not curved", e.FromName, e.ToName)
		}
		mid := e.Path[len(e.Path)/2]
		found := false
		for _, tx := range s.Texts {
			if tx.Text == "1" && math.Abs(float64(tx.X)-mid.X*float64(w)) < 2 && math.Abs(float64(tx.Y)-mid.Y*float64(h)) < 2 {
				found = true
			}
		}
		if !found {
			t.Fatalf("count missing at midpoint of %s -> %s", e.FromName, e.ToName)
		}
	}
}

// Ten teams fit in a grid of three or four columns at both review sizes.
func TestTenTeamGridFits(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {130, 38}} {
		w, h := size[0], size[1]
		m := mockTenTeamsModel(w, h)
		s := m.graphScene(m.graphState, graphview.Size{Width: w, Height: h})
		if len(s.Frames) != 10 {
			t.Fatalf("%dx%d: frames = %d, want 10", w, h, len(s.Frames))
		}
		rows := map[int]int{}
		for _, f := range s.Frames {
			rows[f.Y]++
		}
		if len(rows) < 3 || len(rows) > 4 {
			t.Fatalf("%dx%d: grid has %d rows, want 3 or 4", w, h, len(rows))
		}
		for y, n := range rows {
			if n > 4 {
				t.Fatalf("%dx%d: row %d has %d boxes, want at most 4", w, h, y, n)
			}
		}
		if rows[s.Frames[0].Y] < 3 || rows[s.Frames[min(3, len(s.Frames)-1)].Y] < 3 {
			t.Fatalf("%dx%d: first rows are not 3-4 boxes wide: %v", w, h, rows)
		}
		// No frame may overlap another or the bottom footer row.
		for i, a := range s.Frames {
			if a.Y+a.Height >= h {
				t.Fatalf("%dx%d: frame %s reaches the footer row: %+v", w, h, a.Hit, a)
			}
			for _, b := range s.Frames[i+1:] {
				if a.X < b.X+b.Width && b.X < a.X+a.Width && a.Y < b.Y+b.Height && b.Y < a.Y+a.Height {
					t.Fatalf("%dx%d: frames overlap: %+v %+v", w, h, a, b)
				}
			}
		}
	}
}

// The inside view puts a team tag on the nearest frame edge and keeps its edge
// short.
func TestInsideTagsSitOnFrameEdge(t *testing.T) {
	const w, h = 120, 34
	m := mockTeamsModel(w, h)
	m = keys(m, "enter")
	m = keys(m, "f")
	s := m.graphScene(m.graphState, graphview.Size{Width: w, Height: h})
	if len(s.Frames) == 0 {
		t.Fatal("no frame")
	}
	main := s.Frames[0]
	tags := 0
	for _, tx := range s.Texts {
		if !strings.HasSuffix(tx.Text, " ▸") {
			continue
		}
		tags++
		nearLeft := math.Abs(float64(tx.X-(main.X+1))) <= 1
		nearRight := math.Abs(float64(tx.X-(main.X+main.Width-1-ansiWidth(tx.Text)))) <= 1
		if !nearLeft && !nearRight {
			t.Fatalf("tag %q at x=%d is not on a frame edge", tx.Text, tx.X)
		}
		for _, e := range s.Edges {
			if e.ToName == "" {
				continue
			}
			if int(math.Round(e.To.X*float64(w))) != tx.X || int(math.Round(e.To.Y*float64(h))) != tx.Y {
				continue
			}
			length := 0.0
			for i := 1; i < len(e.Path); i++ {
				dx := (e.Path[i].X - e.Path[i-1].X) * float64(w)
				dy := (e.Path[i].Y - e.Path[i-1].Y) * float64(h)
				length += math.Hypot(dx, dy)
			}
			if length > 24 {
				t.Fatalf("tag edge is %.1f cells long, want short", length)
			}
		}
	}
	if tags == 0 {
		t.Fatal("no team tags")
	}
}

func ansiWidth(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x1100 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// Counts use singular wording, and small boxes are wide enough for their
// lines.
func TestTeamCountsAreSingularAndFit(t *testing.T) {
	const w, h = 130, 38
	m := mockTeamsModel(w, h)
	overview := strings.Join((graphview.Braille{}).Render(m.graphScene(m.graphState, graphview.Size{Width: w, Height: h}), graphview.Size{Width: w, Height: h}).Lines, "\n")
	for _, bad := range []string{"1 projects", "1 sessions"} {
		if strings.Contains(overview, bad) {
			t.Fatalf("overview still says %q", bad)
		}
	}
	if !strings.Contains(overview, "1 project · 1 session") {
		t.Fatalf("overview missing singular counts:\n%s", overview)
	}
	focus := mockTeamsModel(w, h)
	focus = keys(focus, "enter")
	focusText := strings.Join((graphview.Braille{}).Render(focus.graphScene(focus.graphState, graphview.Size{Width: w, Height: h}), graphview.Size{Width: w, Height: h}).Lines, "\n")
	for _, bad := range []string{"1 projects", "1 sessions"} {
		if strings.Contains(focusText, bad) {
			t.Fatalf("focus still says %q", bad)
		}
	}
	if strings.Contains(focusText, "sessio…") {
		t.Fatalf("counts truncated in a box:\n%s", focusText)
	}
}

// The preview renderer draws symbols itself instead of letting the bitmap font
// fall back to a box or zero.
func TestPreviewGlyphFallbackDrawsSymbols(t *testing.T) {
	for _, r := range []rune{'·', '★', '◆', '⇄', '▸', '…'} {
		img := image.NewRGBA(image.Rect(0, 0, 20, 20))
		drawPreviewGlyph(img, 2, 2, r, color.RGBA{255, 255, 255, 255})
		ink := 0
		for i := 3; i < len(img.Pix); i += 4 {
			if img.Pix[i] != 0 {
				ink++
			}
		}
		if ink == 0 {
			t.Fatalf("glyph %q drew nothing", r)
		}
	}
	star := image.NewRGBA(image.Rect(0, 0, 20, 20))
	zero := image.NewRGBA(image.Rect(0, 0, 20, 20))
	drawPreviewGlyph(star, 2, 2, '★', color.RGBA{255, 255, 255, 255})
	drawPreviewGlyph(zero, 2, 2, '0', color.RGBA{255, 255, 255, 255})
	if string(star.Pix) == string(zero.Pix) {
		t.Fatal("star is still drawn as zero")
	}
}

// mockTenTeamsModel builds a ten-team overview with needs-you, busy, idle and
// no-edge teams.
func mockTenTeamsModel(w, h int) Model {
	ref := func(kind, id string) *protocol.GraphRef { return &protocol.GraphRef{Kind: kind, ID: id} }
	v := protocol.GraphView{}
	names := []string{"forge", "ops", "media", "wbg", "games", "tools", "ai", "personal", "lab", "spare"}
	for i, name := range names {
		v.Nodes = append(v.Nodes,
			protocol.Node{Name: name, Ref: ref("team", name), Lead: ref("agent", name+"-lead")},
			protocol.Node{Name: name + "-lead", Ref: ref("agent", name+"-lead")},
			protocol.Node{Name: name + "-repo", Ref: ref("repo", name+"-repo")},
		)
		v.Edges = append(v.Edges,
			protocol.Edge{FromRef: ref("agent", name+"-lead"), ToRef: ref("team", name), Kind: "member_of"},
			protocol.Edge{FromRef: ref("repo", name+"-repo"), ToRef: ref("team", name), Kind: "member_of"},
		)
		if i < 6 {
			next := names[(i+1)%6]
			v.Edges = append(v.Edges, protocol.Edge{FromRef: ref("repo", name+"-repo"), ToRef: ref("repo", next+"-repo"), Kind: "depends"})
		}
	}
	m := mapModel(w, h, v)
	m.snap.Agents = nil
	for i, name := range names {
		status := "idle"
		switch {
		case i == 0:
			status = "needs you"
		case i < 4:
			status = "busy"
		}
		m.snap.Agents = append(m.snap.Agents, protocol.AgentView{SessionID: name, AgentID: name + "-lead", Profile: protocol.Profile{Name: name + " lead"}, Hostname: "desk", Online: true, Status: status})
	}
	m.applyMap(mapMsg{view: v, at: t0})
	m.tab = graphTab
	m.graphState, _ = graphview.Update(m.graphState, graphview.SelectNode{Name: graphRefKey(ref("team", "forge"))})
	return m
}

// Pixel polish: nodes own a left column, labels keep a clear cell from the
// ring, side boxes keep their lead inside, focus is centred, and adjacent
// boxes leave room for a link.
func TestTeamPolishSpacing(t *testing.T) {
	const w, h = 130, 38
	overview := mockTeamsModel(w, h)
	s := overview.graphScene(overview.graphState, graphview.Size{Width: w, Height: h})
	for _, f := range s.Frames {
		for _, tx := range s.Texts {
			if tx.Hit == f.Hit && tx.Y == f.Y+1 && tx.X < f.X+6 {
				t.Fatalf("team %s text starts at x=%d, want its own column at x>=%d", f.Hit, tx.X, f.X+6)
			}
		}
		for _, n := range s.Nodes {
			if n.Name != f.Hit {
				continue
			}
			nx := int(math.Round(n.Position.X * float64(w)))
			if nx > f.X+4 {
				t.Fatalf("team %s node at x=%d is over the text column", f.Hit, nx)
			}
		}
	}

	// A three-team row at 120 leaves a wide gap for a link and its count.
	narrow := mockTeamsModel(120, 34)
	sn := narrow.graphScene(narrow.graphState, graphview.Size{Width: 120, Height: 34})
	if len(sn.Frames) < 2 {
		t.Fatal("no frames")
	}
	if gap := sn.Frames[1].X - (sn.Frames[0].X + sn.Frames[0].Width); gap < 5 {
		t.Fatalf("gap between boxes = %d, want at least 5", gap)
	}

	// Focus centres the frame and keeps the side box lead inside the box.
	focus := mockTeamsModel(w, h)
	focus = keys(focus, "enter")
	sf := focus.graphScene(focus.graphState, graphview.Size{Width: w, Height: h})
	main := sf.Frames[0]
	if main.Y == 0 || main.Y+main.Height >= h {
		t.Fatalf("focus frame y=%d height=%d is not centred", main.Y, main.Height)
	}
	side := sf.Frames[1]
	for _, n := range sf.Nodes {
		if n.Name != side.Hit {
			continue
		}
		nx := int(math.Round(n.Position.X * float64(w)))
		ny := int(math.Round(n.Position.Y * float64(h)))
		if nx < side.X+2 || nx > side.X+side.Width-4 || ny <= side.Y || ny >= side.Y+side.Height-1 {
			t.Fatalf("side box lead at %d,%d is not inside %+v", nx, ny, side)
		}
	}

	// The cell just before a node label is blank, even where an edge passes.
	lines := (graphview.Braille{}).Render(sf, graphview.Size{Width: w, Height: h}).Lines
	for _, n := range sf.Nodes {
		nx := int(math.Round(n.Position.X * float64(w)))
		ny := int(math.Round(n.Position.Y * float64(h)))
		for _, tx := range sf.Texts {
			if tx.Hit != n.Name || tx.Y != ny || tx.X <= nx {
				continue
			}
			runes := []rune(lines[ny])
			if tx.X-1 < len(runes) && runes[tx.X-1] != ' ' {
				t.Fatalf("label %q touches the ring at %d,%d: %q", tx.Text, tx.X-1, ny, string(runes[tx.X-1]))
			}
		}
	}
}

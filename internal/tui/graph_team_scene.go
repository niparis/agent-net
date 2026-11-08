package tui

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/tui/graphview"
)

type teamGraphData struct {
	key, label, lead         string
	members                  []string
	projects, sessions, busy int
	role, merger             string
}
type teamLink struct {
	a, b, kind string
	count      int
}

func (m Model) graphTeamOf(name string) string {
	if name == unassignedTeam {
		return name
	}
	if n, ok := m.mapNode(name); ok && n.Ref != nil && n.Ref.Kind == "team" {
		return name
	}
	for _, e := range m.mp.edges {
		if e.Kind == "member_of" && e.From == name {
			return e.To
		}
	}
	if n, ok := m.mapNode(name); ok && n.Ref != nil && (n.Ref.Kind == "repo" || n.Ref.Kind == "agent") {
		return unassignedTeam
	}
	return ""
}

// graphCharterValues is the one adapter for charter fields. It can switch to
// effective field values and their sources when the charter protocol changes.
func graphCharterValues(c protocol.Charter) [][2]string {
	return [][2]string{{"purpose", c.Purpose}, {"maturity", c.Maturity}, {"accepts", c.Accepts}, {"refuses", c.Refuses}, {"escalate", c.Escalate}, {"feature requests", c.FeatureRequests}, {"consumers", c.Consumers}, {"needs", c.Needs}, {"notes", c.Notes}}
}

func (m Model) graphLastingSessions(n protocol.Node) (online []protocol.GraphSession, offline int, busy int) {
	seen := map[string]bool{}
	for _, a := range m.graphSessions(n) {
		if protocol.SessionType(a.Harness, a.Worker, a.Type) != "agent" {
			continue
		}
		if merged, ok := m.mp.merged[n.Name]; ok && merged.Ref != nil && a.AgentID != "" && a.AgentID != merged.Ref.ID {
			continue
		}
		if n.Ref != nil && n.Ref.Kind == "agent" && a.AgentID != "" && a.AgentID != n.Ref.ID {
			continue
		}
		// When an identified agent has its own node, do not also put its
		// sessions on the project node merely because it works in that repo.
		if n.Ref != nil && n.Ref.Kind == "repo" && a.AgentID != "" {
			key := graphRefKey(&protocol.GraphRef{Kind: "agent", ID: a.AgentID})
			if _, separate := m.mapNode(key); separate && m.mp.aliases[key] != n.Name {
				continue
			}
		}
		if seen[a.SessionID] {
			continue
		}
		seen[a.SessionID] = true
		if !a.Online {
			offline++
			continue
		}
		status := a.Status
		if status == "" {
			status = "idle"
		}
		if status == "busy" {
			busy++
		}
		name := firstNonEmpty(a.Profile.Name, firstNonEmpty(a.Title, a.SessionID))
		if a.RepoLeadExplicit || a.TeamLead {
			name = "★ " + name
		}
		online = append(online, protocol.GraphSession{ID: a.SessionID, Name: name, Machine: a.Hostname, Status: status})
	}
	for _, s := range n.Sessions {
		if seen[s.ID] {
			continue
		}
		if a, ok := m.agentByID(s.ID); ok {
			if protocol.SessionType(a.Harness, a.Worker, a.Type) != "agent" || !a.Online {
				continue
			}
		}
		if s.Status == "offline" {
			offline++
			continue
		}
		seen[s.ID] = true
		online = append(online, s)
		if s.Status == "busy" {
			busy++
		}
	}
	sort.SliceStable(online, func(i, j int) bool { return online[i].Name < online[j].Name })
	return
}

func graphLastingRole(sessions []protocol.GraphSession) string {
	role := "offline"
	for _, s := range sessions {
		if s.Status == "needs you" {
			return "needs you"
		}
		if s.Status == "busy" {
			role = "busy"
		} else if role == "offline" {
			role = "idle"
		}
	}
	return role
}

func (m Model) graphTeamData() ([]teamGraphData, []teamLink) {
	var groups []teamGraphData
	index := map[string]int{}
	for _, n := range m.mp.nodes {
		if n.Ref == nil || n.Ref.Kind != "team" || n.Active != nil && !*n.Active {
			continue
		}
		g := teamGraphData{key: n.Name, label: m.graphName(n.Name), role: "offline"}
		ref := n.Lead
		if ref == nil {
			ref = n.Owner
		}
		if ref != nil {
			g.lead = graphRefKey(ref)
			if a, ok := m.mp.aliases[g.lead]; ok {
				g.lead = a
			}
		}
		index[g.key] = len(groups)
		groups = append(groups, g)
	}
	if len(groups) == 0 { // Typed, unassigned graphs still have a team overview.
		typed := false
		for _, n := range m.mp.nodes {
			typed = typed || n.Ref != nil
		}
		if !typed {
			return nil, nil
		}
	}
	index[unassignedTeam] = len(groups)
	groups = append(groups, teamGraphData{key: unassignedTeam, label: "unassigned", role: "offline"})
	for _, n := range m.mp.nodes {
		if n.Ref == nil || n.Ref.Kind != "repo" && n.Ref.Kind != "agent" || m.mp.dormant[n.Name] && !m.mp.showDormant {
			continue
		}
		team := m.graphTeamOf(n.Name)
		i, ok := index[team]
		if !ok {
			i = index[unassignedTeam]
		}
		g := &groups[i]
		g.members = append(g.members, n.Name)
		if n.Ref.Kind == "repo" {
			g.projects++
		}
		sessions, _, busy := m.graphLastingSessions(n)
		g.sessions += len(sessions)
		g.busy += busy
		role := graphLastingRole(sessions)
		if role == "needs you" || role == "busy" && g.role != "needs you" || role == "idle" && g.role == "offline" {
			g.role = role
		}
		for _, a := range m.snap.Agents {
			if protocol.SessionType(a.Harness, a.Worker, a.Type) == "merger" && n.Ref.Kind == "repo" && firstNonEmpty(a.Repo.Project, a.Profile.Project) == n.Ref.ID {
				state := "offline"
				if a.Online {
					state = firstNonEmpty(a.Status, "idle")
				}
				g.merger = "⇄ merger · " + state
			}
		}
		if g.merger == "" {
			for _, e := range m.mp.edges {
				if e.To == n.Name && protocol.DeliveryOwnerFromSource(e.From).Kind == protocol.OwnerMerger {
					g.merger = "⇄ merger · offline"
					break
				}
			}
		}
	}
	if len(groups[len(groups)-1].members) == 0 {
		groups = groups[:len(groups)-1]
	}
	for i := range groups {
		sort.Strings(groups[i].members)
	}
	type pair struct{ a, b, kind string }
	counts := map[pair]int{}
	for _, e := range m.mp.edges {
		if e.Kind == "member_of" || e.From == e.To || m.mp.everything[e.From] != "" || m.mp.everything[e.To] != "" {
			continue
		}
		a, b := m.graphTeamOf(e.From), m.graphTeamOf(e.To)
		if a == "" || b == "" || a == b {
			continue
		}
		if a > b {
			a, b = b, a
		}
		counts[pair{a, b, e.Kind}]++
	}
	var links []teamLink
	for p, n := range counts {
		links = append(links, teamLink{p.a, p.b, p.kind, n})
	}
	sort.Slice(links, func(i, j int) bool {
		a, b := links[i], links[j]
		if a.a != b.a {
			return a.a < b.a
		}
		if a.b != b.b {
			return a.b < b.b
		}
		return a.kind < b.kind
	})
	return groups, links
}

func (m Model) teamGraphScene(base graphview.Scene, size graphview.Size) (graphview.Scene, bool) {
	if size.Height < 8 {
		return base, false
	}
	inner := graphview.Size{Width: size.Width, Height: size.Height - 1}
	scene, ok := m.teamGraphSceneContent(base, inner)
	if !ok {
		return base, false
	}
	title := "Graph · all teams"
	if m.teamFilter != "" {
		title = "Graph · team " + m.teamFilterName()
		if m.mp.teamInside {
			title += " · inside"
		}
	}
	scene.Texts = append(scene.Texts, graphview.SceneText{X: 1, Y: 0, Text: ansi.Truncate(title, size.Width-2, "…"), Role: "selected"})
	for i := range scene.Frames {
		scene.Frames[i].Y++
	}
	for i := range scene.Groups {
		scene.Groups[i].Y++
	}
	for i := range scene.Texts {
		if i < len(scene.Texts)-1 {
			scene.Texts[i].Y++
		}
	}
	adjust := func(p graphview.Point) graphview.Point {
		p.Y = (p.Y*float64(inner.Height) + 1) / float64(size.Height)
		return p
	}
	for i := range scene.Nodes {
		scene.Nodes[i].Position = adjust(scene.Nodes[i].Position)
	}
	for i := range scene.Edges {
		e := &scene.Edges[i]
		e.From = adjust(e.From)
		e.To = adjust(e.To)
		for j := range e.Path {
			e.Path[j] = adjust(e.Path[j])
		}
	}
	return scene, true
}

func teamCount(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func teamRoleRank(role string) int {
	switch role {
	case "needs you":
		return 0
	case "busy":
		return 1
	case "idle":
		return 2
	default:
		return 3
	}
}

func teamCellPoint(x, y int, size graphview.Size) graphview.Point {
	return graphview.Point{X: float64(x) / float64(size.Width), Y: float64(y) / float64(size.Height)}
}

func teamNodePoint(x, y int, size graphview.Size) graphview.Point {
	return graphview.Point{X: (float64(x) + .5) / float64(size.Width), Y: (float64(y) + .5) / float64(size.Height)}
}

// teamQuad samples a quadratic curve in cell space.
func teamQuad(x0, y0, cx, cy, x1, y1 float64, size graphview.Size) []graphview.Point {
	out := make([]graphview.Point, 0, 25)
	for i := 0; i <= 24; i++ {
		t := float64(i) / 24
		u := 1 - t
		x := u*u*x0 + 2*u*t*cx + t*t*x1
		y := u*u*y0 + 2*u*t*cy + t*t*y1
		out = append(out, graphview.Point{X: x / float64(size.Width), Y: y / float64(size.Height)})
	}
	return out
}

// teamCurve returns a quadratic path through the open space between two cells.
// bend is the control point offset in cells, perpendicular to the chord.
func teamCurve(x0, y0, x1, y1 int, bend float64, size graphview.Size) []graphview.Point {
	ax, ay := float64(x0), float64(y0)
	bx, by := float64(x1), float64(y1)
	dx, dy := bx-ax, by-ay
	l := math.Hypot(dx, dy)
	cx, cy := (ax+bx)/2, (ay+by)/2
	if l > 0 {
		cx -= dy / l * bend
		cy += dx / l * bend
	}
	return teamQuad(ax, ay, cx, cy, bx, by, size)
}

// teamOverviewLink curves through the gutter that separates two rows, so a
// link never runs over a box or its text.
func teamOverviewLink(a, b graphview.SceneFrame, gridW, gridH, gap, stride int, size graphview.Size) []graphview.Point {
	colA, rowA := a.X/(gridW+gap), a.Y/stride
	colB, rowB := b.X/(gridW+gap), b.Y/stride
	ax, bx := a.X+a.Width/2, b.X+b.Width/2
	switch {
	case rowA == rowB && teamAbs(colA-colB) == 1:
		fx, fy, tx, ty := teamBoxPorts(a, b)
		bend := math.Min(3.5, math.Max(1.2, math.Hypot(float64(tx-fx), float64(ty-fy))*.35))
		if (a.X+b.X)%2 == 0 {
			bend = -bend
		}
		return teamCurve(fx, fy, tx, ty, bend, size)
	case rowA == rowB:
		below := true
		gy := float64(a.Y+a.Height-1) + float64(gap)/2 + 1
		if int(gy) >= size.Height {
			below = false
			gy = float64(a.Y) - float64(gap)/2 - 1
		}
		sy, ty := float64(a.Y+a.Height-1), float64(b.Y+b.Height-1)
		if !below {
			sy, ty = float64(a.Y), float64(b.Y)
		}
		return teamQuad(float64(ax), sy, float64(ax+bx)/2, gy, float64(bx), ty, size)
	case teamAbs(rowA-rowB) == 1:
		fx, fy, tx, ty := a.X+a.Width/2, a.Y+a.Height-1, b.X+b.Width/2, b.Y
		if rowB < rowA {
			fx, fy, tx, ty = a.X+a.Width/2, a.Y, b.X+b.Width/2, b.Y+b.Height-1
		}
		bend := 2.0
		if (a.X+b.X)%2 == 0 {
			bend = -bend
		}
		return teamCurve(fx, fy, tx, ty, bend, size)
	}
	way, direct := teamOverviewRoute(a, b, gridW, gridH, gap, stride, size)
	if direct {
		fx, fy, tx, ty := teamBoxPorts(a, b)
		bend := math.Min(3.5, math.Max(1.2, math.Hypot(float64(tx-fx), float64(ty-fy))*.35))
		if (a.X+b.X)%2 == 0 {
			bend = -bend
		}
		return teamCurve(fx, fy, tx, ty, bend, size)
	}
	return teamRoundedPath(way, 3, size)
}

func teamLess(a, b teamGraphData) bool {
	ra, rb := teamRoleRank(a.role), teamRoleRank(b.role)
	if ra != rb {
		return ra < rb
	}
	return a.label < b.label
}

// orderOverviewTeams keeps the selected team and its connected neighbours
// together so link curves stay in the gutters, then orders the rest by state
// and puts teams without any link last.
func orderOverviewTeams(groups []teamGraphData, selected string, links []teamLink) []teamGraphData {
	byKey := map[string]teamGraphData{}
	for _, g := range groups {
		byKey[g.key] = g
	}
	if _, ok := byKey[selected]; !ok {
		selected = groups[0].key
	}
	adj := map[string][]string{}
	hasEdge := map[string]bool{}
	for _, e := range links {
		adj[e.a] = append(adj[e.a], e.b)
		adj[e.b] = append(adj[e.b], e.a)
		hasEdge[e.a], hasEdge[e.b] = true, true
	}
	for k := range adj {
		sort.Slice(adj[k], func(i, j int) bool { return teamLess(byKey[adj[k][i]], byKey[adj[k][j]]) })
	}
	seen := map[string]bool{}
	var order []string
	var visit func(string)
	visit = func(key string) {
		if seen[key] {
			return
		}
		seen[key] = true
		order = append(order, key)
		for _, n := range adj[key] {
			visit(n)
		}
	}
	visit(selected)
	var withEdges, without []string
	for _, g := range groups {
		if seen[g.key] {
			continue
		}
		if hasEdge[g.key] {
			withEdges = append(withEdges, g.key)
		} else {
			without = append(without, g.key)
		}
	}
	sortKeys := func(keys []string) {
		sort.Slice(keys, func(i, j int) bool { return teamLess(byKey[keys[i]], byKey[keys[j]]) })
	}
	sortKeys(withEdges)
	sortKeys(without)
	order = append(order, withEdges...)
	order = append(order, without...)
	out := make([]teamGraphData, 0, len(groups))
	for _, k := range order {
		out = append(out, byKey[k])
	}
	return out
}

func teamBoxPorts(a, b graphview.SceneFrame) (int, int, int, int) {
	if a.Y == b.Y {
		if a.X <= b.X {
			return a.X + a.Width - 1, a.Y + a.Height/2, b.X, b.Y + b.Height/2
		}
		return a.X, a.Y + a.Height/2, b.X + b.Width - 1, b.Y + b.Height/2
	}
	if a.Y < b.Y {
		return a.X + a.Width/2, a.Y + a.Height - 1, b.X + b.Width/2, b.Y
	}
	return a.X + a.Width/2, a.Y, b.X + b.Width/2, b.Y + b.Height - 1
}

// teamRoundedPath samples a polyline with rounded corners in cell space.
func teamRoundedPath(pts [][2]int, radius float64, size graphview.Size) []graphview.Point {
	if len(pts) < 2 {
		return nil
	}
	out := make([]graphview.Point, 0, len(pts)*4)
	add := func(x, y float64) {
		out = append(out, graphview.Point{X: x / float64(size.Width), Y: y / float64(size.Height)})
	}
	add(float64(pts[0][0]), float64(pts[0][1]))
	for i := 1; i < len(pts)-1; i++ {
		prev, cur, next := pts[i-1], pts[i], pts[i+1]
		v1x, v1y := float64(cur[0]-prev[0]), float64(cur[1]-prev[1])
		l1 := math.Hypot(v1x, v1y)
		v2x, v2y := float64(next[0]-cur[0]), float64(next[1]-cur[1])
		l2 := math.Hypot(v2x, v2y)
		if l1 == 0 || l2 == 0 {
			continue
		}
		r := math.Min(radius, math.Min(l1, l2)/2)
		ax, ay := float64(cur[0])-v1x/l1*r, float64(cur[1])-v1y/l1*r
		bx, by := float64(cur[0])+v2x/l2*r, float64(cur[1])+v2y/l2*r
		add(ax, ay)
		for t := 0.2; t <= 1.0001; t += 0.2 {
			u := 1 - t
			add(u*u*ax+2*u*t*float64(cur[0])+t*t*bx, u*u*ay+2*u*t*float64(cur[1])+t*t*by)
		}
	}
	add(float64(pts[len(pts)-1][0]), float64(pts[len(pts)-1][1]))
	return out
}

// teamOverviewRoute keeps a link between two boxes in the grid gutters. The
// direct flag is set when the boxes touch, where a short curve is enough.
func teamOverviewRoute(a, b graphview.SceneFrame, gridW, gridH, gap, stride int, size graphview.Size) (way [][2]int, direct bool) {
	colA, rowA := a.X/(gridW+gap), a.Y/stride
	colB, rowB := b.X/(gridW+gap), b.Y/stride
	if rowA == rowB {
		if teamAbs(colA-colB) == 1 {
			return nil, true
		}
		gy := rowA*stride + gridH + gap/2
		above := false
		if gy >= size.Height {
			gy = rowA*stride - gap/2
			above = true
		}
		if gy < 0 || gy >= size.Height {
			return nil, true
		}
		sx, tx := a.X+a.Width/2, b.X+b.Width/2
		if above {
			return [][2]int{{sx, a.Y}, {sx, gy}, {tx, gy}, {tx, b.Y}}, false
		}
		return [][2]int{{sx, a.Y + a.Height - 1}, {sx, gy}, {tx, gy}, {tx, b.Y + b.Height - 1}}, false
	}
	if colA == colB && teamAbs(rowA-rowB) == 1 {
		return nil, true
	}
	dir := 1
	if rowB < rowA {
		dir = -1
	}
	gyA := rowA*stride + gridH + gap/2
	gyB := rowB*stride - gap/2
	if dir < 0 {
		gyA = rowA*stride - gap/2
		gyB = rowB*stride + gridH + gap/2
	}
	if gyA < 0 || gyA >= size.Height || gyB < 0 || gyB >= size.Height {
		return nil, true
	}
	gx := colA*(gridW+gap) + gridW + gap/2
	if colB < colA {
		gx = colA*(gridW+gap) - gap/2
	}
	if colB == colA {
		if gx >= size.Width {
			gx = colA*(gridW+gap) - gap/2
		}
	}
	if gx < 0 || gx >= size.Width {
		return nil, true
	}
	sx, tx := a.X+a.Width/2, b.X+b.Width/2
	if dir > 0 {
		return [][2]int{{sx, a.Y + a.Height - 1}, {sx, gyA}, {gx, gyA}, {gx, gyB}, {tx, gyB}, {tx, b.Y}}, false
	}
	return [][2]int{{sx, a.Y}, {sx, gyA}, {gx, gyA}, {gx, gyB}, {tx, gyB}, {tx, b.Y + b.Height - 1}}, false
}

func teamPathMidpoint(path []graphview.Point, size graphview.Size) (int, int) {
	if len(path) == 0 {
		return 0, 0
	}
	total := 0.0
	for i := 1; i < len(path); i++ {
		total += math.Hypot((path[i].X-path[i-1].X)*float64(size.Width), (path[i].Y-path[i-1].Y)*float64(size.Height))
	}
	half := total / 2
	for i := 1; i < len(path); i++ {
		seg := math.Hypot((path[i].X-path[i-1].X)*float64(size.Width), (path[i].Y-path[i-1].Y)*float64(size.Height))
		if half <= seg && seg > 0 {
			t := half / seg
			return int(math.Round((path[i-1].X + (path[i].X-path[i-1].X)*t) * float64(size.Width))),
				int(math.Round((path[i-1].Y + (path[i].Y-path[i-1].Y)*t) * float64(size.Height)))
		}
		half -= seg
	}
	last := path[len(path)-1]
	return int(math.Round(last.X * float64(size.Width))), int(math.Round(last.Y * float64(size.Height)))
}

func teamAbs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func (m Model) teamGraphSceneContent(base graphview.Scene, size graphview.Size) (graphview.Scene, bool) {
	groups, links := m.graphTeamData()
	if len(groups) == 0 || m.mp.focusNode != "" || size.Width < 30 || size.Height < 7 {
		return base, false
	}
	scene := graphview.Scene{TeamCanvas: true, ZoomRatio: 1, Level: "labels", Feed: base.Feed, Hints: base.Hints}
	selected := m.graphState.Graph.Selected()
	selectedTeam := m.graphTeamOf(selected)
	byKey := map[string]teamGraphData{}
	for _, g := range groups {
		byKey[g.key] = g
	}
	text := func(x, y, w int, s, role, hit string) {
		if w > 0 && y >= 0 && y < size.Height {
			scene.Texts = append(scene.Texts, graphview.SceneText{X: x, Y: y, Text: ansi.Truncate(s, w, "…"), Role: role, Hit: hit})
		}
	}
	frame := func(x, y, w, h int, g teamGraphData, strong bool) {
		scene.Groups = append(scene.Groups, graphview.Group{Name: g.key, Label: g.label, Members: g.members, Lead: g.lead, X: x, Y: y, Width: w, Height: h})
		scene.Frames = append(scene.Frames, graphview.SceneFrame{X: x, Y: y, Width: w, Height: h, Role: g.role, Hit: g.key, Selected: strong})
		text(x+2, y, w-4, " "+g.label+" ", "selected", g.key)
	}
	duplicates := map[string]int{}
	for _, n := range m.mp.nodes {
		if n.Ref != nil && (n.Ref.Kind == "repo" || n.Ref.Kind == "agent") {
			duplicates[strings.ToLower(m.graphName(n.Name))]++
		}
	}
	node := func(key string, x, y int, strong bool) graphview.SceneNode {
		n, _ := m.mapNode(key)
		sessions, _, busy := m.graphLastingSessions(n)
		label, suffix, kind := m.graphName(key), "", ""
		if n.Ref != nil {
			kind = n.Ref.Kind
			if (kind == "repo" || kind == "agent") && duplicates[strings.ToLower(label)] > 1 {
				suffix = " · " + kind
				label += suffix
			}
		}
		return graphview.SceneNode{Name: key, Label: label, LabelSuffix: suffix, Kind: kind, Position: teamNodePoint(x, y, size), Role: graphLastingRole(sessions), Selected: strong, HasCharter: n.Charter != (protocol.Charter{}), BusySessions: busy, Card: graphview.SceneCard{Sessions: len(sessions), Maturity: n.Charter.Maturity}}
	}
	// small draws a neighbour team box and returns the cell of its lead node.
	// nodeRight puts the lead against the right border, so an edge to a frame
	// on its right leaves without crossing the box text.
	small := func(g teamGraphData, x, y, w, h int, strong, nodeRight bool) (int, int) {
		frame(x, y, w, h, g, strong)
		nx, ny := x+2, y+3
		if h <= 4 {
			ny = y + 1
		}
		if nodeRight {
			nx = x + w - 3
		}
		n := node(g.lead, nx, ny, strong)
		n.Name, n.Label, n.Kind = g.key, g.label, "team"
		if g.lead == "" {
			n.Role = "offline"
			n.Card.Sessions = 0
		}
		scene.Nodes = append(scene.Nodes, n)
		lead := "no lead"
		if g.lead != "" {
			lead = "★ " + m.graphName(g.lead)
		}
		textW := w - 4
		if nodeRight {
			textW = w - 7
		}
		leadX := x + 3
		if h <= 4 {
			leadX = x + 4
			textW = w - 5
			if nodeRight {
				textW = w - 7
			}
		}
		text(leadX, y+1, textW, lead, "label", g.key)
		if h >= 4 {
			text(x+3, y+2, textW, teamCount(g.projects, "project", "projects")+" · "+teamCount(g.sessions, "session", "sessions"), "labelDim", g.key)
		}
		if h >= 5 {
			state := g.role
			if g.busy > 0 && state != "needs you" {
				state = fmt.Sprintf("%d busy", g.busy)
			}
			sx := x + 4
			if strong {
				sx = x + 5
			}
			text(sx, y+3, w-(sx-x)-1, state, g.role, g.key)
		}
		if h > 5 && g.merger != "" {
			text(x+3, y+4, textW, g.merger, "busy", g.key)
		}
		return nx, ny
	}
	if m.teamFilter == "" {
		ordered := orderOverviewTeams(groups, selectedTeam, links)
		cols := 4
		if size.Width < 100 {
			cols = 3
		}
		if len(ordered) < cols {
			cols = len(ordered)
		}
		gap := 3
		if size.Width < 100 {
			gap = 2
		} else if size.Width >= 126 {
			gap = 4
		}
		w := (size.Width - (cols-1)*gap) / cols
		rows := (len(ordered) + cols - 1) / cols
		avail := size.Height - 1 // keep the last row for the footer chip
		h := 6
		stride := h + 1
		if rows > 1 {
			if spread := (avail - h) / (rows - 1); spread > stride {
				stride = spread
			}
		}
		for h > 3 && rows > 1 && (rows-1)*stride+h > avail {
			h--
			stride = h + 1
			if spread := (avail - h) / (rows - 1); spread > stride {
				stride = spread
			}
		}
		boxes := map[string]graphview.SceneFrame{}
		visible := 0
		for i, g := range ordered {
			row := i / cols
			col := i % cols
			if row%2 == 1 && min(cols, len(ordered)-row*cols) == cols {
				col = cols - 1 - col
			}
			x := col * (w + gap)
			y := row * stride
			if y+h > avail {
				break
			}
			small(g, x, y, w, h, g.key == selectedTeam, false)
			boxes[g.key] = scene.Frames[len(scene.Frames)-1]
			visible++
		}
		for _, e := range links {
			a, ok := boxes[e.a]
			b, ok2 := boxes[e.b]
			if !ok || !ok2 {
				continue
			}
			path := teamOverviewLink(a, b, w, h, gap, stride, size)
			if len(path) < 2 {
				continue
			}
			scene.Edges = append(scene.Edges, graphview.SceneEdge{FromName: e.a, ToName: e.b, From: path[0], To: path[len(path)-1], Path: path, Kind: e.kind})
			mx, my := teamPathMidpoint(path, size)
			text(mx, max(0, my-1), 4, fmt.Sprint(e.count), e.kind, "")
		}
		if visible < len(ordered) {
			x := 1
			text(x, size.Height-1, 11, "more teams", "labelDim", "")
			x += 12
			for _, g := range ordered[visible:] {
				chip := g.label + " "
				text(x, size.Height-1, size.Width-x, chip, "label", g.key)
				x += ansi.StringWidth(chip) + 1
				if x >= size.Width-5 {
					break
				}
			}
		}
		if len(m.mp.dormant) > 0 {
			text(1, size.Height-1, size.Width-2, m.graphName(dormantChip), "offline", dormantChip)
		}
		return scene, true
	}
	teamKey := graphRefKey(&protocol.GraphRef{Kind: "team", ID: m.teamFilter})
	if m.teamFilter == unassignedTeam {
		teamKey = unassignedTeam
	}
	g, ok := byKey[teamKey]
	if !ok {
		return base, false
	}
	var neighbours []teamGraphData
	neighbourSet := map[string]bool{}
	for _, e := range links {
		if e.a == teamKey && !neighbourSet[e.b] {
			if ng, ok := byKey[e.b]; ok {
				neighbours = append(neighbours, ng)
				neighbourSet[e.b] = true
			}
		}
		if e.b == teamKey && !neighbourSet[e.a] {
			if ng, ok := byKey[e.a]; ok {
				neighbours = append(neighbours, ng)
				neighbourSet[e.a] = true
			}
		}
	}
	sort.Slice(neighbours, func(i, j int) bool { return neighbours[i].label < neighbours[j].label })
	inside := m.mp.teamInside
	var rest []teamGraphData
	for _, ng := range groups {
		if ng.key != g.key && !neighbourSet[ng.key] {
			rest = append(rest, ng)
		}
	}
	footer := !inside && len(rest) > 0
	mainH := size.Height
	if footer {
		mainH = size.Height - 1
	}
	mainX, mainW := 0, size.Width
	cardX := size.Width
	if inside && size.Width >= 75 {
		cardX = size.Width * 62 / 100
		mainW = cardX - 1
	} else if !inside && size.Width >= 90 && len(neighbours) > 0 {
		mainW = size.Width * 55 / 100
		mainX = (size.Width - mainW) / 2
		if len(neighbours) == 1 {
			sideW := max(16, size.Width/4)
			mainX = sideW + 1
			mainW = size.Width - mainX
		}
	}
	if !inside {
		want := 8 + len(g.members)*4
		if want < 12 {
			want = 12
		}
		if want < mainH {
			mainH = want
		}
	}
	frame(mainX, 0, mainW, mainH, g, true)
	header := fmt.Sprintf("no lead · %s · %s", teamCount(g.projects, "project", "projects"), teamCount(g.sessions, "session", "sessions"))
	if g.lead != "" {
		header = fmt.Sprintf("lead %s · %s · %s", m.graphName(g.lead), teamCount(g.projects, "project", "projects"), teamCount(g.sessions, "session", "sessions"))
	}
	text(mainX+2, 1, mainW-4, header, "labelDim", "")
	cells := map[string][2]int{}
	positions := map[string]graphview.Point{}
	nodes := map[string]graphview.SceneNode{}
	addNode := func(key string, x, y int, strong bool) {
		n := node(key, x, y, strong)
		cells[key] = [2]int{x, y}
		positions[key] = teamNodePoint(x, y, size)
		nodes[key] = n
		scene.Nodes = append(scene.Nodes, n)
	}
	cx, cy := mainX+mainW/2, mainH/2
	if cy < 3 {
		cy = 3
	}
	if g.lead != "" {
		if _, ok := m.mapNode(g.lead); ok {
			addNode(g.lead, cx, cy, true)
		}
	}
	var others []string
	for _, key := range g.members {
		if key != g.lead {
			others = append(others, key)
		}
	}
	rx := max(5, mainW/2-9)
	ry := max(3, mainH/2-4)
	for i, key := range others {
		ang := -math.Pi/2 + 2*math.Pi*float64(i)/float64(max(1, len(others)))
		switch len(others) {
		case 1:
			ang = math.Pi
		case 2:
			ang = 3 * math.Pi / 4
			if i == 1 {
				ang = -math.Pi / 4
			}
		}
		x := cx + int(math.Round(math.Cos(ang)*float64(rx)))
		y := cy + int(math.Round(math.Sin(ang)*float64(ry)))
		addNode(key, clampInt(x, mainX+3, mainX+mainW-4), clampInt(y, 3, mainH-3), key == selected)
	}
	ix0, ix1 := mainX+1, mainX+mainW-2
	iy0, iy1 := 2, mainH-2
	labelTaken := map[[2]int]bool{}
	for _, c := range cells {
		labelTaken[c] = true
	}
	place := func(key, s, role string, x, y int) bool {
		w := ansi.StringWidth(s)
		if w == 0 || x < ix0 || x+w-1 > ix1 || y < iy0 || y > iy1 {
			return false
		}
		for i := 0; i < w; i++ {
			if labelTaken[[2]int{x + i, y}] {
				return false
			}
		}
		for i := 0; i < w; i++ {
			labelTaken[[2]int{x + i, y}] = true
		}
		text(x, y, w, s, role, key)
		return true
	}
	orderedMembers := make([]string, 0, len(cells))
	if _, ok := cells[g.lead]; ok {
		orderedMembers = append(orderedMembers, g.lead)
	}
	for _, key := range others {
		if _, ok := cells[key]; ok {
			orderedMembers = append(orderedMembers, key)
		}
	}
	for _, key := range orderedMembers {
		c := cells[key]
		n := nodes[key]
		label := n.Label
		if key == g.lead {
			label = "★ " + label
		}
		role := n.Role
		if role == "" || role == "none" {
			role = "label"
		}
		detail := teamCount(n.Card.Sessions, "session", "sessions")
		if n.Card.Sessions == 0 {
			detail = "offline"
		}
		if n.BusySessions > 0 {
			detail += fmt.Sprintf(" · %d busy", n.BusySessions)
		}
		suffix := n.LabelSuffix
		prefix := label
		if suffix != "" {
			prefix = strings.TrimSuffix(label, suffix)
		}
		labelW := ansi.StringWidth(label)
		placeLabel := func(x, y int) bool {
			if x < ix0 || x+labelW-1 > ix1 || y < iy0 || y > iy1 {
				return false
			}
			for i := 0; i < labelW; i++ {
				if labelTaken[[2]int{x + i, y}] {
					return false
				}
			}
			for i := 0; i < labelW; i++ {
				labelTaken[[2]int{x + i, y}] = true
			}
			pw := ansi.StringWidth(prefix)
			text(x, y, pw, prefix, role, key)
			if suffix != "" {
				text(x+pw, y, ansi.StringWidth(suffix), suffix, "labelDim", key)
			}
			return true
		}
		gap := 2
		if n.Selected {
			gap = 3
		}
		placed := false
		for _, pos := range [][2]int{
			{c[0] + gap, c[1]},
			{c[0] - gap + 1 - labelW, c[1]},
			{c[0] - labelW/2, c[1] - 1},
			{c[0] - labelW/2, c[1] + 1},
		} {
			if placeLabel(pos[0], pos[1]) {
				placed = true
				if pos[1] == c[1] {
					gapX := pos[0] - 1
					if pos[0] < c[0] {
						gapX = pos[0] + labelW
					}
					if !(n.Selected && (gapX == c[0]-1 || gapX == c[0]+1)) {
						text(gapX, pos[1], 1, " ", "label", key)
					}
				}
				if !place(key, detail, "labelDim", pos[0], pos[1]+1) {
					place(key, detail, "labelDim", pos[0], pos[1]-1)
				}
				break
			}
		}
		if !placed {
			continue
		}
	}
	if !inside && mainX > 0 {
		rows := (len(neighbours) + 1) / 2
		boxH := 6
		if rows > 0 && rows*7 > mainH {
			boxH = max(3, mainH/rows-1)
		}
		leftW := mainX - 1
		rightX := mainX + mainW + 1
		rightW := size.Width - rightX
		for i, ng := range neighbours {
			x, w := 0, leftW
			if i%2 == 1 {
				x, w = rightX, rightW
			}
			y := (i / 2) * (boxH + 1)
			if w < 12 || y+boxH > mainH {
				continue
			}
			nx, ny := small(ng, x, y, w, boxH, false, i%2 == 0)
			cells[ng.key] = [2]int{nx, ny}
			positions[ng.key] = teamNodePoint(nx, ny, size)
		}
	}
	if inside {
		for _, ng := range neighbours {
			target := ""
			for _, e := range m.mp.edges {
				if e.Kind == "member_of" {
					continue
				}
				if m.graphTeamOf(e.From) == teamKey && m.graphTeamOf(e.To) == ng.key {
					target = e.From
					break
				}
				if m.graphTeamOf(e.To) == teamKey && m.graphTeamOf(e.From) == ng.key {
					target = e.To
					break
				}
			}
			c, ok := cells[target]
			if !ok {
				c = [2]int{cx, cy}
			}
			tag := ng.label + " ▸"
			tw := ansi.StringWidth(tag)
			tx := mainX + 1
			if c[0] > mainX+mainW/2 {
				tx = mainX + mainW - 1 - tw
			}
			ty := clampInt(c[1], iy0, iy1)
			for ty <= iy1 && labelTaken[[2]int{tx, ty}] {
				ty++
			}
			if ty > iy1 {
				ty = clampInt(c[1], iy0, iy1)
			}
			labelTaken[[2]int{tx, ty}] = true
			text(tx, ty, tw, tag, "selected", ng.key)
			cells[ng.key] = [2]int{tx, ty}
			positions[ng.key] = teamNodePoint(tx, ty, size)
			scene.Nodes = append(scene.Nodes, graphview.SceneNode{Name: ng.key, Kind: "tag", Position: positions[ng.key], Role: "offline", Selected: selected == ng.key})
		}
	}
	for _, e := range m.mp.edges {
		if e.Kind == "member_of" {
			continue
		}
		a, aok := cells[e.From]
		b, bok := cells[e.To]
		if !aok && bok {
			a, aok = cells[m.graphTeamOf(e.From)]
		}
		if !bok && aok {
			b, bok = cells[m.graphTeamOf(e.To)]
		}
		if !aok || !bok || a == b {
			continue
		}
		chord := math.Hypot(float64(b[0]-a[0]), float64(b[1]-a[1]))
		bend := math.Min(4, math.Max(1.5, chord*.25))
		path := teamCurve(a[0], a[1], b[0], b[1], bend, size)
		scene.Edges = append(scene.Edges, graphview.SceneEdge{FromName: e.From, ToName: e.To, From: path[0], To: path[len(path)-1], Path: path, Kind: e.Kind, Mode: e.Mode, Rule: e.Rule, Selected: e.From == selected || e.To == selected})
	}
	if footer {
		x := 1
		text(x, size.Height-1, 11, "more teams", "labelDim", "")
		x += 12
		for _, ng := range rest {
			chip := ng.label + " "
			text(x, size.Height-1, size.Width-x, chip, "label", ng.key)
			x += ansi.StringWidth(chip) + 1
			if x >= size.Width-5 {
				break
			}
		}
	}
	if inside && cardX < size.Width {
		m.graphTeamCard(&scene, selected, cardX, 0, size.Width-cardX, size.Height, text)
	}
	return scene, true
}

func (m Model) graphTeamCard(scene *graphview.Scene, selected string, x, y, w, h int, text func(int, int, int, string, string, string)) {
	n, ok := m.mapNode(selected)
	if !ok || n.Ref != nil && n.Ref.Kind == "team" {
		return
	}
	scene.Frames = append(scene.Frames, graphview.SceneFrame{X: x, Y: y, Width: w, Height: h, Role: m.graphLiveRole(n), Card: true})
	type line struct{ text, role string }
	var lines []line
	add := func(s, role string) { lines = append(lines, line{sanitize(s), role}) }
	add(m.graphName(selected), "selected")
	add(m.graphLiveRole(n), m.graphLiveRole(n))
	add("", "label")
	add("SESSIONS", "selected")
	sessions, offline, _ := m.graphLastingSessions(n)
	for _, s := range sessions {
		add(fmt.Sprintf("%s  %s  %s", firstNonEmpty(s.Name, s.ID), s.Machine, s.Status), "label")
	}
	if len(sessions) == 0 {
		add("no online sessions", "labelDim")
	}
	if offline > 0 {
		add(fmt.Sprintf("+ %d offline", offline), "labelDim")
	}
	if n.Ref != nil && n.Ref.Kind == "repo" {
		for _, a := range m.snap.Agents {
			if protocol.SessionType(a.Harness, a.Worker, a.Type) == "merger" && firstNonEmpty(a.Repo.Project, a.Profile.Project) == n.Ref.ID {
				status := "offline"
				if a.Online {
					status = firstNonEmpty(a.Status, "idle")
				}
				add("⇄ "+firstNonEmpty(a.Profile.Name, "merger")+" · "+status, "busy")
			}
		}
	}
	charter := n.Charter
	if merged, ok := m.mp.merged[selected]; ok {
		if merged.Charter != (protocol.Charter{}) {
			charter = merged.Charter
		}
		if len(merged.Settings) > 0 {
			n.Settings = merged.Settings
		}
	}
	add("", "label")
	add("CHARTER", "selected")
	for _, v := range graphCharterValues(charter) {
		if v[1] != "" {
			add(v[0]+"  "+v[1], "label")
		}
	}
	add("", "label")
	add("YOUR CHOICES", "selected")
	defaults := protocol.NodeDefaults(charter.Maturity, n.Settings)
	for _, v := range [][2]string{{protocol.SettingTestsBeforeMerge, defaults.TestsBeforeMerge}, {protocol.SettingMergeAuthority, defaults.MergeAuthority}, {protocol.SettingWorkerSandbox, defaults.WorkerSandbox}, {protocol.SettingJudgeConsumers, defaults.JudgeConsumers}, {protocol.SettingDeployRule, defaults.DeployRule}, {protocol.SettingAutonomy, firstNonEmpty(n.Settings[protocol.SettingAutonomy], "off")}} {
		prefix, source := "  ", " default"
		if _, ok := n.Settings[v[0]]; ok {
			prefix, source = "◆ ", " override"
		}
		add(prefix+strings.ReplaceAll(v[0], "_", " ")+"  "+v[1]+source, "label")
	}
	add("", "label")
	add("ROUTING", "selected")
	edgeLines := []string{}
	for _, e := range m.mp.edges {
		if e.Kind == "member_of" || e.From != selected && e.To != selected {
			continue
		}
		peer := e.To
		direction := "to "
		if e.To == selected {
			peer = e.From
			direction = "from "
		}
		edgeLines = append(edgeLines, direction+m.graphName(peer)+"  "+e.Kind)
		if e.Mode != "" || e.Rule != "" {
			add(direction+m.graphName(peer)+"  "+strings.TrimSpace(e.Mode+" "+e.Rule), "label")
		}
	}
	add("", "label")
	add("EDGES", "selected")
	for _, s := range edgeLines {
		add(s, "label")
	}
	offset := min(max(m.mp.teamCardScroll, 0), max(0, len(lines)-(h-2)))
	for i := 0; i < h-2 && i+offset < len(lines); i++ {
		v := lines[i+offset]
		text(x+2, y+1+i, w-4, v.text, v.role, "")
	}
	if len(lines) > h-2 {
		text(x+2, y+h-1, w-4, "pgup/pgdn · more", "labelDim", "")
	}
}

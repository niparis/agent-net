package tui

import (
	"fmt"
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
	connected := map[string]bool{}
	for _, e := range links {
		if e.a == selectedTeam {
			connected[e.b] = true
		}
		if e.b == selectedTeam {
			connected[e.a] = true
		}
	}
	sort.SliceStable(groups, func(i, j int) bool {
		ai, aj := connected[groups[i].key], connected[groups[j].key]
		if ai != aj {
			return ai
		}
		return groups[i].label < groups[j].label
	})
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
		return graphview.SceneNode{Name: key, Label: label, LabelSuffix: suffix, Kind: kind, Position: graphview.Point{X: (float64(x) + .5) / float64(size.Width), Y: (float64(y) + .5) / float64(size.Height)}, Role: graphLastingRole(sessions), Selected: strong, HasCharter: n.Charter != (protocol.Charter{}), BusySessions: busy, Card: graphview.SceneCard{Sessions: len(sessions), Maturity: n.Charter.Maturity}}
	}
	small := func(g teamGraphData, x, y, w, h int, strong bool) {
		frame(x, y, w, h, g, strong)
		n := node(g.lead, x+3, y+h/2, strong)
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
		text(x+6, y+1, w-7, lead, "label", g.key)
		text(x+6, y+2, w-7, fmt.Sprintf("%d projects · %d sessions", g.projects, g.sessions), "labelDim", g.key)
		state := g.role
		if g.busy > 0 && state != "needs you" {
			state = fmt.Sprintf("%d busy", g.busy)
		}
		text(x+6, y+3, w-7, state, g.role, g.key)
		if h > 5 {
			text(x+6, y+4, w-7, g.merger, "busy", g.key)
		}
	}
	if m.teamFilter == "" {
		// Keep connected teams adjacent to the selected one, then place the
		// unconnected teams. Rows have gutters reserved for edge routing.
		if _, ok := byKey[selectedTeam]; !ok {
			selectedTeam = groups[0].key
		}
		ordered := make([]teamGraphData, 0, len(groups))
		for _, g := range groups {
			if g.key != selectedTeam {
				ordered = append(ordered, g)
			}
		}
		at := min(1, len(ordered))
		ordered = append(ordered, teamGraphData{})
		copy(ordered[at+1:], ordered[at:])
		ordered[at] = byKey[selectedTeam]
		cols := 3
		if size.Width < 90 {
			cols = 2
		}
		gap := 4
		w := (size.Width - (cols-1)*gap) / cols
		h := 6
		stride := h + 2
		selRow := at / cols
		offset := max(0, (selRow+1)*stride-size.Height+1)
		offset = (offset / stride) * stride
		boxes := map[string]graphview.SceneFrame{}
		for i, g := range ordered {
			x := (i % cols) * (w + gap)
			y := (i/cols)*stride - offset
			if y < 0 || y+h > size.Height {
				continue
			}
			small(g, x, y, w, h, g.key == selectedTeam)
			boxes[g.key] = scene.Frames[len(scene.Frames)-1]
		}
		for _, e := range links {
			a, ok := boxes[e.a]
			b, ok2 := boxes[e.b]
			if !ok || !ok2 {
				continue
			}
			// Route only in the gutters: leave the bottom border, follow its
			// row gutter and a column gutter, then enter the other frame.
			ax, bx := a.X+a.Width/2, b.X+b.Width/2
			ay, by := a.Y+a.Height-1, b.Y+b.Height-1
			gx := min(size.Width-1, max(0, min(a.X+a.Width, b.X+b.Width)+gap/2))
			if a.X == b.X {
				gx = min(size.Width-1, a.X+a.Width+1)
			}
			pt := func(x, y int) graphview.Point {
				return graphview.Point{X: float64(x) / float64(size.Width), Y: float64(y) / float64(size.Height)}
			}
			path := []graphview.Point{pt(ax, ay), pt(ax, ay+1), pt(gx, ay+1), pt(gx, by+1), pt(bx, by+1), pt(bx, by)}
			// Do not route on the screen edge when the lower gutter is absent.
			if ay+1 >= size.Height || by+1 >= size.Height {
				continue
			}
			scene.Edges = append(scene.Edges, graphview.SceneEdge{Kind: e.kind, Path: path})
			text(gx, min(ay+1, by+1), 4, fmt.Sprint(e.count), e.kind, "")
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
	neighbours := map[string]bool{}
	for _, e := range links {
		if e.a == teamKey {
			neighbours[e.b] = true
		}
		if e.b == teamKey {
			neighbours[e.a] = true
		}
	}
	inside := m.mp.teamInside
	mainX, mainW := 0, size.Width
	if !inside && size.Width >= 90 && len(neighbours) > 0 {
		mainX = size.Width / 4
		mainW = size.Width / 2
	}
	cardX := size.Width
	if inside && size.Width >= 75 {
		cardX = size.Width * 62 / 100
		mainW = cardX - 1
		scene.ZoomRatio = 1.4
	}
	mainH := size.Height
	if !inside && len(groups) > 1 {
		mainH = max(5, size.Height-2)
	}
	frame(mainX, 0, mainW, mainH, g, true)
	text(mainX+2, 1, mainW-4, fmt.Sprintf("lead %s · %d projects · %d sessions", m.graphName(g.lead), g.projects, g.sessions), "labelDim", "")
	positions := map[string]graphview.Point{}
	cols := 2
	if mainW < 36 {
		cols = 1
	}
	rows := (len(g.members) + cols - 1) / cols
	for i, key := range g.members {
		col, row := i%cols, i/cols
		x := mainX + 3 + col*max(1, (mainW-6)/cols)
		y := 3 + (row * max(3, (mainH-5)/max(rows, 1)))
		y = min(y, mainH-2)
		n := node(key, x, y, key == selected)
		positions[key] = n.Position
		scene.Nodes = append(scene.Nodes, n)
		label := n.Label
		if key == g.lead {
			label = "★ " + label
		}
		width := max(1, mainX+mainW-x-5)
		if cols > 1 && col == 0 {
			width = max(1, (mainW-6)/cols-5)
		}
		if n.LabelSuffix != "" && ansi.StringWidth(label) <= width {
			prefix := strings.TrimSuffix(label, n.LabelSuffix)
			text(x+3, y-1, width, prefix, n.Role, key)
			text(x+3+ansi.StringWidth(prefix), y-1, width-ansi.StringWidth(prefix), n.LabelSuffix, "labelDim", key)
		} else {
			text(x+3, y-1, width, label, n.Role, key)
		}
		text(x+3, y, width, fmt.Sprintf("%d sessions", n.Card.Sessions), "labelDim", key)
		if n.BusySessions > 0 {
			text(x+3, y+1, width, fmt.Sprintf("%d busy", n.BusySessions), "busy", key)
		}
	}
	if !inside && mainX > 0 {
		i := 0
		for _, ng := range groups {
			if !neighbours[ng.key] {
				continue
			}
			x := 0
			if i%2 == 1 {
				x = mainX + mainW
			}
			y := (i / 2) * 7
			if y+6 > mainH {
				break
			}
			small(ng, x, y, mainX-1, 6, false)
			portX := x + mainX - 2
			if x > 0 {
				portX = x
			}
			positions[ng.key] = graphview.Point{X: float64(portX) / float64(size.Width), Y: float64(y+3) / float64(size.Height)}
			i++
		}
	}
	if inside {
		i := 0
		for _, ng := range groups {
			if !neighbours[ng.key] {
				continue
			}
			x := mainX + mainW - 13
			y := 2 + i*3
			if y >= mainH-1 {
				break
			}
			text(x, y, 12, ng.label+" ▸", "selected", ng.key)
			positions[ng.key] = graphview.Point{X: float64(x) / float64(size.Width), Y: float64(y) / float64(size.Height)}
			scene.Nodes = append(scene.Nodes, graphview.SceneNode{Name: ng.key, Position: positions[ng.key], Role: "offline", Selected: selected == ng.key})
			i++
		}
	}
	for _, e := range m.mp.edges {
		if e.Kind == "member_of" {
			continue
		}
		a, aok := positions[e.From]
		b, bok := positions[e.To]
		if !aok && bok {
			a, aok = positions[m.graphTeamOf(e.From)]
		}
		if !bok && aok {
			b, bok = positions[m.graphTeamOf(e.To)]
		}
		if !aok || !bok || a == b {
			continue
		}
		scene.Edges = append(scene.Edges, graphview.SceneEdge{FromName: e.From, ToName: e.To, From: a, To: b, Kind: e.Kind, Mode: e.Mode, Rule: e.Rule, Selected: e.From == selected || e.To == selected})
	}
	if !inside && len(groups) > 1 {
		x := 1
		text(x, size.Height-1, 11, "more teams", "labelDim", "")
		x += 12
		for _, ng := range groups {
			if ng.key == g.key || neighbours[ng.key] {
				continue
			}
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

package phone

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/operator"
	"github.com/niparis/agent-net/internal/protocol"
)

// The Graph screen is a server-rendered SVG of the same read the TUI Graph tab
// uses: OpGraphView with places and the team records. It is read only.

const (
	graphCellW  = 190
	graphCellH  = 44
	graphPad    = 16
	graphHeader = 46
	graphGap    = 22
	graphStaffW = 180
	graphTop    = 74
	graphLeft   = 20
	graphStaffY = 30
)

// graphNodeView is one circle and its two lines of text.
type graphNodeView struct {
	Key, Name, Sub, Class string
	X, Y                  int // circle centre
	TextX, TextY, SubY    int
	Selected              bool
}

type graphBoxView struct {
	X, Y, W, H int
	Title, Sub string
	TextX      int
	TextY      int
	SubY       int
	Nodes      []*graphNodeView
}

type graphEdgeView struct {
	Class, D, Aria string
}

type graphDetailView struct {
	Name, State, StateClass, Harness, Model, Place, Machine, Steps, Purpose string
	Members                                                                 []string
}

type graphView struct {
	Err                            string
	Boxes                          []graphBoxView
	Staff                          []*graphNodeView
	Edges                          []graphEdgeView
	Detail                         *graphDetailView
	Width, Height                  int
	TeamCount, NodeCount, Messages int
	TeamLabel, NodeLabel           string
	MessagesLabel                  string
}

// graphKey is the key a graph selection uses. A repo keeps its name; agent
// and team keys carry their kind so two records cannot collide.
func graphKey(ref *protocol.GraphRef) string {
	if ref == nil {
		return ""
	}
	switch ref.Kind {
	case "repo":
		return ref.ID
	case "agent", "team":
		return ref.Kind + ":" + ref.ID
	}
	return ref.Kind + ":" + ref.ID
}

func graphStateClass(state string) string {
	switch state {
	case "needs you":
		return "you"
	case "busy":
		return "busy"
	case "offline", "off":
		return "off"
	}
	return ""
}

func graphStateLabel(state string) string {
	switch state {
	case "needs you", "busy", "offline":
		return state
	case "off", "":
		return "offline"
	}
	return "idle"
}

func graphStateRank(state string) int {
	switch state {
	case "needs you":
		return 3
	case "busy":
		return 2
	case "idle":
		return 1
	}
	return 0
}

func graphBetterState(have, next string) string {
	if graphStateRank(next) > graphStateRank(have) {
		return next
	}
	return have
}

// graphNodeHolds says whether a session is a member of a node.
func graphNodeHolds(n protocol.Node, a protocol.AgentView) bool {
	if n.Ref == nil {
		return false
	}
	switch n.Ref.Kind {
	case "agent":
		return a.AgentID != "" && a.AgentID == n.Ref.ID
	case "repo":
		project := firstNonEmpty(a.Repo.Project, a.Profile.Project)
		return project != "" && (project == n.Ref.ID || project == n.Name)
	}
	return false
}

// graphNodeState reads the live state of one record from its sessions. A
// record with no online session is offline, as the TUI Graph shows it.
func graphNodeState(n protocol.Node, snap operator.Snapshot) string {
	state := ""
	for _, s := range n.Sessions {
		if s.Status != "" {
			state = graphBetterState(state, s.Status)
		}
	}
	for _, a := range snap.Agents {
		if !a.Online || !graphNodeHolds(n, a) {
			continue
		}
		if a.Waiting != nil {
			state = graphBetterState(state, "needs you")
			continue
		}
		s := a.Status
		if s == "" {
			s = "idle"
		}
		state = graphBetterState(state, s)
	}
	if state == "" {
		return "offline"
	}
	return state
}

func graphAgentsFor(n protocol.Node, snap operator.Snapshot) []protocol.AgentView {
	var out []protocol.AgentView
	for _, a := range snap.Agents {
		if graphNodeHolds(n, a) {
			out = append(out, a)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Online != out[j].Online {
			return out[i].Online
		}
		return out[i].LastActive > out[j].LastActive
	})
	return out
}

// graphClip keeps an SVG label inside its cell. The detail card has the full
// name, so the drawing can be short.
func graphClip(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n-1]) + "…"
}

func graphNodeViewFor(n protocol.Node, key, state string, selected bool) *graphNodeView {
	name := firstNonEmpty(n.Name, key)
	return &graphNodeView{Key: key, Name: graphClip(name, 26), Sub: graphStateLabel(state), Class: graphStateClass(state), Selected: selected}
}

// graphLoad reads the graph, the chief status and the conversations of the last
// hour. The conversations supply the dashed message edges.
func (a *app) graph(ctx context.Context, snap operator.Snapshot, selected string) *graphView {
	v := &graphView{}
	raw, err := a.src.Call(ctx, protocol.OpGraphView, protocol.QueryGraph{Places: true, All: true})
	if err != nil {
		v.Err = "The graph could not be read: " + firstLine(err.Error())
		return v
	}
	var g protocol.GraphView
	if err := json.Unmarshal(raw, &g); err != nil {
		v.Err = "The graph could not be read: " + firstLine(err.Error())
		return v
	}
	var chief protocol.ChiefStatus
	if raw, err := a.src.Call(ctx, protocol.OpChiefGet, nil); err == nil {
		_ = json.Unmarshal(raw, &chief)
	}

	byKey := map[string]protocol.Node{}
	keyByName := map[string]string{}
	keyByRefID := map[string]string{}
	for _, n := range g.Nodes {
		if n.Ref == nil {
			continue
		}
		key := graphKey(n.Ref)
		byKey[key] = n
		keyByName[n.Name] = key
		keyByRefID[n.Ref.ID] = key
	}
	// A team is a member_of edge, or the record's place when an older gateway
	// sends no edge. Member keys are stable, names are only a fallback.
	teamOf := map[string]string{}
	for _, e := range g.Edges {
		if e.Kind != "member_of" || e.FromRef == nil || e.ToRef == nil || e.ToRef.Kind != "team" {
			continue
		}
		teamOf[graphKey(e.FromRef)] = graphKey(e.ToRef)
	}
	for key, n := range byKey {
		if n.Place != nil && n.Place.Team != "" {
			if teamKey, ok := keyByName[n.Place.Team]; ok && strings.HasPrefix(teamKey, "team:") {
				if _, set := teamOf[key]; !set {
					teamOf[key] = teamKey
				}
			}
		}
	}

	// Teams, sorted by name.
	var teams []protocol.Node
	for _, n := range g.Nodes {
		if n.Ref == nil || n.Ref.Kind != "team" {
			continue
		}
		if n.Active != nil && !*n.Active {
			continue
		}
		teams = append(teams, n)
	}
	sort.SliceStable(teams, func(i, j int) bool { return teams[i].Name < teams[j].Name })
	teamKeys := map[string]bool{}
	for _, t := range teams {
		teamKeys[graphKey(t.Ref)] = true
	}

	members := map[string][]*graphNodeView{}
	teamState := map[string]string{}
	var loose []*graphNodeView
	var ctoNode *protocol.Node
	for _, n := range g.Nodes {
		if n.Ref == nil {
			if n.Kind == "cto" || n.Name == protocol.CTONodeName {
				copy := n
				ctoNode = &copy
			}
			continue
		}
		if n.Ref.Kind == "team" {
			continue
		}
		if n.Kind == "cto" || n.Name == protocol.CTONodeName {
			copy := n
			ctoNode = &copy
			continue
		}
		key := graphKey(n.Ref)
		state := graphNodeState(n, snap)
		node := graphNodeViewFor(n, key, state, key == selected)
		teamKey := teamOf[key]
		if teamKey == "" || !teamKeys[teamKey] {
			loose = append(loose, node)
			continue
		}
		members[teamKey] = append(members[teamKey], node)
		teamState[teamKey] = graphBetterState(teamState[teamKey], state)
	}
	for key := range members {
		sort.SliceStable(members[key], func(i, j int) bool { return members[key][i].Name < members[key][j].Name })
	}
	sort.SliceStable(loose, func(i, j int) bool { return loose[i].Name < loose[j].Name })

	// Staff at the top: the owner, the CTO and the chief of staff.
	v.Staff = append(v.Staff, &graphNodeView{Key: "owner", Name: "you", Sub: "owner", Selected: selected == "owner"})
	ctoState := "offline"
	var cto protocol.Node
	if ctoNode != nil {
		cto = *ctoNode
		ctoState = graphNodeState(cto, snap)
		if ctoState == "offline" {
			for _, a := range snap.Agents {
				if a.Online && a.Harness == "cto" {
					ctoState = "busy"
					if a.Status == "idle" || a.Status == "" {
						ctoState = "idle"
					}
				}
			}
		}
	} else {
		cto = protocol.Node{Name: protocol.CTONodeName, Kind: "cto"}
	}
	v.Staff = append(v.Staff, &graphNodeView{Key: "cto", Name: "CTO", Sub: graphStateLabel(ctoState), Class: "cto", Selected: selected == "cto"})
	chiefState := "not set up"
	if chief.SessionID != "" {
		chiefState = "offline"
		if chief.Online {
			chiefState = "idle"
		}
		for _, a := range snap.Agents {
			if a.SessionID == chief.SessionID && a.Online {
				chiefState = firstNonEmpty(a.Status, "idle")
				if a.Waiting != nil {
					chiefState = "needs you"
				}
			}
		}
	}
	v.Staff = append(v.Staff, &graphNodeView{Key: "chief", Name: "chief of staff", Sub: chiefState, Class: graphStateClass(chiefState), Selected: selected == "chief"})

	for _, t := range teams {
		key := graphKey(t.Ref)
		state := teamState[key]
		if state == "" {
			state = "offline"
		}
		title := "team " + t.Name
		lead := ""
		mode := ""
		if t.Place != nil {
			lead, mode = t.Place.TeamLead, t.Place.Mode
		}
		if lead == "" && t.Lead != nil {
			if n, ok := byKey[graphKey(t.Lead)]; ok {
				lead = n.Name
			}
		}
		if mode == "" {
			mode = t.AuthorityMode
		}
		parts := []string{}
		if lead != "" {
			parts = append(parts, "lead "+lead)
		}
		if mode != "" {
			parts = append(parts, protocol.AuthorityModeLabel(mode))
		}
		v.Boxes = append(v.Boxes, graphBoxView{Title: graphClip(title, 26), Sub: graphClip(strings.Join(parts, " · "), 32), Nodes: members[key]})
		v.TeamCount++
	}
	if len(loose) > 0 {
		v.Boxes = append(v.Boxes, graphBoxView{Title: "answers to you", Sub: "no team", Nodes: loose})
	}

	layoutGraph(v)
	v.NodeCount = len(v.Staff)
	for _, b := range v.Boxes {
		v.NodeCount += len(b.Nodes)
	}
	v.TeamLabel, v.NodeLabel = plural(v.TeamCount, "team"), plural(v.NodeCount, "node")

	// Depends edges are solid. They are drawn only when both ends are visible.
	pos := map[string][2]int{}
	for _, n := range v.Staff {
		pos[n.Key] = [2]int{n.X, n.Y}
	}
	for _, b := range v.Boxes {
		for _, n := range b.Nodes {
			pos[n.Key] = [2]int{n.X, n.Y}
		}
	}
	edgeKey := func(ref *protocol.GraphRef, name string) string {
		if ref != nil {
			if k := graphKey(ref); k != "" {
				if _, ok := pos[k]; ok {
					return k
				}
			}
		}
		if k, ok := keyByName[name]; ok {
			if _, ok := pos[k]; ok {
				return k
			}
		}
		if k, ok := keyByRefID[name]; ok {
			if _, ok := pos[k]; ok {
				return k
			}
		}
		return ""
	}
	for _, e := range g.Edges {
		if e.Kind != "depends" {
			continue
		}
		from, to := edgeKey(e.FromRef, e.From), edgeKey(e.ToRef, e.To)
		if from == "" || to == "" || from == to {
			continue
		}
		v.Edges = append(v.Edges, graphEdge(from, to, pos, false, "depends on"))
	}
	// Dashed message edges are conversations whose newest message is in the
	// last hour and whose two sessions both map to a visible record.
	now := time.Now()
	type pair struct{ a, b string }
	counts := map[pair]int{}
	order := []pair{}
	agentKeys, repoKeys := map[string]string{}, map[string]string{}
	for key, n := range byKey {
		if n.Ref == nil {
			continue
		}
		switch n.Ref.Kind {
		case "agent":
			agentKeys[n.Ref.ID] = key
		case "repo":
			repoKeys[n.Ref.ID] = key
			if n.Name != "" {
				repoKeys[n.Name] = key
			}
		}
	}
	sessionKey := func(session string) string {
		for _, a := range snap.Agents {
			if a.SessionID != session {
				continue
			}
			switch {
			case protocol.IsOwner(a.Harness):
				return "owner"
			case a.Harness == "cto":
				return "cto"
			}
			if key := agentKeys[a.AgentID]; key != "" {
				return key
			}
			project := firstNonEmpty(a.Repo.Project, a.Profile.Project)
			if key := repoKeys[project]; key != "" {
				return key
			}
			return ""
		}
		return ""
	}
	if raw, err := a.src.Call(ctx, protocol.OpConversations, protocol.QueryConversations{Limit: 100}); err == nil {
		var page protocol.ConversationPage
		if json.Unmarshal(raw, &page) == nil {
			for _, c := range page.Conversations {
				if c.At.IsZero() || now.Sub(c.At) > time.Hour {
					continue
				}
				from, to := sessionKey(c.From), sessionKey(c.To)
				if from == "" || to == "" || from == to {
					continue
				}
				if from > to {
					from, to = to, from
				}
				p := pair{from, to}
				if counts[p] == 0 {
					order = append(order, p)
				}
				counts[p]++
				v.Messages++
			}
		}
	}
	sort.SliceStable(order, func(i, j int) bool {
		if order[i].a != order[j].a {
			return order[i].a < order[j].a
		}
		return order[i].b < order[j].b
	})
	v.MessagesLabel = plural(v.Messages, "message")
	for _, p := range order {
		v.Edges = append(v.Edges, graphEdge(p.a, p.b, pos, true, plural(counts[p], "message")+" in the last hour"))
	}

	v.Detail = graphDetail(g, snap, selected, byKey, members, teamState, chief, ctoState, chiefState)
	return v
}

// layoutGraph places the staff row, team boxes and loose box. Every member
// gets its own cell, so labels never overlap; a wide graph scrolls inside its
// own box on a phone.
func layoutGraph(v *graphView) {
	for i, n := range v.Staff {
		n.X, n.Y = graphLeft+i*graphStaffW+8, graphStaffY
		n.TextX, n.TextY, n.SubY = n.X+17, n.Y-3, n.Y+13
	}
	staffRight := graphLeft + len(v.Staff)*graphStaffW
	x, y := graphLeft, graphTop
	maxH := 0
	for i := range v.Boxes {
		b := &v.Boxes[i]
		cols := 2
		if len(b.Nodes) <= 2 {
			cols = 1
		}
		rows := (len(b.Nodes) + cols - 1) / cols
		if rows < 1 {
			rows = 1
		}
		b.X, b.Y = x, y
		b.W = 2*graphPad + cols*graphCellW
		b.H = graphHeader + rows*graphCellH + graphPad
		b.TextX, b.TextY, b.SubY = b.X+14, b.Y+20, b.Y+34
		for j, n := range b.Nodes {
			col, row := j%cols, j/cols
			n.X = b.X + graphPad + col*graphCellW + 10
			n.Y = b.Y + graphHeader + row*graphCellH + 12
			n.TextX, n.TextY, n.SubY = n.X+17, n.Y-3, n.Y+13
		}
		x += b.W + graphGap
		if b.H > maxH {
			maxH = b.H
		}
	}
	v.Width = max(staffRight+graphLeft, x-graphGap+graphLeft)
	v.Height = y + maxH + 24
}

func graphEdge(from, to string, pos map[string][2]int, message bool, aria string) graphEdgeView {
	a, b := pos[from], pos[to]
	mx, my := (a[0]+b[0])/2, (a[1]+b[1])/2-20
	class := "edge dep"
	if message {
		class = "edge msg"
	}
	return graphEdgeView{Class: class, D: fmt.Sprintf("M%d %d Q%d %d %d %d", a[0], a[1], mx, my, b[0], b[1]), Aria: aria}
}

// graphDetail fills the card of the selected record. The name and the place
// come from the graph; the harness, model, machine and charter come from the
// session that holds the record when it is online.
func graphDetail(g protocol.GraphView, snap operator.Snapshot, selected string,
	byKey map[string]protocol.Node, members map[string][]*graphNodeView, teamState map[string]string,
	chief protocol.ChiefStatus, ctoState, chiefState string) *graphDetailView {
	d := &graphDetailView{}
	switch selected {
	case "", "owner":
		if selected != "owner" {
			selected = "owner"
		}
		d.Name, d.State, d.StateClass = "you", "owner", ""
		d.Place, d.Purpose = "everything without a lead answers to you", "The owner of the network."
		return d
	case "cto":
		d.Name, d.State, d.StateClass = "CTO", graphStateLabel(ctoState), graphStateClass(ctoState)
		d.Place, d.Purpose = "answers to you", "Runs the network for you. Sees every charter and rule."
		return d
	case "chief":
		d.Name, d.State, d.StateClass = "chief of staff", chiefState, graphStateClass(chiefState)
		if chief.SessionID == "" {
			d.State = "not set up"
		} else {
			d.Model = chief.Model
		}
		d.Place, d.Purpose = "answers to you", "Sorts what waits for you before it reaches the Desk."
		return d
	}
	n, ok := byKey[selected]
	if !ok {
		// A selected staff or box key that no longer exists falls back to the
		// owner card instead of an empty panel.
		d.Name, d.State = "you", "owner"
		d.Place, d.Purpose = "everything without a lead answers to you", "The owner of the network."
		return d
	}
	d.Name = firstNonEmpty(n.Name, selected)
	state := graphNodeState(n, snap)
	if n.Ref != nil && n.Ref.Kind == "team" {
		if s := teamState[selected]; s != "" {
			state = s
		}
		d.Members = nil
		for _, member := range members[selected] {
			d.Members = append(d.Members, member.Name)
		}
	} else {
		agents := graphAgentsFor(n, snap)
		if len(agents) > 0 {
			d.Harness, d.Model = agents[0].Harness, agents[0].Model
		}
		if n.Ref != nil && (n.Ref.Kind == "repo" || n.Ref.Kind == "agent") {
			d.Steps = graphStepOwners(n, g.Edges)
			if n.Ref.Kind == "agent" && d.Steps == "not set" && len(agents) > 0 {
				if project := firstNonEmpty(agents[0].Repo.Project, agents[0].Profile.Project); project != "" {
					if repo, ok := byKey[project]; ok && repo.Ref != nil && repo.Ref.Kind == "repo" {
						d.Steps = graphStepOwners(repo, g.Edges)
					}
				}
			}
		}
	}
	d.State, d.StateClass = graphStateLabel(state), graphStateClass(state)
	if n.Place != nil && n.Place.Team != "" {
		d.Place = "team " + n.Place.Team
		if n.Place.TeamLead != "" {
			d.Place += " · lead " + n.Place.TeamLead
		}
		if n.Place.Mode != "" {
			d.Place += " · " + protocol.AuthorityModeLabel(n.Place.Mode)
		}
	} else {
		d.Place = "answers to you"
	}
	machines := map[string]bool{}
	for _, loc := range n.Locations {
		if loc.Machine != "" {
			machines[loc.Machine] = true
		}
	}
	for _, a := range graphAgentsFor(n, snap) {
		if a.Hostname != "" {
			machines[a.Hostname] = true
		}
	}
	if n.Ref != nil && n.Ref.Kind == "team" {
		for _, member := range members[selected] {
			mn, ok := byKey[member.Key]
			if !ok {
				continue
			}
			for _, loc := range mn.Locations {
				if loc.Machine != "" {
					machines[loc.Machine] = true
				}
			}
			for _, a := range graphAgentsFor(mn, snap) {
				if a.Hostname != "" {
					machines[a.Hostname] = true
				}
			}
		}
	}
	var names []string
	for m := range machines {
		names = append(names, m)
	}
	sort.Strings(names)
	d.Machine = strings.Join(names, ", ")
	d.Purpose = n.Charter.Purpose
	for _, a := range graphAgentsFor(n, snap) {
		if d.Purpose == "" && a.Charter.Purpose != "" {
			d.Purpose = a.Charter.Purpose
		}
	}
	return d
}

// graphStepOwners groups the delivery assignment edges that point at a repo by
// owner, in step order: "accept: caller, test: merger-node".
func graphStepOwners(n protocol.Node, edges []protocol.Edge) string {
	type group struct {
		owner string
		steps []string
	}
	var groups []group
	index := map[string]int{}
	for _, e := range edges {
		step := protocol.DeliveryEdgeStep(e.Kind)
		if step == "" {
			continue
		}
		target := e.To
		if e.ToRef != nil {
			target = e.ToRef.ID
		}
		if target != n.Name && (n.Ref == nil || target != n.Ref.ID) {
			continue
		}
		owner := stepOwnerLabel(protocol.DeliveryOwnerFromSource(e.From))
		i, ok := index[owner]
		if !ok {
			i = len(groups)
			index[owner] = i
			groups = append(groups, group{owner: owner})
		}
		groups[i].steps = append(groups[i].steps, step)
	}
	if len(groups) == 0 {
		return "not set"
	}
	var parts []string
	for _, g := range groups {
		parts = append(parts, strings.Join(g.steps, ", ")+": "+g.owner)
	}
	sort.Strings(parts)
	return strings.Join(parts, " · ")
}

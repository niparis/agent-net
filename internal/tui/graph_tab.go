package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/tui/graphview"
)

var graphSelectionStyle = lipgloss.NewStyle().Bold(true).Foreground(graphview.AccentColor)

type graphTickMsg struct{ generation int }

const (
	// graphIdleInterval keeps the graph alive without drawing when nothing
	// moves. graphFrameInterval is the fastest pixel frame request. A 120x34
	// frame with 20 nodes costs about 90 ms to rasterize off the UI loop, so
	// 8 requests a second leaves room for the terminal and the rest of the UI.
	graphIdleInterval  = 250 * time.Millisecond
	graphFrameInterval = 125 * time.Millisecond
)

// graphPixelState holds the pixel-mode decision and the last finished frame.
// Rendering happens in a tea.Cmd, never in View or Update.
type graphPixelState struct {
	graphics     bool
	profile      colorprofile.Profile
	cellW, cellH int
	imageID      uint32
	hash         [32]byte
	have         bool
	ready        bool
	rendering    bool
	size         graphview.Size
	key          [32]byte
	lines        []string
	hits         map[graphview.Cell]string
	transmit     string
	// shown is the image id the placeholders point at now. Frames alternate
	// between imageID and imageID+1: a new frame is sent under the other id
	// and the old one is deleted only after the switch, so the terminal never
	// shows an empty graph between frames.
	shown uint32
	// motion turns on the animated pixel graph (message dots, busy pulse,
	// eased camera). Off by default: every animated frame is a new image, and
	// the owner prefers a still graph that changes only when the graph does.
	motion bool
}

// rasterizeFrame is a variable so a test can prove View never calls it.
var rasterizeFrame = graphview.Rasterize

// graphFrameMsg carries a finished image and its placeholder rows back to the
// update loop.
type graphFrameMsg struct {
	id       uint32
	key      [32]byte
	size     graphview.Size
	lines    []string
	hits     map[graphview.Cell]string
	hash     [32]byte
	have     bool
	transmit string
}

// The kitty placeholder names its image with a 24-bit foreground color.
// Lower output profiles rewrite or remove that color and make the image blank.
func (g *graphPixelState) enabled() bool {
	return g != nil && g.graphics && g.profile == colorprofile.TrueColor
}

func (g *graphPixelState) set(gr graphview.Graphics) {
	if g == nil {
		return
	}
	g.graphics = gr.Pixel
	g.cellW, g.cellH = gr.CellWidth, gr.CellHeight
}

// stillScene drops what only moves: message dots, arrival ripples and the busy
// pulse. The pixel graph then changes only when the graph itself does.
func stillScene(s *graphview.Scene) {
	s.Comets, s.Ripples = nil, nil
	for i := range s.Nodes {
		s.Nodes[i].Pulse = false
		s.Nodes[i].PulsePhase = .5
	}
}

func graphTick(generation int) tea.Cmd {
	return graphTickAfter(generation, graphIdleInterval)
}

func graphTickAfter(generation int, interval time.Duration) tea.Cmd {
	return tea.Tick(interval, func(time.Time) tea.Msg { return graphTickMsg{generation} })
}

// graphFrameInterval chooses the next frame delay: fast only while pixel mode
// has something to animate.
func (m Model) graphFrameInterval() time.Duration {
	if !m.graphPixel.enabled() {
		return graphIdleInterval
	}
	if m.graphPixel.motion && m.graphState.Moving() {
		return graphFrameInterval
	}
	return graphIdleInterval
}

// smoothCamera turns on the eased camera only for the pixel renderer.
func (m *Model) smoothCamera() {
	m.graphState.Smooth = m.graphPixel.enabled() && m.graphPixel.motion
}

// graphScene builds the renderer-independent scene with display names. It is
// cheap and never touches pixels.
func (m Model) graphScene(state graphview.State, size graphview.Size) graphview.Scene {
	scene := graphview.BuildScene(state, graphview.Viewport{Width: float64(size.Width), Height: float64(size.Height)})
	duplicates := map[string]int{}
	for _, n := range m.mp.nodes {
		if n.Ref != nil && (n.Ref.Kind == "repo" || n.Ref.Kind == "agent") {
			duplicates[strings.ToLower(m.graphName(n.Name))]++
		}
	}
	scene.Hints = nil
	for _, binding := range keyBindings {
		if binding.context == contextGraph {
			scene.Hints = append(scene.Hints, graphview.KeyHint{Keys: binding.keys, Action: string(binding.action)})
		}
	}
	for i := range scene.Nodes {
		if scene.Nodes[i].Name == protocol.GraphVoiceOwner {
			continue
		}
		scene.Nodes[i].Label = m.graphName(scene.Nodes[i].Name)
		scene.Nodes[i].Card.Name = m.graphName(scene.Nodes[i].Name)
		if node, ok := m.mapNode(scene.Nodes[i].Name); ok && node.Ref != nil {
			scene.Nodes[i].Kind = node.Ref.Kind
			if (node.Ref.Kind == "repo" || node.Ref.Kind == "agent") && duplicates[strings.ToLower(m.graphName(node.Name))] > 1 {
				scene.Nodes[i].LabelSuffix = " · " + node.Ref.Kind
				scene.Nodes[i].Label += scene.Nodes[i].LabelSuffix
			}
		}
	}
	for i := range scene.Nodes {
		n := &scene.Nodes[i]
		if n.Name == dormantChip {
			n.Position = graphview.Point{X: .02, Y: float64(size.Height-1) / float64(size.Height)}
		}
		if node, ok := m.mapNode(n.Name); ok {
			if node.Ref != nil {
				n.Kind = node.Ref.Kind
			}
			n.Role = m.graphLiveRole(node)
			if m.mp.dormant[n.Name] || m.mp.outside[n.Name] {
				n.Role = "offline"
			}
		}
		if mark := m.mp.everything[n.Name]; mark != "" {
			n.Wildcard = true
			n.WildcardLabel = mark
		}
	}
	if teamScene, ok := m.teamGraphScene(scene, size); ok {
		return teamScene
	}
	scene.Groups = m.graphGroups(scene)
	if len(scene.Groups) > 0 {
		scene = graphview.ArrangeGroups(scene, size)
	}
	return scene
}

// scheduleGraphRender starts one background frame when the cheap scene key
// changed. While a frame is in flight new frames are dropped; the next update
// after it finishes starts another if the scene moved on.
func (m *Model) scheduleGraphRender() tea.Cmd {
	g := m.graphPixel
	if !g.enabled() || g.cellW <= 0 || g.cellH <= 0 {
		return nil
	}
	if m.tab != graphTab || m.mode != modeNormal || m.mapOn || m.help || m.graphState.Graph == nil {
		return nil
	}
	if g.rendering {
		return nil
	}
	gw, gh := m.graphSize()
	if gw <= 0 || gh <= 0 {
		return nil
	}
	size := graphview.Size{Width: gw, Height: gh}
	state := m.graphState
	state.Smooth = g.motion
	scene := m.graphScene(state, size)
	if !g.motion {
		stillScene(&scene)
	}
	key := graphview.HashScene(scene, size)
	// Pixel labels are cached with their terminal colors. Rebuild them if the
	// runtime upgrades or changes its output profile.
	key[0] ^= byte(g.profile)
	if g.ready && g.lines != nil && g.key == key && g.size == size {
		return nil
	}
	g.rendering = true
	g.key = key
	return m.graphRenderCmd(scene, size, key)
}

// graphRenderCmd rasterizes and encodes one frame off the update loop.
func (m Model) graphRenderCmd(scene graphview.Scene, size graphview.Size, key [32]byte) tea.Cmd {
	g := m.graphPixel
	metrics := graphview.PixelMetrics{CellWidth: g.cellW, CellHeight: g.cellH}
	shown := g.shown
	next := g.imageID
	if shown == g.imageID {
		next = g.imageID + 1
	}
	previousHash, previousHave := g.hash, g.have
	previousTransmit := g.transmit
	cols, rows := size.Width, size.Height
	return func() tea.Msg {
		img := rasterizeFrame(scene, size, metrics)
		if img == nil {
			return graphFrameMsg{id: shown, key: key, size: size, hash: previousHash, have: previousHave}
		}
		hash := graphview.HashImage(img)
		labels, hits := graphview.PixelLayout(scene, size, metrics)
		if previousHave && hash == previousHash && shown != 0 {
			lines := graphview.PlaceholderLines(labels, size, shown, graphview.StyleRole)
			return graphFrameMsg{id: shown, key: key, size: size, lines: lines, hits: hits, hash: hash, have: true, transmit: previousTransmit}
		}
		// Send the frame under the other id, then delete the one on screen:
		// the placeholders switch to the new id in the same write.
		transmit := graphview.TransmitImage(img, next, cols, rows)
		if shown != 0 {
			transmit += graphview.DeleteImage(shown)
		}
		lines := graphview.PlaceholderLines(labels, size, next, graphview.StyleRole)
		return graphFrameMsg{id: next, key: key, size: size, lines: lines, hits: hits, hash: hash, have: true, transmit: transmit}
	}
}

func (m Model) graphSize() (int, int) {
	w, h := max(m.width, 40), max(m.height, 10)
	box := m.ctoBoxHeight(graphTab)
	footer := m.graphFooterHeight()

	content := max(h-1-box, 1)
	return max(w-4, 1), max(content-1-footer, 1)
}

// Reserve the same space for every selection to keep the canvas stable.
func (m Model) graphOwnerCount() int {
	if m.height <= 24 {
		return 2
	}
	return 3
}
func (m Model) graphFooterHeight() int {
	if m.height <= 24 {
		return 3 + m.graphOwnerCount()
	}
	return 4 + m.graphOwnerCount()
}
func (m Model) graphOwnerLines(w int) []string {
	n := m.graphOwnerCount()
	lines := make([]string, n)
	if m.graphState.Graph == nil {
		return lines
	}
	for i, line := range m.graphDeliveryOwners(m.graphState.Graph.Selected()) {
		if i < n {
			lines[i] = line
		} else {
			lines[n-1] += "; " + line
		}
	}
	for i := range lines {
		lines[i] = clip(lines[i], max(w-2, 0))
	}
	return lines
}

// startGraph opens the network and refreshes its data without losing the selection.
func (m Model) startGraph() (tea.Model, tea.Cmd) {
	m.tab, m.mapOn, m.focus = graphTab, false, focusSessions
	m.mp.finding = false
	m.uiFilter = ""
	m.smoothCamera()
	m.graphGeneration++
	commands := []tea.Cmd{graphTick(m.graphGeneration)}
	if !m.tl.loaded && !m.tl.loading {
		model, cmd := m.loadTools()
		m = model.(Model)
		commands = append(commands, cmd)
	}
	if !m.graphTalkLoading {
		m.graphTalkLoading = true
		m.graphTalkPending = nil
		commands = append(commands, m.graphTalkCmd())
	}
	if !m.mp.loading {
		m.mp.loading = true
		commands = append(commands, m.mapCmd())
	}
	return m, tea.Batch(commands...)
}

func (m Model) graphKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.mp.finding {
		switch keyAction(contextGraphFind, k.String()) {
		case "clear or back":
			if m.mp.findQuery != "" {
				m.mp.findQuery = ""
			} else {
				m.mp.finding = false
			}
		case "select match":
			if nodes := m.mapProjectNodes(); len(nodes) > 0 {
				m.graphState, _ = graphview.Update(m.graphState, graphview.SelectNode{Name: nodes[0].Name})
			}
			m.mp.finding, m.mp.findQuery = false, ""
		case "delete text":
			r := []rune(m.mp.findQuery)
			if len(r) > 0 {
				m.mp.findQuery = string(r[:len(r)-1])
			}
		default:
			if k.Text != "" {
				m.mp.findQuery += k.Text
			}
		}
		return m, nil
	}
	if k.String() == "tab" && m.teamFilter != "" {
		return m.cycleTeamFilter()
	}
	if m.mp.teamInside && (k.String() == "pgup" || k.String() == "pgdown") {
		if k.String() == "pgup" {
			m.mp.teamCardScroll = max(0, m.mp.teamCardScroll-8)
		} else {
			m.mp.teamCardScroll += 8
		}
		return m, nil
	}
	key := keyAction(contextGraph, k.String())
	if key == "help" {
		m.openHelp()
		return m, nil
	}
	if key == actQuit {
		m.quitting = true
		return m, tea.Quit
	}
	if m.graphState.Graph == nil {
		return m, nil
	}
	var action graphview.Action
	switch key {
	case "edit charter", "global charter":
		n, ok := m.mapNode(m.mp.selected)
		if key == "global charter" {
			n = protocol.Node{Ref: &protocol.GraphRef{Kind: "global"}}
			ok = true
		}
		if ok {
			m.startCharterForm(n)
			return m, m.charterForm.input.Focus()
		}
		return m, nil
	case "focus":
		if m.teamFilter != "" && m.mp.focusNode == "" && !m.mp.teamInside && !m.mp.outside[m.graphState.Graph.Selected()] {
			m.mp.teamInside = true
			m.applyTeamView(true)
			return m, nil
		}
		return m.graphFocus()
	case "back":
		if m.mp.teamInside {
			m.mp.teamInside = false
			m.applyTeamView(true)
			return m, nil
		}
		return m.graphBack()
	case "dormant":
		m.mp.showDormant = !m.mp.showDormant
		m.applyTeamView(true)
		return m, nil
	case "remove dormant":
		return m.startGraphRemoval(false)
	case "remove all dormant":
		return m.startGraphRemoval(true)
	case "show all traffic":
		action = graphview.ToggleTalk{}
	case "find":
		m.mp.finding, m.mp.findQuery = true, ""
	case "zoom in":
		action = graphview.ZoomIn{}
	case "zoom out":
		action = graphview.ZoomOut{}
	case "select left", "select right", "select up", "select down":
		w, h := m.graphSize()
		d := graphview.SelectDirection{Viewport: graphview.Viewport{Width: float64(w), Height: float64(h)}}
		switch key {
		case "select left":
			d.DX = -1
		case "select right":
			d.DX = 1
		case "select up":
			d.DY = -1
		case "select down":
			d.DY = 1
		}
		scene := m.graphScene(m.graphState, graphview.Size{Width: w, Height: h})
		if scene.TeamCanvas || len(scene.Groups) > 0 || len(m.mp.dormant) > 0 {
			current := m.graphState.Graph.Selected()
			var from graphview.Point
			for _, n := range scene.Nodes {
				if n.Name == current || n.Selected {
					from = n.Position
				}
			}
			best := ""
			score := 1e30
			for _, n := range scene.Nodes {
				if n.Name == current {
					continue
				}
				dx, dy := (n.Position.X-from.X)*float64(w), (n.Position.Y-from.Y)*float64(h)
				forward := dx*d.DX + dy*d.DY
				if forward <= 0 {
					continue
				}
				cross := dx*d.DY - dy*d.DX
				s := forward + cross*cross/forward*2
				if s < score {
					score = s
					best = n.Name
				}
			}
			if best != "" {
				action = graphview.SelectNode{Name: best}
			}
		} else {
			action = d
		}
	case "pan left":
		action = graphview.Pan{DX: -4}
	case "pan right":
		action = graphview.Pan{DX: 4}
	case "pan up":
		action = graphview.Pan{DY: -2}
	case "pan down":
		action = graphview.Pan{DY: 2}
	case "previous link":
		w, h := m.graphSize()
		action = graphview.SelectPrev{Viewport: graphview.Viewport{Width: float64(w), Height: float64(h)}}
	case "next link":
		w, h := m.graphSize()
		action = graphview.SelectNext{Viewport: graphview.Viewport{Width: float64(w), Height: float64(h)}}
	case "delivery":
		name := m.graphState.Graph.Selected()
		if node, ok := m.mapNode(name); ok && node.Kind == "component" {
			m.tab, m.delivery.project, m.delivery.row, m.delivery.detail = deliveryTab, name, 0, false
			return m.startDeliveryLoad()
		}
	case "new session":
		name := m.graphState.Graph.Selected()
		if node, ok := m.mapNode(name); ok && node.Kind == "component" {
			m.mp.selected = name
			return m.startSessionFormFromNode()
		}
	case "open card":
		name := m.graphState.Graph.Selected()
		if m.teamFilter == "" {
			if team := m.graphTeamOf(name); team != "" && team != name {
				m.graphState, _ = graphview.Update(m.graphState, graphview.SelectNode{Name: team})
				return m.graphFocus()
			}
		}
		if m.teamFilter != "" && m.mp.teamInside {
			if n, ok := m.mapNode(name); ok && n.Ref != nil && n.Ref.Kind == "team" {
				m.setTeamFilter(n.Ref.ID)
				return m, nil
			}
			if name == unassignedTeam {
				m.setTeamFilter(unassignedTeam)
				return m, nil
			}
		}
		if name == dormantChip {
			m.mp.showDormant = !m.mp.showDormant
			m.applyTeamView(true)
			return m, nil
		}
		if name == unassignedTeam {
			return m.graphFocus()
		}
		if n, ok := m.mapNode(name); ok && n.Ref != nil && n.Ref.Kind == "team" {
			return m.graphFocus()
		}
		action = graphview.OpenSelected{}
	case "next team":
		return m.cycleTeamFilter()
	case "manage teams":
		return m.startTeamMenu()
	case "refresh":
		return m.startGraph()
	}
	if action != nil {
		m.smoothCamera()
		m.graphState, _ = graphview.Update(m.graphState, graphview.Tick{Now: m.now()})
		var effect graphview.Effect
		m.graphState, effect = graphview.Update(m.graphState, action)
		if effect.Open {
			m.mp.selected = effect.OpenCard
			m.mapOn = true
			return m, nil
		}
	}
	return m, nil
}

func (m Model) graphView(w, h int) []string {
	out := []string{m.titleLine(w)}
	if m.graphState.Graph == nil {
		if m.mp.err != "" {
			for _, line := range wrap("! could not read the graph: "+m.mp.err, max(w-2, 8)) {
				out = append(out, "  "+sBad.Render(line))
			}
			return out
		}
		return append(out, sDim.Render("  reading the graph..."))
	}
	footer := m.graphFooterHeight()

	gw, gh := max(w-4, 1), max(h-1-footer, 1)
	smooth := m.graphPixel.enabled() && m.graphPixel.motion
	m.graphState.Smooth = smooth
	state, _ := graphview.Update(m.graphState, graphview.Tick{Now: m.now()})
	state.Smooth = smooth
	size := graphview.Size{Width: gw, Height: gh}
	scene := m.graphScene(state, size)
	renderer := graphview.Braille{Styled: true}
	if lines, ok := m.pixelLines(gw, gh); ok {
		for _, line := range lines {
			out = append(out, "  "+line)
		}
	} else {
		for _, line := range renderer.Render(scene, size).Lines {
			out = append(out, "  "+line)
		}
	}
	if m.height > 24 {
		feed := []string{}
		for _, msg := range scene.Feed {
			from := m.graphName(msg.From)
			if msg.FromOwner {
				from = "you"
			}
			feed = append(feed, fmt.Sprintf("%s → %s %s", from, m.graphName(msg.To), sanitize(msg.Kind)))
		}
		if len(feed) == 0 {
			feed = append(feed, "no messages yet")
		}
		out = append(out, sDim.Render(clip("  "+strings.Join(feed, "  ·  "), w)))
		out = append(out, sDim.Render(graphLegend(w)))
	} else {
		out = append(out, sDim.Render(graphLegendCompact(w)))
	}
	out = append(out, graphStatusLine(w, scene))
	if m.mp.removal != nil {
		questions := wrap(m.mp.removal.question, max(w-4, 1))
		for i := 0; i < m.graphOwnerCount(); i++ {
			line := ""
			if i < len(questions) {
				line = "  " + questions[i]
			}
			out = append(out, clip(line, w))
		}
		out = append(out, sDim.Render("  "+confirmHint))
	} else {
		for _, line := range m.graphOwnerLines(w) {
			out = append(out, sDim.Render(clip("  "+line, w)))
		}
		out = append(out, sDim.Render(graphHintLine(m.teamFilterName(), w)))
	}

	return out
}

// pixelLines returns the last finished placeholder rows. It never rasterizes
// or encodes; a frame in flight is prepared by graphRenderCmd. Without a frame
// for this size the caller falls back to Braille.
func (m Model) pixelLines(gw, gh int) ([]string, bool) {
	g := m.graphPixel
	if !g.enabled() || g.cellW <= 0 || g.cellH <= 0 {
		return nil, false
	}
	size := graphview.Size{Width: gw, Height: gh}
	if !g.ready || g.lines == nil || g.size != size {
		return nil, false
	}
	if g.transmit == "" {
		return g.lines, true
	}
	// The transmit stays on the first row until the next frame replaces it.
	// Bubble Tea flushes only the latest View at its frame rate, so a transmit
	// sent in one View and cleared in the next can be dropped, leaving the
	// placeholders pointing at an image the terminal never got (a blank
	// flash). The renderer skips unchanged rows, so it is still written once.
	frame := append([]string(nil), g.lines...)
	frame[0] = g.transmit + frame[0]
	return frame, true
}

// graphStatusLine names the selected node and its place in the network.
func graphStatusLine(w int, scene graphview.Scene) string {
	for _, n := range scene.Nodes {
		if !n.Selected {
			continue
		}
		name := n.Label
		if name == "" {
			name = n.Name
		}
		kind := n.Kind
		if kind == "" {
			kind = "node"
		}
		edges := n.Card.Incoming + n.Card.Outgoing
		prefix := "  selected: "
		line := prefix + ansi.Truncate(name, max(w-ansi.StringWidth(prefix), 1), "…")
		extras := []string{edgeCount(edges)}
		if n.LabelSuffix == "" {
			extras = append([]string{" · " + kind}, extras...)
		}
		for _, extra := range extras {
			if ansi.StringWidth(line+extra) <= w {
				line += extra
			}
		}
		return graphSelectionStyle.Render(line)
	}
	return ""
}

// graphTalkSource is optional for compatibility with older sources.
type graphTalkSource interface {
	GraphTalk(context.Context) (protocol.GraphTalkResult, error)
}
type graphTalkMsg struct {
	result protocol.GraphTalkResult
	at     time.Time
	err    error
}

func (m Model) graphTalkCmd() tea.Cmd {
	src, now := m.src, m.now
	return func() tea.Msg {
		at := now()
		s, ok := src.(graphTalkSource)
		if !ok {
			return graphTalkMsg{at: at, err: ErrFeedUnavailable}
		}
		ctx, cancel := context.WithTimeout(context.Background(), Timeout)
		defer cancel()
		result, err := s.GraphTalk(ctx)
		return graphTalkMsg{result: result, at: at, err: err}
	}
}

// graphHintLine drops whole table hints, starting with the least important.
// Focus, dormant and back stay visible before less-used navigation.
func graphHintLine(team string, w int) string {
	if ansi.StringWidth(team) > 24 {
		team = ansi.Truncate(team, 24, "…")
	}
	teamHint := hint{key: "t", label: "team: " + team, prio: 1}
	direction := bindingPairHint(contextGraph, "select left", "select right")
	direction.label, direction.prio = "select", 2
	hs := []hint{{key: "f", label: "focus/zoom", prio: 0}, {key: "d", label: "dormant", prio: 0}, {key: "esc", label: "back", prio: 0}, {key: "x/X", label: "remove", prio: 1}, teamHint, direction, {key: "?", label: "help", prio: -1}}

	text := func() string {
		parts := make([]string, 0, len(hs))
		for _, h := range hs {
			parts = append(parts, h.key+" "+h.label)
		}
		if len(parts) == 0 {
			return ""
		}
		return "  " + strings.Join(parts, "  ")
	}
	for ansi.StringWidth(text()) > w && len(hs) > 0 {
		worst := 0
		for i, h := range hs {
			if h.prio >= hs[worst].prio {
				worst = i
			}
		}
		hs = append(hs[:worst:worst], hs[worst+1:]...)
	}
	return text()
}

// graphLegend drops whole items from the end when the viewport is narrow.
func graphLegend(w int) string {
	items := []string{"needs you: highlighted", "busy: bright", "idle: normal", "offline: dim"}
	for len(items) > 0 {
		line := "  " + strings.Join(items, " · ")
		if ansi.StringWidth(line) <= w {
			return line
		}
		items = items[:len(items)-1]
	}
	return ""
}

// graphLegendCompact is the one row legend on a 24 row terminal.
func graphLegendCompact(w int) string {
	items := []string{"needs you: highlighted", "busy: bright", "offline: dim"}
	for len(items) > 0 {
		line := "  " + strings.Join(items, " · ")
		if ansi.StringWidth(line) <= w {
			return line
		}
		items = items[:len(items)-1]
	}
	return ""
}

// edgeCount names a number of edges: "1 edge", "3 edges".
func edgeCount(n int) string {
	if n == 1 {
		return " · 1 edge"
	}
	return fmt.Sprintf(" · %d edges", n)
}

// graphMouse handles cell coordinates relative to the graph body. Pixel hit
// testing uses the actual node radius; Braille label hits remain available.
func (m Model) graphMouse(msg tea.MouseMsg) Model {
	if !m.graphMouseActive() || m.graphState.Graph == nil {
		return m
	}
	mouse := msg.Mouse()
	w, h := m.graphSize()
	x, y := mouse.X-2, mouse.Y-1
	inside := x >= 0 && y >= 0 && x < w && y < h
	if _, ok := msg.(tea.MouseReleaseMsg); ok {
		m.graphDragging = false
		return m
	}
	if _, ok := msg.(tea.MouseMotionMsg); ok && m.graphDragging {
		m.graphState, _ = graphview.Update(m.graphState, graphview.DragPan{DX: float64(mouse.X - m.graphDragX), DY: float64(mouse.Y - m.graphDragY)})
		m.graphDragX, m.graphDragY = mouse.X, mouse.Y
		return m
	}
	if !inside {
		return m
	}
	if mouse.Button == tea.MouseWheelUp || mouse.Button == tea.MouseWheelDown {
		factor := 1.25
		if mouse.Button == tea.MouseWheelDown {
			factor = 1 / factor
		}
		m.smoothCamera()
		m.graphState, _ = graphview.Update(m.graphState, graphview.Tick{Now: m.now()})
		m.graphState, _ = graphview.Update(m.graphState, graphview.ZoomAt{X: float64(x) + .5, Y: float64(y) + .5, Viewport: graphview.Viewport{Width: float64(w), Height: float64(h)}, Factor: factor})
		return m
	}
	if _, ok := msg.(tea.MouseClickMsg); !ok {
		return m
	}
	if mouse.Button != tea.MouseLeft {
		return m
	}
	size := graphview.Size{Width: w, Height: h}
	scene := m.graphScene(m.graphState, size)
	hit := ""
	pixelReady := m.graphPixel.enabled() && m.graphPixel.ready && m.graphPixel.size == size
	if pixelReady {
		hit = graphview.PixelHit(scene, size, graphview.PixelMetrics{CellWidth: m.graphPixel.cellW, CellHeight: m.graphPixel.cellH}, graphview.Cell{X: x, Y: y})
		if hit == "" {
			hit = m.graphPixel.hits[graphview.Cell{X: x, Y: y}]
		}
	} else {
		hit = (graphview.Braille{}).Render(scene, size).Hits[graphview.Cell{X: x, Y: y}]
	}
	if hit == "" {
		m.graphDragging = true
		m.graphDragX, m.graphDragY = mouse.X, mouse.Y
		m.graphLastNode = ""
		return m
	}
	now := m.now()
	double := hit == m.graphLastNode && !m.graphLastClick.IsZero() && now.Sub(m.graphLastClick) >= 0 && now.Sub(m.graphLastClick) <= 400*time.Millisecond
	m.graphLastNode, m.graphLastClick = hit, now
	m.smoothCamera()
	m.graphState, _ = graphview.Update(m.graphState, graphview.Tick{Now: now})
	m.graphState, _ = graphview.Update(m.graphState, graphview.SelectNode{Name: hit})
	if double {
		m.graphState, _ = graphview.Update(m.graphState, graphview.CenterNode{Name: hit})
		m.graphLastNode = ""
	}
	return m
}

// graphDeliveryOwners is shared by the selected project and its details card.
func (m Model) graphDeliveryOwners(project string) []string {
	node, ok := m.mapNode(project)
	if !ok || node.Kind != "component" {
		return nil
	}
	owner := func(step string) string {
		source := ""
		for _, edge := range m.mp.edges {
			if edge.To == project && edge.Kind == protocol.DeliveryStepEdge(step) {
				source = edge.From
				if edge.FromRef != nil {
					source = edge.FromRef.ID
				}
			}
		}
		if source == "" {
			source = "/delivery/caller"
		}
		assignment := protocol.DeliveryOwnerFromSource(source)
		if assignment.Kind != protocol.OwnerMerger {
			name := assignment.Node
			if assignment.Kind == protocol.OwnerCaller {
				return "caller"
			}
			if name != "" {
				var sessions []string
				key := name
				if node.Ref != nil {
					key = graphRefKey(&protocol.GraphRef{Kind: "agent", ID: name})
				}
				if n, ok := m.mapNode(key); ok {
					for _, session := range n.Sessions {
						status := "offline"
						if a, ok := m.agentByID(session.ID); ok && a.Online {
							status = "online"
						}
						sessions = append(sessions, sanitize(firstNonEmpty(session.Name, session.ID))+" ("+status+")")
					}
				}
				sort.Strings(sessions)
				if len(sessions) > 0 {
					return strings.Join(sessions, ", ")
				}
				return m.graphName(key) + " (offline)"
			}
			return assignment.Kind
		}
		var mergers []string
		for _, a := range m.snap.Agents {
			if protocol.SessionType(a.Harness, a.Worker, a.Type) != "merger" || firstNonEmpty(a.Repo.Project, a.Profile.Project) != graphRecordID(node) {
				continue
			}
			status := "offline, tasks wait"
			if a.Online {
				status = "online"
			}
			name := firstNonEmpty(a.Profile.Name, m.graphName(project)+" merger")
			mergers = append(mergers, sanitize(name)+" ("+status+")")
		}
		sort.Strings(mergers)
		if len(mergers) == 0 {
			return m.graphName(project) + " merger (offline, tasks wait)"
		}
		return strings.Join(mergers, ", ")
	}
	steps := []string{protocol.StepAccept, protocol.StepTest, protocol.StepReview, protocol.StepMerge, protocol.StepRelease, protocol.StepVerify}
	var owners, labels []string
	for _, step := range steps {
		o := owner(step)
		found := -1
		for i := range owners {
			if owners[i] == o {
				found = i
				break
			}
		}
		if found < 0 {
			owners = append(owners, o)
			labels = append(labels, step)
		} else {
			labels[found] += ", " + step
		}
	}
	for i := range labels {
		labels[i] += ": " + owners[i]
	}
	return labels
}

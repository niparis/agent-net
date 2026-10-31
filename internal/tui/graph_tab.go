package tui

import (
	"context"
	"fmt"
	"github.com/niparis/agent-net/internal/protocol"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/tui/graphview"
)

type graphTickMsg struct{ generation int }

func graphTick(generation int) tea.Cmd {
	return tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg { return graphTickMsg{generation} })
}

func (m Model) graphSize() (int, int) {
	w, h := max(m.width, 40), max(m.height, 10)
	box := min(max((h-1)/3, 6), max(h-4, 1))
	return max(w-4, 1), max(h-1-box-4, 1)
}
func (m Model) graphKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.graphHelp {
		if k.String() == "?" || k.String() == "esc" {
			m.graphHelp = false
		}
		return m, nil
	}
	if k.String() == "q" {
		m.quitting = true
		return m, tea.Quit
	}
	if m.graphState.Graph == nil {
		if k.String() == "?" {
			m.graphHelp = true
		}
		return m, nil
	}
	var action graphview.Action
	switch k.String() {
	case "+", "=":
		action = graphview.ZoomIn{}
	case "-":
		action = graphview.ZoomOut{}
	case "left", "right", "up", "down":
		w, h := m.graphSize()
		d := graphview.SelectDirection{Viewport: graphview.Viewport{Width: float64(w), Height: float64(h)}}
		switch k.String() {
		case "left":
			d.DX = -1
		case "right":
			d.DX = 1
		case "up":
			d.DY = -1
		case "down":
			d.DY = 1
		}
		action = d
	case "shift+left", "h":
		action = graphview.Pan{DX: -4}
	case "shift+right", "l":
		action = graphview.Pan{DX: 4}
	case "shift+up", "k":
		action = graphview.Pan{DY: -2}
	case "shift+down", "j":
		action = graphview.Pan{DY: 2}
	case "0":
		w, h := m.graphSize()
		action = graphview.Fit{Viewport: graphview.Viewport{Width: float64(w), Height: float64(h)}}
	case "[":
		w, h := m.graphSize()
		action = graphview.SelectPrev{Viewport: graphview.Viewport{Width: float64(w), Height: float64(h)}}
	case "]":
		w, h := m.graphSize()
		action = graphview.SelectNext{Viewport: graphview.Viewport{Width: float64(w), Height: float64(h)}}
	case "t":
		action = graphview.ToggleTalk{}
	case "o":
		action = graphview.ToggleOrbit{}
	case " ":
		action = graphview.TogglePause{}
	case "?":
		m.graphHelp = true
	case "enter":
		action = graphview.OpenSelected{}
	}
	if action != nil {
		m.graphState, _ = graphview.Update(m.graphState, graphview.Tick{Now: m.now()})
		var effect graphview.Effect
		m.graphState, effect = graphview.Update(m.graphState, action)
		if effect.Open {
			m.mp.selected = effect.OpenMap
			m.mp.overview = false
			return m.startMap()
		}
	}
	return m, nil
}

func (m Model) graphView(w, h int) []string {
	out := []string{m.titleLine(w)}
	if m.graphHelp {
		return append(out, sDim.Render("  + - zoom  arrows select  hjkl/shift+arrows pan  0 fit  [ ] neighbours"), sDim.Render("  o orbit  t talk  enter Map  space pause  ? close  tab CTO  q quit"))
	}
	if m.graphState.Graph == nil {
		return append(out, sDim.Render("  reading the graph..."))
	}
	gw, gh := max(w-4, 1), max(h-4, 1)
	state, _ := graphview.Update(m.graphState, graphview.Tick{Now: m.now()})
	scene := graphview.BuildScene(state, graphview.Viewport{Width: float64(gw), Height: float64(gh)})
	for i := range scene.Nodes {
		if scene.Nodes[i].Name == protocol.GraphVoiceOwner {
			continue
		}
		scene.Nodes[i].Label = m.projectName(scene.Nodes[i].Name)
		scene.Nodes[i].Card.Name = m.projectName(scene.Nodes[i].Name)
	}
	renderer := graphview.Braille{Styled: true}
	for _, line := range renderer.Render(scene, graphview.Size{Width: gw, Height: gh}).Lines {
		out = append(out, "  "+line)
	}
	feed := []string{}
	for _, msg := range scene.Feed {
		from := m.projectName(msg.From)
		if msg.FromOwner {
			from = "you"
		}
		feed = append(feed, fmt.Sprintf("%s → %s %s", from, m.projectName(msg.To), sanitize(msg.Kind)))
	}
	if len(feed) == 0 {
		feed = append(feed, "no messages yet")
	}
	out = append(out, sDim.Render(clip("  "+strings.Join(feed, "  ·  "), w)))
	out = append(out, sDim.Render(graphLegend(w)))
	out = append(out, sDim.Render(graphHintLine(w, scene.Paused)))
	return out
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

// graphHintLine drops whole hints, starting with the least important. It holds
// the keys used most on the graph; the full list is in the graph help panel.
func graphHintLine(w int, paused bool) string {
	hs := []hint{{"arrows", "select", 1}, {"+ -", "zoom", 1}, {"0", "fit", 2}, {"o", "orbit", 2}, {"t", "talk", 1}, {"enter", "Map", 2}, {"?", "help", 0}}
	if paused {
		// The paused marker takes the place of the least used hint, so the
		// line never grows past seven.
		hs = []hint{{"paused", "·", 0}, {"arrows", "select", 1}, {"+ -", "zoom", 1}, {"0", "fit", 2}, {"t", "talk", 1}, {"enter", "Map", 2}, {"?", "help", 0}}
	}
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
	items := []string{"● charter", "○ no charter", "gold: your words", "pink: talked, no rule set"}
	for len(items) > 0 {
		line := "  " + strings.Join(items, "  ·  ")
		if ansi.StringWidth(line) <= w {
			return line
		}
		items = items[:len(items)-1]
	}
	return ""
}

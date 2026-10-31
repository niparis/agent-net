package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
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
	case "left", "h":
		action = graphview.Pan{DX: -4}
	case "right", "l":
		action = graphview.Pan{DX: 4}
	case "up", "k":
		action = graphview.Pan{DY: -2}
	case "down", "j":
		action = graphview.Pan{DY: 2}
	case "0":
		w, h := m.graphSize()
		action = graphview.Fit{Viewport: graphview.Viewport{Width: float64(w), Height: float64(h)}}
	case "[":
		action = graphview.SelectPrev{}
	case "]":
		action = graphview.SelectNext{}
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
		return append(out, sDim.Render("  + - zoom  arrows/hjkl pan  0 fit  [ ] select  o orbit"), sDim.Render("  enter Map  space pause  ? close  tab CTO"))
	}
	if m.graphState.Graph == nil {
		return append(out, sDim.Render("  reading the graph..."))
	}
	gw, gh := max(w-4, 1), max(h-4, 1)
	state, _ := graphview.Update(m.graphState, graphview.Tick{Now: m.now()})
	scene := graphview.BuildScene(state, graphview.Viewport{Width: float64(gw), Height: float64(gh)})
	renderer := graphview.Braille{Styled: true}
	for _, line := range renderer.Render(scene, graphview.Size{Width: gw, Height: gh}).Lines {
		out = append(out, "  "+line)
	}
	feed := []string{}
	for _, msg := range scene.Feed {
		feed = append(feed, fmt.Sprintf("%s → %s %s", sanitize(msg.From), sanitize(msg.To), sanitize(msg.Kind)))
	}
	if len(feed) == 0 {
		feed = append(feed, "no messages yet")
	}
	out = append(out, sDim.Render(clip("  "+strings.Join(feed, "  ·  "), w)))
	hint := "  + - zoom  hjkl pan  0 fit  [ ] select  o orbit  enter Map  space pause  ? keys"
	if scene.Paused {
		hint = "  paused · " + strings.TrimSpace(hint)
	}
	out = append(out, sDim.Render(clip(hint, w)))
	return out
}

package tui

import (
	"github.com/charmbracelet/glamour"
	ga "github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"strings"
)

func (m Model) ctoView(w, h int) []string {
	c := m.cto
	title := "CTO"
	if c.harness != "" {
		title += " · " + sanitize(c.harness)
	}
	if c.model != "" {
		title += " · " + sDim.Render(sanitize(c.model))
	}
	borderColor := roleFaint
	if m.focus == focusCTO {
		borderColor = roleCTO
	}

	w = max(w-4, 1)
	h = max(h-2, 1)
	out := []string{}
	if f := c.settingsForm; f != nil {
		marker := ">"
		other := " "
		if f.modelFocus {
			marker, other = " ", ">"
		}
		f.input.Width = max(w-12, 1)
		out = append(out, clip(marker+" Harness: < "+f.harness+" >", w), clip(other+" Model:   "+f.input.View(), w))
		candidates := m.ctoModelCandidates(f.harness, f.input.Value())
		if len(candidates) > 3 {
			candidates = candidates[:3]
		}
		if len(candidates) > 0 {
			out = append(out, clip("  Suggestions: "+strings.Join(candidates, ", "), w))
		}
		out = append(out, clip("  up/down select  left/right harness  tab complete  enter save  esc cancel", w))
		for len(out) < h {
			out = append(out, "")
		}
		return panelWithBorder(title, out[:min(len(out), h)], w+4, roleCTO, borderColor)
	}
	var lines []string
	for _, v := range c.messages {
		lines = append(lines, c.renderMessage(v, w)...)
	}
	if c.err != "" {
		duplicate := false
		pollError := "error: " + ctoErrorHint(sanitize(c.err))
		for _, e := range c.sendErrors {
			if e.text == pollError {
				duplicate = true
				break
			}
		}
		if !duplicate {
			lines = append(lines, wrapCTOLine("error: "+c.err, w)...)
		}
	}
	if len(lines) == 0 {
		hint := "write to the CTO: it can set charters, start workers, answer what the network is doing"
		if w < 84 {
			hint = "write to the CTO: set charters, start workers, ask about the network"
		}
		lines = append(lines, sDim.Render(hint))
	}
	if c.confirming {
		lines = append(lines, "Start a new conversation? y/n")
	}
	rows := max(h-1, 0)
	c.pageRows = max(rows-1, 1)
	c.scroll = min(c.scroll, max(len(lines)-rows, 0))
	end := len(lines) - c.scroll
	start := max(end-rows, 0)
	if start > 0 && rows > 1 {
		start++
		lines = append([]string{sDim.Render("↑ more")}, lines[start:end]...)
	} else {
		lines = lines[start:end]
	}
	for len(out) < h-1-len(lines) {
		out = append(out, "")
	}
	for _, v := range lines {
		out = append(out, clip(v, w))
	}
	c.input.Width = max(w-4, 1)
	input := c.input.View()
	if !c.editing {
		input = "> write to the CTO..."
	}
	out = append(out, clip(input, w))
	return panelWithBorder(title, out, w+4, roleCTO, borderColor)
}

// wrapCTOLine indents continuation rows while keeping their text in the box.
func wrapCTOLine(text string, width int) []string {
	var parts []string
	for _, line := range strings.Split(sanitizeBody(text), "\n") {
		parts = append(parts, wrap(line, max(width-2, 8))...)
	}
	for i := 1; i < len(parts); i++ {
		parts[i] = "  " + parts[i]
	}
	return parts
}

type ctoRenderKey struct {
	text    string
	width   int
	dark    bool
	profile termenv.Profile
}

func (c *ctoState) renderMessage(text string, width int) []string {
	if strings.HasPrefix(text, "switched to ") {
		lines := wrapCTOLine(text, width)
		for i := range lines {
			lines[i] = sDim.Render(lines[i])
		}
		return lines
	}
	if !strings.HasPrefix(text, "cto: ") {
		return wrapCTOLine(text, width)
	}
	key := ctoRenderKey{text, width, lipgloss.HasDarkBackground(), lipgloss.ColorProfile()}
	if lines, ok := c.renderCache[key]; ok {
		return lines
	}
	style := styles.DarkStyleConfig
	if !key.dark {
		style = styles.LightStyleConfig
	}
	color := func(role lipgloss.TerminalColor) *string {
		adaptive := role.(lipgloss.AdaptiveColor)
		value := adaptive.Light
		if key.dark {
			value = adaptive.Dark
		}
		return &value
	}
	zero := uint(0)
	yes := true
	style.Document = ga.StyleBlock{StylePrimitive: ga.StylePrimitive{Color: color(roleText)}, Margin: &zero}
	style.Heading.Color, style.Heading.Bold = color(roleText), &yes
	style.Code.Color, style.CodeBlock.Color = color(roleAccent), color(roleAccent)
	style.Link.Color, style.LinkText.Color = color(roleAccent), color(roleAccent)
	style.BlockQuote.Color = color(roleDim)
	renderer, err := glamour.NewTermRenderer(glamour.WithStyles(style), glamour.WithWordWrap(max(width, 1)), glamour.WithColorProfile(key.profile), glamour.WithPreservedNewLines())
	if err != nil {
		return wrapCTOLine(text, width)
	}
	rendered, err := renderer.Render(sanitizeBody(strings.TrimPrefix(text, "cto: ")))
	if err != nil {
		return wrapCTOLine(text, width)
	}
	lines := strings.Split(strings.Trim(rendered, "\n"), "\n")
	label := lipgloss.NewStyle().Foreground(roleCTO).Render("cto:")
	lines = append([]string{label}, lines...)
	if c.renderCache == nil {
		c.renderCache = make(map[ctoRenderKey][]string)
	}
	if len(c.renderCache) > 512 {
		clear(c.renderCache)
	}
	c.renderCache[key] = lines
	return lines
}

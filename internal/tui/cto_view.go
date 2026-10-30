package tui

import "strings"

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

	w = max(w-2, 1)
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
		return panelWithBorder(title, out[:min(len(out), h)], w+2, roleCTO, borderColor)
	}
	var lines []string
	for _, v := range c.messages {
		lines = append(lines, wrap(sanitize(v), max(w-2, 8))...)
	}
	if c.err != "" {
		lines = append(lines, "error: "+c.err)
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
	if len(lines) > rows {
		lines = lines[len(lines)-rows:]
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
	return panelWithBorder(title, out, w+2, roleCTO, borderColor)
}

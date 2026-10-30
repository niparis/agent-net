package tui

func (m Model) ctoView(w, h int) []string {
	c := m.cto
	title := "CTO"
	if c.model != "" {
		if c.harness != "" {
			title += " (" + c.harness + " · " + c.model + ")"
		} else {
			title += " (" + c.model + ")"
		}
	}
	w = max(w-2, 1)
	h = max(h-2, 1)
	out := []string{}
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
	return panel(title, out, w+2, roleCTO)
}

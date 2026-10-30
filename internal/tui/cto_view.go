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
	out := []string{section(title, w)}
	var lines []string
	for _, v := range c.messages {
		lines = append(lines, wrap(sanitize(v), max(w-2, 8))...)
	}
	if c.err != "" {
		lines = append(lines, "! "+c.err)
	}
	rows := max(h-2, 0)
	if len(lines) > rows {
		lines = lines[len(lines)-rows:]
	}
	for _, v := range lines {
		out = append(out, clip(v, w))
	}
	for len(out) < h-1 {
		out = append(out, "")
	}
	c.input.Width = max(w-2, 1)
	input := c.input.View()
	if !c.editing {
		input = "> press Enter to write"
	}
	out = append(out, clip(input, w))
	return out
}
func (m Model) ctoLayout(w, h int) []string {
	if m.mapOn {
		leftW := (w - 3) / 2
		rightW := w - leftW - 3
		left := m.mapView(leftW, h)
		right := m.ctoView(rightW, h)
		out := make([]string, 0, h)
		for i := 0; i < h; i++ {
			l, r := "", ""
			if i < len(left) {
				l = left[i]
			}
			if i < len(right) {
				r = right[i]
			}
			out = append(out, clip(padStyled(l, leftW)+" │ "+r, w))
		}
		return out
	}
	top := h / 2
	main := m.mainView(w, top)
	if len(main) > top {
		main = main[:top]
	}
	for len(main) < top {
		main = append(main, "")
	}
	return append(main, m.ctoView(w, h-top)...)
}

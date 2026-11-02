package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// toolsView draws skills per agent, the all-tools list or one modal. The list is
// not drawn underneath a modal, so old panel borders cannot bleed through.
func (m Model) toolsView(w, h int) []string {
	out := []string{m.titleLine(w)}
	if notice, ok := m.noticeLine(w); ok {
		out[0] = notice
	}
	if m.tl.modal != nil {
		box := m.modalBox(w, max(h-1, 6))
		out = append(out, box...)
	} else if !m.tl.allTools {
		out = append(out, m.agentToolsView(w, max(h-1, 3))...)
	} else if m.tl.projectsOpen {
		out = append(out, m.projectCostView(w, max(h-1, 3))...)
	} else if w >= 100 {
		left := w * 55 / 100
		a, b := m.toolListView(left, max(h-1, 3)), m.toolDetailView(w-left, max(h-1, 3))
		for i := range a {
			out = append(out, a[i]+b[i])
		}
	} else {
		detail := min(8, max(h/2, 3))
		out = append(out, m.toolListView(w, max(h-1-detail, 3))...)
		out = append(out, m.toolDetailView(w, detail)...)
	}
	for len(out) < h {
		out = append(out, "")
	}
	return out[:h]
}

func (m Model) toolsHints() []hint {
	if md := m.tl.modal; md != nil {
		var out []hint
		for i := 0; i+1 < len(modalKeys(md)); i += 2 {
			out = append(out, hint{modalKeys(md)[i], modalKeys(md)[i+1], 0})
		}
		return out
	}
	if !m.tl.allTools {
		return m.agentToolsHints()
	}
	if m.tl.projectsOpen {
		return []hint{{"j/k", "move", 0}, {"enter", "open", 0}, {"esc", "back", 0}}
	}
	return []hint{
		bindingPairHint(contextTools, actDown, actUp),
		bindingHint(contextTools, "open actions"),
		bindingHint(contextTools, "filter"),
		bindingHint(contextTools, "skills per agent"),
		bindingHint(contextTools, "add"),
		bindingHint(contextTools, "clear filter"),
		bindingHint(contextTools, "help"),
		bindingHint(contextTools, actQuit),
	}
}

func (m Model) toolsHelpLine(w int) (string, bool) {
	if m.focus == focusCTO {
		return "", false
	}
	return strings.Join(m.toolsKeyLines(w), "\n"), true
}

func searchCursor(on bool) string {
	if on {
		return "▏"
	}
	return ""
}

func ago(d interface{ Seconds() float64 }) string {
	s := int(d.Seconds())
	switch {
	case s < 60:
		return fmt.Sprintf("%ds", max(s, 0))
	case s < 3600:
		return fmt.Sprintf("%dm", s/60)
	}
	return fmt.Sprintf("%dh", s/3600)
}

// ---- plan on top ----

func (m Model) modalBox(w, h int) []string {
	md := m.tl.modal
	bw := w
	var body []string
	switch {
	case md.menu != nil:
		body = toolMenuLines(md.menu)
	case md.form != nil:
		body = toolAddLines(md.form)
	case md.picker != nil && !md.loading && !md.done:
		body = pickerLines(md.picker, bw-4, max(h-8, 4))
	case md.choose != nil && len(md.choose.act.Copies) == 2:
		body = compareBoxLines(md.choose, md.choose.act.Copies, bw-4)
	default:
		for i := 0; i < len(md.lines); i++ {
			l := md.lines[i]
			if strings.HasSuffix(l, " differ:") && i+1 < len(md.lines) && strings.HasPrefix(md.lines[i+1], "    ") {
				var items []string
				for i+1 < len(md.lines) && strings.HasPrefix(md.lines[i+1], "    ") {
					i++
					items = append(items, strings.TrimPrefix(md.lines[i], "    "))
				}
				body = append(body, wrapDifferenceItems(l, items, bw-4)...)
			} else {
				body = append(body, wrapStyled(l, bw-4)...)
			}
		}
		if md.loading && len(md.lines) == 0 {
			body = append(body, tsDim.Render("planning..."))
		}
		if md.showPaths && len(md.paths) > 0 {
			title := md.pathsTitle
			if title == "" {
				title = "exact folders"
			}
			body = append(body, "", tsFaint.Bold(true).Render(title))
			for _, p := range md.paths {
				body = append(body, tsDim.Render("  "+sanitize(p)))
			}
		}
	}
	room := max(h-6, 3)
	if len(body) > room {
		start := min(md.scroll, len(body)-room+1)
		end := min(start+room-1, len(body))
		window := append([]string(nil), body[start:end]...)
		window = append(window, tsFaint.Render(fmt.Sprintf("  lines %d-%d of %d · up/down scroll", start+1, end, len(body))))
		body = window
	}
	if toast := m.activeToast(); toast != "" {
		body = append(body, tsWarn.Render(toast))
	}
	if md.choose == nil {
		body = append(body, "")
		foot := modalKeys(md)
		body = append(body, wrapHints(foot, bw-4)...)
	}
	return panelWithBorder(md.title, body, bw, roleAccent, roleAccent)
}

// wrapDifferenceItems wraps between file differences, preserving each item.
func wrapDifferenceItems(header string, items []string, width int) []string {
	line := header
	var out []string
	for _, item := range items {
		sep := ", "
		if line == header {
			sep = " "
		}
		if ansi.StringWidth(line)+len(sep)+ansi.StringWidth(item) <= width {
			line += sep + item
			continue
		}
		out = append(out, line)
		line = "    " + item
		if ansi.StringWidth(line) > width {
			wrapped := wrapStyled(line, width)
			out = append(out, wrapped[:len(wrapped)-1]...)
			line = wrapped[len(wrapped)-1]
		}
	}
	return append(out, line)
}

// wrapStyled keeps continuation lines indented under the text they continue.
func wrapStyled(s string, width int) []string {
	if ansi.StringWidth(s) <= width {
		return []string{s}
	}
	plain := ansi.Strip(s)
	// File locations are one phrase: move the entire phrase to the next line
	// when it cannot fit beside the file name.
	if strings.HasPrefix(s, "    ") {
		if at := strings.LastIndex(s, " only on "); at >= 0 {
			prefix, phrase := s[:at], strings.TrimSpace(s[at:])
			if ansi.StringWidth(phrase)+6 <= width {
				return append(wrapStyled(prefix, width), "      "+phrase)
			}
		}
	}
	indent := len(plain) - len(strings.TrimLeft(plain, " "))
	indent = max(indent, 2)
	indent = min(indent, max(width-1, 0))
	lines := strings.Split(ansi.Wrap(s, max(width-indent, 1), ""), "\n")
	for i := 1; i < len(lines); i++ {
		lines[i] = strings.Repeat(" ", indent) + strings.TrimLeft(lines[i], " ")
	}
	return lines
}

// wrapHints keeps each key and its label together when the footer wraps.
func wrapHints(parts []string, width int) []string {
	var lines []string
	line := ""
	for i := 0; i+1 < len(parts); i += 2 {
		hint := keyHints(parts[i], parts[i+1])
		if ansi.StringWidth(hint) > width-2 {
			continue
		}
		sep := "  "
		if line == "" {
			sep = ""
		}
		if line != "" && ansi.StringWidth(line)+len(sep)+ansi.StringWidth(hint) > width {
			lines = append(lines, line)
			line = "  " + hint
		} else {
			line += sep + hint
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

func pickerLines(p *projPicker, width, rows int) []string {
	shown := p.shown()
	picked := 0
	for _, v := range p.sel {
		if v {
			picked++
		}
	}
	noun := "projects"
	if p.targets {
		noun = "targets"
	}
	head := tsDim.Render(fmt.Sprintf("%d of %d %s shown · %d ticked", len(shown), len(p.all), noun, picked))
	if p.remove {
		head = tsDim.Render(fmt.Sprintf("tick the projects to remove it from · %d of %d shown · %d ticked", len(shown), len(p.all), picked))
	}
	out := []string{head}
	if p.filter != "" || p.filtering {
		out = append(out, tsAcc.Render("/"+sanitize(p.filter)+searchCursor(p.filtering)))
	}
	room := max(rows-len(out)-1, 2)
	start := max(0, min(p.cur-room/2, len(shown)-room))
	for i := start; i < len(shown) && i < start+room; i++ {
		box := "[ ] "
		if p.sel[shown[i]] {
			box = "[x] "
		}
		line := tsAcc.Render(box) + tsText.Render(sanitize(shown[i]))
		if i == p.cur {
			line = tsAccB.Render("▸ ") + line
		} else {
			line = "  " + line
		}
		out = append(out, line)
	}
	if len(shown) == 0 {
		out = append(out, tsFaint.Render("  no project matches"))
	}
	if len(shown) > start+room {
		out = append(out, tsFaint.Render(fmt.Sprintf("  and %d more", len(shown)-start-room)))
	}
	return out
}

// overlayModal draws the box over the screen, near the top.
func overlayModal(screen, box []string, w int) []string {
	y := 2
	if len(box)+y > len(screen) {
		y = max(len(screen)-len(box), 0)
	}
	bw := 0
	for _, l := range box {
		bw = max(bw, ansi.StringWidth(l))
	}
	x := max((w-bw)/2, 0)
	out := append([]string(nil), screen...)
	// The plan floats on a clear band: no pieces of the screen under it show
	// beside it.
	for i, l := range box {
		r := y + i
		if r >= len(out) {
			break
		}
		out[r] = strings.Repeat(" ", x) + l
	}
	return out
}

func modalKeys(md *toolsModal) []string {
	if md.loading {
		return []string{"esc", "cancel"}
	}
	return md.foot
}

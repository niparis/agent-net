package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// noticeLines is the result of the last action, on one or two rows: plain text
// when it worked, "!" and red when it did not.
func (m Model) noticeLines(w int) []string {
	n := m.activeNotice()
	if n == nil {
		return nil
	}
	prefix, style := "  ", sIdle
	if n.err {
		prefix, style = "! ", sBad
	}
	ls := wrap(sanitize(n.text), w-2)
	if len(ls) > 2 {
		ls = append(ls[:1], clip(strings.Join(ls[1:], " "), w-2))
	}
	out := make([]string, 0, 2)
	for i, l := range ls {
		p := prefix
		if i > 0 {
			p = "  "
		}
		out = append(out, style.Render(cell(p+l, w)))
	}
	return out
}

// formView draws the start-a-worker form in the place of the detail pane and
// the feed. rows is the room it has, including its heading.
func (m Model) formView(w, rows int) []string {
	f := m.form
	out := []string{section("Start a worker", w)}
	const lw = 9
	for i := formField(0); i < nFields; i++ {
		marker := "  "
		if i == f.cur {
			marker = "> "
		}
		val := f.in[i].View()
		if i != f.cur {
			val = sanitize(f.in[i].Value())
		}
		line := marker + sBold.Render(cell(fieldLabels[i], lw)) + " " + val
		if note := m.fieldNote(i); note != "" && i != f.cur {
			if room := w - ansi.StringWidth(line) - 3; room > 6 {
				line += "   " + sDim.Render(clip(note, room))
			}
		}
		out = append(out, clip(line, w))
	}
	if f.err != "" {
		out = append(out, sBad.Render(clip("  ! "+f.err, w)))
	} else {
		out = append(out, "")
	}
	// Completions of the field that has the focus.
	room := rows - len(out)
	if f.cur == fTask {
		out = append(out, sDim.Render(clip("  The task is sent to the worker as a message from you. Enter starts it.", w)))
	} else if room > 0 {
		cands := m.candidates()
		switch {
		case len(cands) == 0:
			out = append(out, sDim.Render(clip("  "+m.noCandidates(), w)))
		default:
			show := min(len(cands), max(room-1, 1))
			if show < len(cands) && room > 1 {
				show = min(len(cands), room-1)
			}
			for _, c := range cands[:show] {
				line := "  " + cell(c.value, min(max(w/2-2, 20), 44))
				if c.note != "" {
					line += " " + sDim.Render(c.note)
				}
				out = append(out, clip(line, w))
			}
			if show < len(cands) {
				out = append(out, sDim.Render(fmt.Sprintf("  +%d more (type to narrow, tab completes)", len(cands)-show)))
			}
		}
	}
	for len(out) < rows {
		out = append(out, "")
	}
	return out[:rows]
}

// fieldNote says what the project or model field resolves to.
func (m Model) fieldNote(i formField) string {
	switch i {
	case fProject:
		if m.form.value(fProject) == "" {
			return ""
		}
		_, machine, node := m.resolveProject(m.form.value(fProject))
		if node == nil {
			return "no such project"
		}
		return "on " + machine
	case fModel:
		if m.form.value(fModel) == "" {
			return "harness default"
		}
	}
	return ""
}

func (m Model) noCandidates() string {
	switch m.form.cur {
	case fProject:
		if len(m.projectRows()) == 0 {
			return "No projects are known. Register one with: agent-net projects add <path>"
		}
		return "No project matches. Known: " + strings.Join(firstNames(m.projectRows(), 4), ", ")
	case fHarness:
		return "No worker harness is installed on that machine."
	case fModel:
		if _, _, node := m.resolveProject(m.form.value(fProject)); node == nil {
			return "Pick the project first: its machine lists the models. Free text works too."
		}
		return "No model matches. Leave it empty for the harness default."
	}
	return ""
}

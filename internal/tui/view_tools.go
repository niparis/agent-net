package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/toolsdiff"
)

// toolsView draws the Tools tab into h lines: the tab bar, the line with the
// three screens, the screen, and the plan on top when one is open.
func (m Model) toolsView(w, h int) []string {
	out := []string{m.titleLine(w), m.toolsScreenLine(w)}
	body := max(h-len(out), 3)
	var lines []string
	switch {
	case !m.tl.loaded:
		msg := "reading the tools..."
		style := tsDim
		if m.tl.err != "" {
			msg, style = sanitize(m.tl.err), tsErr
		} else if _, ok := m.toolsSource(); !ok {
			msg, style = "this daemon cannot list tools", tsErr
		}
		lines = panel("Tools", []string{style.Render(msg)}, w, roleAccent)
	case m.tl.screen == toolsDiffs:
		lines = m.diffsView(w, body)
	case m.tl.screen == toolsAll:
		lines = m.allToolsView(w, body)
	default:
		lines = m.projectToolsView(w, body)
	}
	for len(lines) < body {
		lines = append(lines, "")
	}
	out = append(out, lines[:body]...)
	if m.tl.modal != nil {
		out = overlayModal(out, m.modalBox(w, len(out)), w)
	}
	return out
}

func (m Model) toolsScreenLine(w int) string {
	var parts []string
	for i, n := range toolsScreenNames {
		label := " " + n + " "
		if i == m.tl.screen {
			parts = append(parts, sSel.Render(label))
		} else {
			parts = append(parts, sDim.Render(label))
		}
	}
	left := strings.Join(parts, " ") + tsFaint.Render("  [ ] switch")
	if m.tl.screen == toolsAll && (m.tl.search != "" || m.tl.searching) {
		left += tsAcc.Render("   /" + sanitize(m.tl.search) + searchCursor(m.tl.searching))
	} else if m.tl.screen == toolsProject && (m.tl.projFind != "" || m.tl.searching) {
		left += tsAcc.Render("   /" + sanitize(m.tl.projFind) + searchCursor(m.tl.searching))
	} else {
		left += tsFaint.Render("   / search")
	}
	if !m.tl.loaded {
		return clip(left, w)
	}
	skills, mcp := 0, 0
	for _, r := range m.tl.cat.Rows {
		if r.Kind == protocol.InventoryMCP {
			mcp++
		} else {
			skills++
		}
	}
	n := toolsdiff.Counts(m.tl.cat.Diffs, m.tl.accepted)
	diffs := tsGood.Render("no differences")
	if n > 0 {
		diffs = tsWarn.Render(nounCount(n, "difference", "differences"))
	}
	right := tsDim.Render(nounCount(skills, "skill", "skills")+" · "+nounCount(mcp, "MCP server", "MCP servers")+" · ") + diffs +
		tsDim.Render(" · "+nounCount(len(m.tl.cat.Machines), "machine", "machines"))
	if m.tl.loading {
		right += tsDim.Render(" · reading")
	} else if !m.tl.at.IsZero() {
		right += tsDim.Render(" · scanned " + ago(m.now().Sub(m.tl.at)) + " ago")
	}
	gap := w - ansi.StringWidth(left) - ansi.StringWidth(right)
	if gap < 2 {
		return clip(left, w)
	}
	return left + strings.Repeat(" ", gap) + right
}

// toolsHints lists the keys of the Tools tab. [ and ] switch the screens: the
// digits are the global tabs and tab goes to the CTO box.
func (m Model) toolsHints() []hint {
	switch m.tl.screen {
	case toolsDiffs:
		return []hint{{"j/k", "move", 1}, {"enter", "fix", 1}, {"x", "accept as is", 1}, {".", "accepted", 3}, {"[ ]", "screen", 1}, {"r", "rescan", 4}, {"tab", "CTO", 2}, {"?", "help", 3}, {"q", "quit", 5}}
	case toolsAll:
		return []hint{{"j/k", "item", 1}, {"h/l", "machine", 1}, {"a", "turn on", 1}, {"u", "turn off", 1}, {"/", "search", 2}, {"[ ]", "screen", 1}, {"r", "rescan", 4}, {"tab", "CTO", 2}, {"?", "help", 3}, {"q", "quit", 5}}
	}
	return []hint{{"j/k", "project", 1}, {"m", "machine", 1}, {"/", "filter", 2}, {"[ ]", "screen", 1}, {"r", "rescan", 4}, {"tab", "CTO", 2}, {"?", "help", 3}, {"q", "quit", 5}}
}

// toolsHelpLine takes over the key line for a toast, a text box or a plan.
func (m Model) toolsHelpLine(w int) (string, bool) {
	if m.focus == focusCTO {
		return "", false
	}
	if md := m.tl.modal; md != nil {
		return "", false
	}
	if m.tl.searching {
		what := m.tl.search
		if m.tl.screen == toolsProject {
			what = m.tl.projFind
		}
		return clip(tsAcc.Render("/"+sanitize(what)+"▏")+tsDim.Render("   enter keeps it, esc clears it"), w), true
	}
	if t := m.activeToast(); t != "" {
		return clip(tsWarn.Render(sanitize(t)), w), true
	}
	return "", false
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

// ---- screen 1: differences ----

func (m Model) diffsView(w, h int) []string {
	ds := m.shownDiffs()
	toLook := toolsdiff.Counts(m.tl.cat.Diffs, m.tl.accepted)
	inner := max(h-2, 2)
	rows := inner - 1 // the last line is the count of what matches
	var body []string
	if len(ds) == 0 {
		body = append(body, tsGood.Render("Everything matches what you set. Nothing to do."))
	}
	per := 3
	if rows < 6 || len(ds)*3 > rows {
		per = 2 // no blank line between them when they would not all fit
	}
	visible := max(rows/per, 1)
	start := 0
	if m.tl.row >= visible {
		start = m.tl.row - visible + 1
	}
	for i := start; i < len(ds) && i < start+visible; i++ {
		d := ds[i]
		sel := i == m.tl.row
		accepted := m.tl.accepted[d.Key]
		mark := tsDim.Render("○ ")
		switch {
		case d.Info:
			mark = tsDim.Render("… ")
		case d.Severity == toolsdiff.SevErr:
			mark = tsErr.Render("! ")
		case d.Severity == toolsdiff.SevWarn:
			mark = tsWarn.Render("◐ ")
		}
		title := tsText.Render(sanitize(d.Title))
		if d.Info {
			title = tsDim.Render(sanitize(d.Title))
		}
		if accepted {
			title = tsFaint.Render(sanitize(d.Title)) + tsFaint.Render("  (accepted)")
		}
		fix := tsAcc.Render(sanitize(d.Fix))
		lead := "fix: "
		if d.Info {
			fix, lead = tsFaint.Render(sanitize(d.Fix)), ""
		}
		l1 := marker(sel) + mark + title
		l2 := "    " + tsFaint.Render(lead) + fix
		if sel {
			l1, l2 = selectedRow(padStyled(l1, w-4)), selectedRow(padStyled(l2, w-4))
		}
		body = append(body, l1, l2)
		if per == 3 {
			body = append(body, "")
		}
	}
	for len(body) < rows {
		body = append(body, "")
	}
	body = body[:rows]
	same := 0
	named := map[string]bool{}
	for _, d := range m.tl.cat.Diffs {
		named[d.Name] = true
	}
	for _, r := range m.tl.cat.Rows {
		if (r.Origin == toolsdiff.OriginStore || r.Origin == toolsdiff.OriginGateway) && !named[r.Name] {
			same++
		}
	}
	body = append(body, tsGood.Render(nounCount(same, "tool is", "tools are")+" the same everywhere you set them")+tsFaint.Render("  ·  [ ] shows all of them"))
	title := fmt.Sprintf("What differs · %d to look at", toLook)
	return panel(title, body, w, roleAccent)
}

func marker(sel bool) string {
	if sel {
		return tsAccB.Render("▸") + " "
	}
	return "  "
}

// ---- screen 2: all tools ----

type toolCols struct {
	name, from, proj, note int
	machs                  []int // one width per machine column
}

func toolColumns(w int, ms []toolsdiff.Machine) toolCols {
	inner := w - 4
	c := toolCols{from: 17, proj: 15}
	if inner >= 110 {
		c.from, c.proj, c.note = 21, 22, 22
	}
	machTotal := 0
	for _, mc := range ms {
		n := min(max(ansi.StringWidth(sanitize(mc.Name))+2, 6), 14)
		if len(ms) > 3 {
			n = min(n, 10)
		}
		c.machs = append(c.machs, n)
		machTotal += n
	}
	c.name = inner - c.from - machTotal - c.proj - c.note
	if c.name < 14 {
		c.proj = max(c.proj-(14-c.name), 8)
		c.name = inner - c.from - machTotal - c.proj - c.note
	}
	if c.name < 12 {
		c.from = max(c.from-(12-c.name), 8)
		c.name = inner - c.from - machTotal - c.proj - c.note
	}
	c.name = max(c.name, 8)
	return c
}

func cellGlyph(c toolsdiff.Cell) (string, lipgloss.Style) {
	switch c.State {
	case toolsdiff.CellOn:
		return "●", tsGood
	case toolsdiff.CellPartial:
		return "◐", tsWarn
	case toolsdiff.CellOther:
		return c.Label, tsWarn
	case toolsdiff.CellFound:
		return "○", tsDim
	}
	return "·", tsFaint
}

func (m Model) allToolsView(w, h int) []string {
	rows := m.shownRows()
	ms := m.tl.cat.Machines
	cols := toolColumns(w, ms)
	detailH := 4
	listH := max(h-detailH-2-2, 2) // panel borders, the header and the count
	hdrStyle := func(i int) lipgloss.Style {
		if m.tl.col == i {
			return tsAccB
		}
		return tsDim
	}
	hdr := "  " + tsDim.Render(cell("NAME", cols.name-3)) + " " + tsDim.Render(cell("FROM", cols.from-1)) + " "
	for i, mc := range ms {
		hdr += hdrStyle(i).Render(cell(" "+mc.Name, cols.machs[i]-1)) + " "
	}
	hdr += hdrStyle(len(ms)).Render(cell("PROJECTS", cols.proj))
	if cols.note > 0 {
		hdr += tsDim.Render("NOTE")
	}
	body := []string{hdr}
	start := 0
	if m.tl.row >= listH {
		start = m.tl.row - listH + 1
	}
	for i := start; i < len(rows) && i < start+listH; i++ {
		r := rows[i]
		sel := i == m.tl.row
		prefix, nameStyle := "", tsText
		switch {
		case r.Kind == protocol.InventoryMCP:
			prefix, nameStyle = "◆ ", tsMat
		case r.Origin == toolsdiff.OriginRepo:
			prefix, nameStyle = "⌂ ", tsDim
		}
		line := marker(sel) + nameStyle.Render(cell(prefix+r.Name, cols.name-3)) + " "
		fromStyle := tsDim
		if r.Origin == toolsdiff.OriginStore {
			fromStyle = tsText
		}
		line += fromStyle.Render(cell(r.From, cols.from-1)) + " "
		for ci := range ms {
			g, st := cellGlyph(r.Cells[ci])
			text := " " + g
			if sel && m.tl.col == ci {
				text = "[" + g + "]"
				st = sSel
			}
			line += st.Render(cell(text, cols.machs[ci]-1)) + " "
		}
		ptext, pwarn := r.ProjectSummary()
		pst := tsText
		switch {
		case pwarn:
			pst = tsWarn
		case ptext == "-":
			pst = tsFaint
		case r.Origin == toolsdiff.OriginRepo:
			pst = tsDim
		}
		if sel && m.tl.col == len(ms) {
			ptext, pst = "["+ptext+"]", sSel
		}
		line += pst.Render(cell(ptext, cols.proj))
		if cols.note > 0 && r.Note != "" {
			line += tsWarn.Render(clip(r.Note, cols.note))
		}
		if sel {
			line = selectedRow(padStyled(line, w-4))
		}
		body = append(body, line)
	}
	for len(body) < listH+1 {
		body = append(body, "")
	}
	count := "no tools yet"
	if len(rows) > 0 {
		count = fmt.Sprintf("%d-%d of %d", start+1, min(len(rows), start+listH), len(rows))
	}
	body = append(body, tsFaint.Render(count))
	out := panel("All tools · on or off per machine, or for chosen projects", body, w, roleAccent)
	name, detail := "", ""
	if m.tl.row < len(rows) {
		name = rows[m.tl.row].Name
		detail = rows[m.tl.row].Detail
		if n := rows[m.tl.row].Cells; len(n) > m.tl.col && m.tl.col < len(ms) && n[m.tl.col].Note != "" {
			detail += " " + n[m.tl.col].Note
		}
	}
	dl := wrap(sanitize(detail), w-4)
	for len(dl) < 2 {
		dl = append(dl, "")
	}
	dl = dl[:2]
	for i := range dl {
		dl[i] = tsDim.Render(dl[i])
	}
	return append(out, panelWithBorder("Details · "+sanitize(name), dl, w, roleText, roleFaint)...)
}

// ---- screen 3: by project ----

func (m Model) projectToolsView(w, h int) []string {
	ms := m.tl.cat.Machines
	proj := m.tl.projSel
	mach := ""
	if m.tl.machine < len(ms) {
		mach = ms[m.tl.machine].Name
	}
	rows := max(h-2, 3)
	lw := 22
	if w < 100 {
		lw = 20
	}
	// left: the project list, scrolled around the selection
	ps := m.shownProjects()
	cur := 0
	for i, p := range ps {
		if p == proj {
			cur = i
		}
	}
	start := max(0, min(cur-rows/2, len(ps)-rows))
	var left []string
	for i := start; i < len(ps) && len(left) < rows; i++ {
		p := ps[i]
		if p == proj {
			left = append(left, tsAccB.Render(cell("▸ "+p, lw)))
		} else {
			left = append(left, tsText.Render(cell("  "+p, lw)))
		}
	}
	if len(ps) == 0 {
		left = append(left, tsFaint.Render(cell("  no project matches", lw)))
	}
	right := m.projectLayers(proj, mach, rows, w-4-lw-2)
	var body []string
	for i := 0; i < rows; i++ {
		l := strings.Repeat(" ", lw)
		if i < len(left) {
			l = left[i]
		}
		r := ""
		if i < len(right) {
			r = right[i]
		}
		body = append(body, l+tsFaint.Render("│ ")+r)
	}
	title := fmt.Sprintf("By project · what %s gets on %s · m machine", sanitize(proj), sanitize(mach))
	return panel(title, body, w, roleAccent)
}

type layer struct {
	head  string
	color lipgloss.Style
	lines []string
	empty string
}

func (m Model) projectLayers(proj, mach string, rows, width int) []string {
	if proj == "" {
		return []string{tsFaint.Render("no project yet")}
	}
	ms := m.tl.cat.Machines
	mi := m.tl.machine
	var repo, forProj, every, servers layer
	repo = layer{head: "FROM THE REPO", color: tsDim, empty: "  none"}
	forProj = layer{head: "ON FOR THIS PROJECT", color: tsAcc, empty: "  none · a on All tools adds one"}
	every = layer{head: "ON " + strings.ToUpper(mach) + " FOR EVERY PROJECT", color: tsText, empty: "  none"}
	servers = layer{head: "MCP SERVERS", color: tsMat, empty: "  none"}
	repoNames := map[string]bool{}
	var available []string
	for _, r := range m.tl.cat.Rows {
		if r.Origin == toolsdiff.OriginRepo && r.Repo == proj {
			repoNames[r.Name] = true
			available = append(available, r.Name)
			repo.lines = append(repo.lines, "  "+tsDim.Render("⌂ ")+tsText.Render(cell(r.Name, 30))+tsFaint.Render("committed"))
		}
	}
	for _, r := range m.tl.cat.Rows {
		if r.Origin == toolsdiff.OriginRepo {
			continue
		}
		if r.Kind == protocol.InventoryMCP {
			on := false
			for _, p := range r.Projects {
				on = on || p.Name == proj
			}
			if len(r.Projects) == 0 && mi < len(r.Cells) && r.Cells[mi].State != toolsdiff.CellOff {
				on = true
			}
			if on {
				available = append(available, r.Name)
				servers.lines = append(servers.lines, "  "+tsMat.Render("◆ ")+tsText.Render(cell(r.Name, 30))+tsFaint.Render(r.From))
			}
			continue
		}
		state := ""
		for _, p := range r.Projects {
			if p.Name == proj {
				state = p.State
			}
		}
		if state != "" {
			name, why, ws := tsText.Render(cell(r.Name, 30)), r.From, tsFaint
			dot := tsGood.Render("● ")
			switch state {
			case toolsdiff.ProjHidden:
				name, why, ws, dot = tsStrike.Render(cell(r.Name, 30)), "hidden by the repo's own copy", tsWarn, tsWarn.Render("● ")
			case toolsdiff.ProjPending:
				why, ws, dot = "pending: machine offline", tsWarn, tsWarn.Render("● ")
			case toolsdiff.ProjLocal:
				why, ws, dot = "local copy, not committed", tsDim, tsDim.Render("● ")
			}
			if state == toolsdiff.ProjOn || state == toolsdiff.ProjLocal {
				available = append(available, r.Name)
			}
			forProj.lines = append(forProj.lines, "  "+dot+name+ws.Render(why))
			continue
		}
		if len(r.Projects) == 0 && mi < len(r.Cells) && r.Cells[mi].State != toolsdiff.CellOff {
			g, st := cellGlyph(r.Cells[mi])
			if r.Cells[mi].State == toolsdiff.CellOther {
				g = "◐"
			}
			name, why := tsText.Render(cell(r.Name, 30)), r.From
			if r.Origin == toolsdiff.OriginMachines {
				why = "not managed"
			}
			if repoNames[r.Name] {
				name, why = tsStrike.Render(cell(r.Name, 30)), "hidden by the repo's copy"
			} else {
				available = append(available, r.Name)
			}
			every.lines = append(every.lines, "  "+st.Render(cell(g, 2))+name+tsFaint.Render(why))
		}
	}
	_ = ms
	layers := []*layer{&repo, &forProj, &every, &servers}
	// Share the lines. A heading and a line each come first; short layers are
	// shown whole, and the longest ones get what is left.
	budget := rows - 1 - len(layers) // the needs line and the headings
	take := make([]int, len(layers))
	order := make([]int, len(layers))
	for i, l := range layers {
		order[i] = i
		if len(l.lines) == 0 {
			budget-- // the "none" line
		}
	}
	sort.SliceStable(order, func(a, b int) bool { return len(layers[order[a]].lines) < len(layers[order[b]].lines) })
	for n, i := range order {
		l := layers[i]
		if len(l.lines) == 0 {
			continue
		}
		fair := max(budget/(len(order)-n), 1)
		take[i] = min(len(l.lines), fair)
		budget -= take[i]
	}
	// The "+ n more" lines need room too: take lines from the longest layer.
	size := func() int {
		n := 0
		for i, l := range layers {
			n += 1 + max(take[i], 1)
			if take[i] < len(l.lines) {
				n++
			}
		}
		return n
	}
	for size() > rows-1 {
		big := -1
		for i := range layers {
			if take[i] > 1 && (big < 0 || take[i] > take[big]) {
				big = i
			}
		}
		if big < 0 {
			break
		}
		take[big]--
	}
	var out []string
	for i, l := range layers {
		out = append(out, l.color.Bold(true).Render(l.head)+tsFaint.Render(fmt.Sprintf("  %d", len(l.lines))))
		if len(l.lines) == 0 {
			out = append(out, tsFaint.Render(l.empty))
		}
		out = append(out, l.lines[:take[i]]...)
		if take[i] < len(l.lines) {
			out = append(out, tsFaint.Render(fmt.Sprintf("  + %d more", len(l.lines)-take[i])))
		}
	}
	for len(out) < rows-1 {
		out = append(out, "")
	}
	out = out[:min(len(out), rows-1)]
	return append(out, m.needsLine(proj, available))
}

var needStopWord = map[string]bool{"skill": true, "skills": true, "server": true, "tools": true, "found": true}

// needsLine is the charter's needs text, and whether a tool covers it. A need is
// advisory: nothing is assigned because of it.
func (m Model) needsLine(proj string, available []string) string {
	text := ""
	for _, n := range m.tl.needs {
		if n.Node == proj {
			text = n.Text
		}
	}
	head := tsFaint.Render("needs: ")
	if text == "" {
		return head + tsFaint.Render("nothing listed")
	}
	low := strings.ToLower(text)
	for _, name := range available {
		for _, tok := range strings.Split(strings.ToLower(name), "-") {
			if len(tok) >= 5 && !needStopWord[tok] && strings.Contains(low, tok) {
				return head + tsGood.Render(sanitize(text)+" - "+name)
			}
		}
	}
	return head + tsWarn.Render(sanitize(text)+" - not covered yet")
}

// ---- plan on top ----

func (m Model) modalBox(w, h int) []string {
	md := m.tl.modal
	bw := w
	var body []string
	switch {
	case md.picker != nil && !md.loading && !md.done:
		body = pickerLines(md.picker, bw-4, max(h-8, 4))
	default:
		for _, l := range md.lines {
			body = append(body, wrapStyled(l, bw-4)...)
		}
		if md.loading && len(md.lines) == 0 {
			body = append(body, tsDim.Render("planning..."))
		}
		if md.showPaths && len(md.paths) > 0 {
			body = append(body, "", tsFaint.Bold(true).Render("exact folders"))
			for _, p := range md.paths {
				body = append(body, tsDim.Render("  "+sanitize(p)))
			}
		}
	}
	room := max(h-6, 3)
	if len(body) > room {
		body = append(body[:room-1:room-1], tsFaint.Render(fmt.Sprintf("  and %d more lines", len(body)-room+1)))
	}
	body = append(body, "")
	var foot []string
	for i := 0; i+1 < len(md.foot); i += 2 {
		foot = append(foot, md.foot[i], md.foot[i+1])
	}
	body = append(body, clip(keyHints(foot...), bw-4))
	return panelWithBorder(md.title, body, bw, roleAccent, roleAccent)
}

// wrapStyled wraps a styled line at width, keeping short lines as they are.
func wrapStyled(s string, width int) []string {
	if ansi.StringWidth(s) <= width {
		return []string{s}
	}
	return strings.Split(ansi.Wrap(s, width, ""), "\n")
}

func pickerLines(p *projPicker, width, rows int) []string {
	shown := p.shown()
	picked := 0
	for _, v := range p.sel {
		if v {
			picked++
		}
	}
	head := tsDim.Render(fmt.Sprintf("%d of %d projects shown · %d ticked", len(shown), len(p.all), picked))
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

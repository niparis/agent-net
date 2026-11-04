package tui

import (
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// stripLetters are the one-letter names of the steps, in deliverySteps order.
const stripLetters = "artpmldv"

// stripSpacedFrom is the inner width from which the strip and the list get
// room for spaces between marks and a head column.
const stripSpacedFrom = 96

func stripGlyph(s stripState) string {
	switch s {
	case stripPassed:
		return "✓"
	case stripFailed:
		return "✗"
	case stripRunning:
		return "*"
	case stripNotNeeded:
		return "–"
	}
	return "·"
}

func stripStyle(s stripState) lipgloss.Style {
	switch s {
	case stripPassed:
		return sBusy
	case stripFailed:
		return sBad
	case stripRunning:
		return lipgloss.NewStyle().Foreground(roleWarning)
	case stripNotNeeded:
		return sDim
	}
	return lipgloss.NewStyle().Foreground(roleFaint)
}

// deliveryStripWidth is the width of the strip. Every mark is a letter and a
// glyph, so the strip does not depend on colour alone.
func deliveryStripWidth(spaced bool) int {
	n := len(deliverySteps) * 2
	if spaced {
		n += len(deliverySteps) - 1
	}
	return n
}

func deliveryStrip(states []stripState, spaced bool) string {
	var b strings.Builder
	for i, s := range states {
		if i > 0 && spaced {
			b.WriteString(" ")
		}
		b.WriteString(sDim.Render(string(stripLetters[i])) + stripStyle(s).Render(stripGlyph(s)))
	}
	return b.String()
}

// deliveryLegend names the letters and the marks. It wraps between items, so a
// name is never split.
func deliveryLegend(w int) []string {
	steps := make([]string, len(deliverySteps))
	for i, s := range deliverySteps {
		steps[i] = string(stripLetters[i]) + " " + s
	}
	marks := []string{"✓ passed", "✗ failed", "* running", "– not needed", "· waiting"}
	var out []string
	for _, items := range [][]string{steps, marks} {
		// Split a list that does not fit in half, so the lines look even.
		parts := [][]string{items}
		if ansi.StringWidth(strings.Join(items, "  ")) > w {
			parts = [][]string{items[:(len(items)+1)/2], items[(len(items)+1)/2:]}
		}
		for _, p := range parts {
			out = append(out, sDim.Render(clip(strings.Join(p, "  "), w)))
		}
	}
	return out
}

// deliveryColumns is the layout of the list for one width.
type deliveryColumns struct {
	repo, branch, head, owner int // head is 0 when the column is left out
	spaced                    bool
}

const (
	deliveryRevW  = 3 // "r12" fits, longer revisions are cut
	deliveryStepW = 7
	deliveryWaitW = 5
	deliveryLead  = 2
)

// gap is the space between columns: tighter when the screen is narrow.
func (c deliveryColumns) gap() int {
	if c.spaced {
		return 2
	}
	return 1
}

func deliveryLayout(inner int, changes []deliveryChange) deliveryColumns {
	var c deliveryColumns
	c.spaced = inner >= stripSpacedFrom
	longest := func(f func(deliveryChange) string, floor, ceil int) int {
		n := floor
		for _, ch := range changes {
			n = max(n, ansi.StringWidth(sanitize(f(ch))))
		}
		return min(n, ceil)
	}
	c.owner = longest(func(ch deliveryChange) string { return ch.StepOwner }, 5, 16)
	fixed := deliveryLead + deliveryRevW + deliveryStepW + c.owner + deliveryWaitW + deliveryStripWidth(c.spaced)
	columns := 6 // repo, branch, rev, step, owner, wait; the strip is the 7th
	if c.spaced {
		c.head = 7
		fixed += c.head
		columns++
	}
	fixed += columns * c.gap()
	avail := max(inner-fixed, 8)
	c.repo = longest(func(ch deliveryChange) string { return ch.Repo }, 6, 24)
	c.repo = min(c.repo, max(avail*2/5, 4))
	c.branch = max(avail-c.repo, 4)
	// A branch column wider than the longest branch only moves the rest away.
	c.branch = min(c.branch, longest(func(ch deliveryChange) string { return shortBranch(ch.Branch) }, 8, 60))
	return c
}

func (c deliveryColumns) width() int {
	n := deliveryLead + c.repo + c.branch + deliveryRevW + deliveryStepW + c.owner + deliveryWaitW + deliveryStripWidth(c.spaced) + 6*c.gap()
	if c.head > 0 {
		n += c.head + c.gap()
	}
	return n
}

func (c deliveryColumns) header() string {
	gap := strings.Repeat(" ", c.gap())
	parts := []string{cell("REPO", c.repo), cell("BRANCH", c.branch)}
	if c.head > 0 {
		parts = append(parts, cell("HEAD", c.head))
	}
	parts = append(parts, cell("REV", deliveryRevW), cell("STEP", deliveryStepW), cell("OWNER", c.owner), cell("WAIT", deliveryWaitW), "EVIDENCE")
	return sDim.Render(strings.Repeat(" ", deliveryLead) + strings.Join(parts, gap))
}

func (c deliveryColumns) row(ch deliveryChange, selected bool) string {
	gap := strings.Repeat(" ", c.gap())
	lead := "  "
	if selected {
		lead = "▸ "
	}
	parts := []string{cell(ch.Repo, c.repo), sDim.Render(cell(shortBranch(ch.Branch), c.branch))}
	if c.head > 0 {
		parts = append(parts, sDim.Render(cell(shortCommit(ch.Head), c.head)))
	}
	rev := "-"
	if ch.Revision > 0 {
		rev = "r" + strconv.Itoa(ch.Revision)
	}
	parts = append(parts, cell(rev, deliveryRevW), cell(firstNonEmpty(ch.Step, "-"), deliveryStepW), cell(firstNonEmpty(ch.StepOwner, "-"), c.owner), cell(deliveryWaitText(ch.Waiting), deliveryWaitW), deliveryStrip(deliveryStripStates(ch), c.spaced))
	line := lead + strings.Join(parts, gap)
	if selected {
		line = selectedRow(line)
	}
	return line
}

// quotasSubtabs is the line under the title: the three sub-tabs of Quotas.
func (m Model) quotasSubtabs(w int) string {
	labels := []string{"Quotas", "Workers log", "Delivery"}
	active := 0
	switch {
	case m.delivery.on:
		active = 2
	case m.workersLog:
		active = 1
	}
	var parts []string
	for i, label := range labels {
		if i == active {
			parts = append(parts, sActiveTab.Render("[ "+label+" ]"))
		} else {
			parts = append(parts, sDim.Render("  "+label+"  "))
		}
	}
	return clip(strings.Join(parts, "  "), w)
}

// deliveryView draws the list, or the evidence of the selected change.
func (m Model) deliveryView(w, h int) []string {
	out := []string{m.titleLine(w), m.quotasSubtabs(w)}
	d := m.delivery
	inner := max(w-4, 1)
	room := max(h-len(out)-2, 0) // the panel borders take two lines
	var errLine []string
	if d.err != "" {
		for _, l := range wrap("Could not load changes: "+sanitize(d.err), inner) {
			errLine = append(errLine, sBad.Render(l))
		}
	}
	sel := min(max(d.row, 0), max(len(d.changes)-1, 0))
	if d.detail && len(d.changes) > 0 {
		return append(out, m.deliveryDetail(d.changes[sel], errLine, w, room)...)
	}
	if len(d.changes) == 0 {
		var body []string
		body = append(body, errLine...)
		switch {
		case d.err != "":
		case m.deliverySrc != nil && !d.loaded:
			body = append(body, sDim.Render("Loading changes..."))
		default:
			body = append(body, "No open changes.", sDim.Render("A change shows up here when a worker hands back finished work."))
		}
		return append(out, panel("Delivery", messageFit(body, min(len(body), room), 0), w, roleAccent)...)
	}
	cols := deliveryLayout(inner, d.changes)
	legend := deliveryLegend(inner)
	body := append([]string{}, errLine...)
	body = append(body, cols.header())
	listRoom := max(room-len(body)-len(legend), 1)
	var list []string
	for i, ch := range d.changes {
		list = append(list, cols.row(ch, i == sel))
	}
	body = append(body, messageFit(list, min(listRoom, len(list)), sel)...)
	if room-len(body) >= len(legend) {
		body = append(body, legend...)
	}
	title := strconv.Itoa(len(d.changes)) + " open"
	if len(d.changes) == 1 {
		title += " change"
	} else {
		title += " changes"
	}
	return append(out, panelWithRightHeader("Delivery", title, body, w, roleAccent)...)
}

func resultStyle(result string) lipgloss.Style { return stripStyle(normalResult(result)) }

// evidenceFields is "key=value" pairs in key order.
func evidenceFields(f map[string]string) string {
	keys := make([]string, 0, len(f))
	for k := range f {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for i, k := range keys {
		keys[i] = sanitize(k) + "=" + sanitize(f[k])
	}
	return strings.Join(keys, " ")
}

// deliveryDetail lists the evidence of one change, oldest first.
func (m Model) deliveryDetail(ch deliveryChange, errLine []string, w, room int) []string {
	inner := max(w-4, 1)
	spaced := inner >= stripSpacedFrom
	var head []string
	head = append(head, sBold.Render(sanitize(ch.Repo))+"  "+sanitize(shortBranch(ch.Branch)))
	revision := "-"
	if ch.Revision > 0 {
		revision = "r" + strconv.Itoa(ch.Revision)
	}
	facts := "head: " + firstNonEmpty(sanitize(ch.Head), "-") + "  revision: " + revision + "  step: " + firstNonEmpty(sanitize(ch.Step), "-") + "  owner: " + firstNonEmpty(sanitize(ch.StepOwner), "-") + "  waiting: " + deliveryWaitText(ch.Waiting)
	for _, l := range wrap(facts, inner) {
		head = append(head, sDim.Render(l))
	}
	head = append(head, deliveryStrip(deliveryStripStates(ch), spaced))
	head = append(head, errLine...)

	evidence := append([]deliveryEvidence(nil), ch.Evidence...)
	sort.SliceStable(evidence, func(i, j int) bool { return evidence[i].At.Before(evidence[j].At) })

	const stepW, resultW, commitW, byW, timeW = 7, 10, 11, 12, 11
	wide := inner >= stripSpacedFrom+4
	modelW := 22
	gap := "  "
	var lines []string
	if wide {
		fieldsW := max(inner-(stepW+resultW+commitW+byW+modelW+timeW+6*2), 8)
		lines = append(lines, sDim.Render(strings.Join([]string{cell("STEP", stepW), cell("RESULT", resultW), cell("COMMIT", commitW), cell("BY", byW), cell("MODEL", modelW), cell("TIME", timeW), cell("FIELDS", fieldsW)}, gap)))
	} else {
		lines = append(lines, sDim.Render(strings.Join([]string{cell("STEP", stepW), cell("RESULT", resultW), cell("COMMIT", commitW), cell("BY", byW), cell("TIME", timeW)}, gap)))
	}
	if len(evidence) == 0 {
		lines = append(lines, sDim.Render("No evidence yet."))
	}
	for _, e := range evidence {
		commit := shortCommit(e.Commit)
		commitCell := cell(commit, commitW)
		if !sameCommit(e.Commit, ch.Head) {
			commitCell = sDim.Render(cell(commit+" old", commitW))
		}
		at := "-"
		if !e.At.IsZero() {
			at = e.At.In(m.loc).Format("01-02 15:04")
		}
		result := resultStyle(e.Result).Render(cell(firstNonEmpty(e.Result, "-"), resultW))
		fields := evidenceFields(e.Fields)
		if wide {
			fieldsW := max(inner-(stepW+resultW+commitW+byW+modelW+timeW+6*2), 8)
			lines = append(lines, strings.Join([]string{cell(e.Step, stepW), result, commitCell, cell(firstNonEmpty(e.By, "-"), byW), cell(firstNonEmpty(e.Model, "-"), modelW), cell(at, timeW), cell(firstNonEmpty(fields, "-"), fieldsW)}, gap))
			continue
		}
		lines = append(lines, strings.Join([]string{cell(e.Step, stepW), result, commitCell, cell(firstNonEmpty(e.By, "-"), byW), cell(at, timeW)}, gap))
		if second := strings.TrimSpace(sanitize(e.Model) + "  " + fields); second != "" {
			if e.Model == "" {
				second = fields
			}
			lines = append(lines, sDim.Render("  "+cell(second, max(inner-2, 1))))
		}
	}
	out := panel("Change", head, w, roleAccent)
	roomForEvidence := max(room-len(head)-2, 1)
	scroll := min(max(m.delivery.scroll, 0), max(len(lines)-roomForEvidence, 0))
	view := messageFit(lines, min(roomForEvidence, len(lines)), min(roomForEvidence-1+scroll, max(len(lines)-1, 0)))
	title := "Evidence"
	if len(lines) > roomForEvidence {
		title += " (" + strconv.Itoa(scroll+1) + "-" + strconv.Itoa(min(scroll+roomForEvidence, len(lines))) + " of " + strconv.Itoa(len(lines)) + ")"
	}
	return append(out, panel(title, view, w, roleAccent)...)
}

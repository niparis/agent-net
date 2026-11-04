package tui

import (
	"sort"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/protocol"
)

// stripLetters are the one-letter names of the steps, in deliverySteps order.
const stripLetters = "artpmldv"

// stripSpacedFrom is the inner width from which the strip and the list get
// room for spaces between marks and a repo column.
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
		return "-"
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

// deliveryLegend names every letter the strip shows, in strip order. A line
// that does not fit wraps between items, so no word is cut.
func deliveryLegend(w int) []string {
	type item struct{ text, sep string }
	items := []item{
		{"a accept", ""}, {"r review", " · "}, {"t test", " · "}, {"p approve", " · "},
		{"m merge", " · "}, {"l release", " · "}, {"d deploy", " · "}, {"v verify.", " · "},
		{"✓ passed", " "}, {"✗ failed", " "}, {"· waiting", " "}, {"- not needed", " "},
	}
	line := ""
	for _, it := range items {
		line += it.sep + it.text
	}
	if ansi.StringWidth(line) <= w {
		return []string{sDim.Render(line)}
	}
	// Keep the step names together when they fit on their own line; only the
	// marks need the second line.
	steps := "a accept · r review · t test · p approve · m merge · l release · d deploy · v verify."
	if ansi.StringWidth(steps) <= w {
		return []string{sDim.Render(steps), sDim.Render("✓ passed ✗ failed · waiting - not needed")}
	}
	var out []string
	line = ""
	for _, it := range items {
		if line == "" {
			line = it.text
			continue
		}
		if ansi.StringWidth(line+it.sep+it.text) <= w {
			line += it.sep + it.text
			continue
		}
		out = append(out, line)
		line = it.text
	}
	if line != "" {
		out = append(out, line)
	}
	for i := range out {
		out[i] = sDim.Render(out[i])
	}
	return out
}

// deliveryColumns is the layout of the list for one width. The title is the
// main column; the branch, head and revision live in the detail view.
type deliveryColumns struct {
	title, repo, step, status, age int
	spaced                         bool
}

const (
	deliveryStepW   = 8 // "approve" fits
	deliveryStatusW = 22
	deliveryAgeW    = 6
	deliveryLead    = 2
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
	gaps := 4 // between the five columns without a repo
	if c.spaced {
		c.repo = 10
		for _, ch := range changes {
			c.repo = max(c.repo, ansi.StringWidth(sanitize(ch.Repo)))
		}
		c.repo = min(c.repo, 16)
		gaps = 5
	}
	fixed := deliveryLead + c.repo + deliveryStepW + deliveryStatusW + deliveryAgeW + deliveryStripWidth(c.spaced) + gaps*c.gap()
	c.title = max(inner-fixed, 12)
	return c
}

func (c deliveryColumns) width() int {
	n := deliveryLead + c.title + c.repo + deliveryStepW + deliveryStatusW + deliveryAgeW + deliveryStripWidth(c.spaced) + 4*c.gap()
	if c.repo > 0 {
		n += c.gap()
	}
	return n
}

func (c deliveryColumns) header() string {
	gap := strings.Repeat(" ", c.gap())
	parts := []string{cell("TITLE", c.title)}
	if c.repo > 0 {
		parts = append(parts, cell("REPO", c.repo))
	}
	parts = append(parts, cell("STEP", deliveryStepW), cell("STATUS", deliveryStatusW), cell("AGE", deliveryAgeW), "EVIDENCE")
	return sDim.Render(strings.Repeat(" ", deliveryLead) + strings.Join(parts, gap))
}

func (m Model) deliveryRow(c deliveryColumns, ch deliveryChange, selected bool) string {
	gap := strings.Repeat(" ", c.gap())
	lead := "  "
	if selected {
		lead = "▸ "
	}
	parts := []string{cell(deliveryTitle(ch), c.title)}
	if c.repo > 0 {
		parts = append(parts, sDim.Render(cell(ch.Repo, c.repo)))
	}
	status, style := m.deliveryStatusText(ch)
	age := deliveryWaitText(ch.Waiting)
	ageStyle := sDim
	if deliveryStale(ch) {
		age += "!"
		ageStyle = lipgloss.NewStyle().Foreground(roleWarning)
	}
	parts = append(parts, cell(firstNonEmpty(ch.Step, "-"), deliveryStepW), style.Render(cell(status, deliveryStatusW)), ageStyle.Render(cell(age, deliveryAgeW)), deliveryStrip(deliveryStripStates(ch), c.spaced))
	line := lead + strings.Join(parts, gap)
	if selected {
		line = selectedRow(line)
	}
	return line
}

// deliveryTitle is the main column: what the work is, never the branch.
func deliveryTitle(ch deliveryChange) string {
	if title := strings.TrimSpace(sanitize(ch.Title)); title != "" {
		return title
	}
	return "(untitled) " + shortID(ch.ID)
}

// deliveryWho names the session or node a ready step waits for. The owner is
// "you"; a caller session is the name the sessions list shows for it.
func (m Model) deliveryWho(ch deliveryChange) string {
	switch ch.OwnerKind {
	case protocol.OwnerPerson, protocol.OwnerDelegate:
		return "you"
	case protocol.OwnerGitHub:
		return "github"
	case protocol.OwnerNode:
		return firstNonEmpty(sanitize(ch.StepOwner), "a node")
	case protocol.OwnerCaller:
		if ch.Caller == "" {
			return firstNonEmpty(sanitize(ch.StepOwner), "the caller")
		}
		if ch.Caller == m.snap.OwnerSession {
			return "you"
		}
		if a, ok := m.agentByID(ch.Caller); ok {
			return m.needsName(a)
		}
		return shortID(ch.Caller)
	}
	return firstNonEmpty(sanitize(ch.StepOwner), "someone")
}

// deliveryStatusText says what is true of the current step: waiting for its
// owner, running only when a task is claimed, or how it ended.
func (m Model) deliveryStatusText(ch deliveryChange) (string, lipgloss.Style) {
	if ch.State == protocol.ChangeDone {
		return "done", sDim
	}
	if ch.State == protocol.ChangeStopped {
		return "stopped", sDim
	}
	switch ch.StepState {
	case protocol.StepStateClosed:
		return "closed", sDim
	case protocol.StepStateRunning:
		return "running", sBusy
	case protocol.StepStateFailed:
		return "failed", sBad
	case protocol.StepStatePassed:
		return "passed", sBusy
	case protocol.StepStateSkipped:
		return "not needed", sDim
	case protocol.StepStateWaiting:
		return "blocked", sDim
	case protocol.StepStateReady, protocol.StepStateNeedsOwner:
		// The owner has to act; an agent waits for the owner's word.
		return "waiting for " + m.deliveryWho(ch), lipgloss.NewStyle().Foreground(roleWarning)
	}
	if ch.State == protocol.ChangeMerged {
		return "merged", sDim
	}
	return "-", sDim
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
	visible := m.deliveryVisible()
	owner := m.snap.OwnerSession
	sel := min(max(d.row, 0), max(len(visible)-1, 0))
	if d.detail && d.detailErr != "" {
		for _, l := range wrap("Could not load change: "+sanitize(d.detailErr), inner) {
			errLine = append(errLine, sBad.Render(l))
		}
	}
	if d.detail {
		if ch, ok := m.selectedChange(); ok {
			return append(out, m.deliveryDetail(ch, errLine, w, room)...)
		}
	}
	if len(visible) == 0 {
		var body []string
		body = append(body, errLine...)
		switch {
		case d.err != "":
		case m.deliverySrc != nil && !d.loaded:
			body = append(body, sDim.Render("Loading changes..."))
		case len(d.changes) > 0:
			done, stale := m.deliveryHidden()
			body = append(body, "Nothing needs attention.", sDim.Render(deliveryHiddenText(done, stale)))
		default:
			body = append(body, "No open changes.", sDim.Render("A change shows up here when a worker hands back finished work."))
		}
		return append(out, panel("Delivery", messageFit(body, min(len(body), room), 0), w, roleAccent)...)
	}
	cols := deliveryLayout(inner, visible)
	legend := deliveryLegend(inner)
	body := append([]string{}, errLine...)
	body = append(body, cols.header())
	var offline []string
	for _, step := range visible[sel].Steps {
		if strings.HasPrefix(step.Note, "waiting for ") && strings.HasSuffix(step.Note, " (offline)") {
			offline = append(offline, wrap(sanitize(step.Note), inner)...)
		}
	}
	var footer []string
	done, stale := m.deliveryHidden()
	if !d.showAll && done+stale > 0 {
		footer = append(footer, sDim.Render(deliveryHiddenText(done, stale)))
	}
	listRoom := max(room-len(body)-len(legend)-len(footer)-len(offline), 1)
	var list []string
	selLine, prev, first := 0, groupNeedsYou, true
	for i, ch := range visible {
		if group := deliveryGroupOf(ch, owner); first || group != prev {
			list = append(list, deliveryGroupHeader(group, deliveryGroupCount(visible, group, owner)))
			prev, first = group, false
		}
		if i == sel {
			selLine = len(list)
		}
		list = append(list, m.deliveryRow(cols, ch, i == sel))
	}
	body = append(body, messageFit(list, min(listRoom, len(list)), selLine)...)
	body = append(body, messageFit(offline, min(len(offline), max(room-len(body), 0)), 0)...)
	if len(footer) > 0 && room-len(body) >= len(legend)+len(footer) {
		body = append(body, footer...)
	}
	if room-len(body) >= len(legend) {
		body = append(body, legend...)
	}
	title := strconv.Itoa(len(visible)) + " shown"
	return append(out, panelWithRightHeader("Delivery", title, body, w, roleAccent)...)
}

// deliveryGroupHeader names a band of the list and counts its changes.
func deliveryGroupHeader(group deliveryGroup, n int) string {
	label := group.title() + " · " + strconv.Itoa(n)
	switch group {
	case groupNeedsYou:
		return lipgloss.NewStyle().Bold(true).Foreground(roleWarning).Render(label)
	case groupDone:
		return sDim.Render(label)
	}
	return sBold.Render(label)
}

func deliveryGroupCount(changes []deliveryChange, group deliveryGroup, ownerSession string) int {
	n := 0
	for _, ch := range changes {
		if deliveryGroupOf(ch, ownerSession) == group {
			n++
		}
	}
	return n
}

// deliveryHidden counts the rows the list leaves out until the owner asks.
func (m Model) deliveryHidden() (done, stale int) {
	owner := m.snap.OwnerSession
	for _, ch := range m.delivery.changes {
		if deliveryGroupOf(ch, owner) == groupDone {
			done++
		} else if deliveryStale(ch) {
			stale++
		}
	}
	return done, stale
}

func deliveryHiddenText(done, stale int) string {
	var parts []string
	if done > 0 {
		parts = append(parts, strconv.Itoa(done)+" done")
	}
	if stale > 0 {
		parts = append(parts, strconv.Itoa(stale)+" stale")
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " and ") + " hidden · a shows all"
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
	head = append(head, sBold.Render(deliveryTitle(ch)))
	head = append(head, sDim.Render(sanitize(ch.Repo)+"  "+sanitize(shortBranch(ch.Branch))))
	revision := "-"
	if ch.Revision > 0 {
		revision = "r" + strconv.Itoa(ch.Revision)
	}
	status, _ := m.deliveryStatusText(ch)
	facts := "head: " + firstNonEmpty(sanitize(ch.Head[:min(len(ch.Head), 12)]), "-") + "  revision: " + revision + "  state: " + firstNonEmpty(sanitize(ch.State), "-") + "  step: " + firstNonEmpty(sanitize(ch.Step), "-") + "  owner: " + firstNonEmpty(sanitize(ch.StepOwner), "-") + "  status: " + sanitize(status) + "  waiting: " + deliveryWaitText(ch.Waiting)
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
	if len(ch.Steps) > 0 {
		lines = append(lines, sBold.Render("Steps"))
		for _, step := range ch.Steps {
			line := sanitize(step.Name) + "  " + sanitize(step.Owner) + "  " + sanitize(step.State)
			if step.Note != "" {
				line += "  " + sanitize(step.Note)
			}
			lines = append(lines, wrap(line, inner)...)
		}
		lines = append(lines, "", sBold.Render("Evidence"))
	}
	if wide {
		fieldsW := max(inner-(stepW+resultW+commitW+byW+modelW+timeW+6*2), 8)
		lines = append(lines, sDim.Render(strings.Join([]string{cell("STEP", stepW), cell("RESULT", resultW), cell("COMMIT", commitW), cell("BY", byW), cell("MODEL", modelW), cell("TIME", timeW), cell("FIELDS", fieldsW)}, gap)))
	} else {
		lines = append(lines, sDim.Render(strings.Join([]string{cell("STEP", stepW), cell("RESULT", resultW), cell("COMMIT", commitW), cell("BY", byW), cell("TIME", timeW)}, gap)))
	}
	switch {
	case m.delivery.detailLoading:
		lines = append(lines, sDim.Render("Loading all evidence..."))
	case len(evidence) == 0:
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
	view := messageFit(lines, roomForEvidence, min(roomForEvidence-1+scroll, max(len(lines)-1, 0)))
	title := "Evidence"
	if len(ch.Steps) > 0 {
		title = "Steps and evidence"
	}
	if len(lines) > roomForEvidence {
		title += " (" + strconv.Itoa(scroll+1) + "-" + strconv.Itoa(min(scroll+roomForEvidence, len(lines))) + " of " + strconv.Itoa(len(lines)) + ")"
	}
	return append(out, panel(title, view, w, roleAccent)...)
}

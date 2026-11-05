package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"image/color"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/protocol"
)

// The Inbox tab (tab 7) is one place for everything that waits for the owner.
// It holds every kind the Needs you rows and the old inbox overlay held, with
// the same keys and the same operations.

type inboxGroup int

const (
	inboxHeld inboxGroup = iota
	inboxOutcome
	inboxLearned
	inboxProposal
	inboxApproval
	inboxNotice
)

// inboxBox is one of the three boxes the owner sees. Every group belongs to
// exactly one box.
type inboxBox int

const (
	inboxBoxAnswer inboxBox = iota
	inboxBoxProposals
	inboxBoxInfo
)

var inboxBoxOrder = []inboxBox{inboxBoxAnswer, inboxBoxProposals, inboxBoxInfo}

func (b inboxBox) title() string {
	switch b {
	case inboxBoxAnswer:
		return "Needs you"
	case inboxBoxProposals:
		return "Proposals"
	case inboxBoxInfo:
		return "For your information"
	}
	return ""
}

func (b inboxBox) color() color.Color {
	if b == inboxBoxAnswer {
		return roleWarning
	}
	return roleAccent
}

// box is the box a group sits in.
func (g inboxGroup) box() inboxBox {
	switch g {
	case inboxHeld, inboxApproval:
		return inboxBoxAnswer
	case inboxLearned, inboxProposal:
		return inboxBoxProposals
	}
	return inboxBoxInfo
}

// inboxRow is one waiting item. kind names the row's actions, not its group:
// held, outcome, learned, proposal, approval, question, notice or waiting (a
// session that reports a wait).
type inboxRow struct {
	group    inboxGroup
	key      string
	kind     string
	name     string
	text     string
	since    int64
	session  string
	held     protocol.Message
	outcome  protocol.OutcomeInboxEntry
	learned  protocol.LearnedSuggestion
	proposal protocol.CTOProposal
	item     protocol.InboxItem
}

// inboxRows builds every waiting row, grouped and newest first inside a group.
func (m Model) inboxRows() []inboxRow {
	var rows []inboxRow
	for _, held := range m.snap.Held {
		recipient := held.To
		if name := m.inboxSessionName(held.To); name != held.To {
			recipient = name
		}
		label := held.FromName
		if label == "" {
			label = held.From
		}
		rows = append(rows, inboxRow{
			group: inboxHeld, key: "held:" + held.ID, kind: "held",
			name: label, text: "held request → " + recipient,
			since: held.CreatedAt.Unix(), session: held.To, held: held,
		})
	}
	for _, e := range m.snap.Outcomes {
		rows = append(rows, inboxRow{
			group: inboxOutcome, key: "outcome:" + e.ReplyID, kind: "outcome",
			name: e.From + " → " + e.To, text: inboxOutcomeSummary(e),
			since: e.At / 1e9, outcome: e,
		})
	}
	for _, v := range m.snap.Learned {
		if v.Status != "pending" || v.Severity != "major" {
			continue
		}
		rows = append(rows, inboxRow{
			group: inboxLearned, key: fmt.Sprintf("learned:%d", v.ID), kind: "learned",
			name: v.Node + " (learned)", text: inboxLearnedSummary(v),
			since: v.CreatedAt / 1e9, learned: v,
		})
	}
	for _, p := range m.proposals {
		if p.Status != "open" {
			continue
		}
		rows = append(rows, inboxRow{
			group: inboxProposal, key: proposalKey(p), kind: "proposal",
			name: "CTO", text: m.inboxProposalSummary(p),
			since: p.CreatedAt.Unix(), proposal: p,
		})
	}
	for _, item := range m.inboxItems {
		if item.Status != "open" {
			continue
		}
		switch item.Kind {
		case "check":
			name, text := inboxNoticeParts(item)
			rows = append(rows, inboxRow{
				group: inboxNotice, key: "notice:" + item.ID, kind: "notice",
				name: name, text: text,
				since: item.CreatedAt.Unix(), item: item,
			})
		case "approval", "question":
			rows = append(rows, inboxRow{
				group: inboxApproval, key: "inbox:" + item.ID, kind: item.Kind,
				name: m.inboxSessionName(item.SessionID), text: inboxItemSummary(item),
				since: item.CreatedAt.Unix(), session: item.SessionID, item: item,
			})
		}
	}
	for _, a := range m.snap.Agents {
		if protocol.IsOwner(a.Harness) || a.Worker != nil || a.Waiting == nil {
			continue
		}
		if m.hiddenSessions[a.SessionID] && !m.showHiddenSessions {
			continue
		}
		if (!a.Online || a.Status == protocol.StatusDormant) && !m.showOffline {
			continue
		}
		if a.Worker != nil && !a.Online {
			continue
		}
		if m.activeOnly && m.idleLong(a) {
			continue
		}
		if !matches(a, m.filter) {
			continue
		}
		rows = append(rows, inboxRow{
			group: inboxApproval, key: "waiting:" + a.SessionID, kind: "waiting",
			name: m.needsName(a), text: inboxWaitingSummary(a.Waiting),
			since: a.Waiting.Since, session: a.SessionID,
		})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].group.box() != rows[j].group.box() {
			return rows[i].group.box() < rows[j].group.box()
		}
		if rankI, rankJ := inboxKindRank(rows[i]), inboxKindRank(rows[j]); rankI != rankJ {
			return rankI < rankJ
		}
		if rows[i].since != rows[j].since {
			return rows[i].since > rows[j].since
		}
		return rows[i].key < rows[j].key
	})
	return rows
}

// inboxKindRank orders the rows inside a box: what needs an answer comes
// first, then what proposes a change, then what only informs.
func inboxKindRank(row inboxRow) int {
	switch row.kind {
	case "question":
		return 0
	case "approval":
		return 1
	case "waiting":
		return 2
	case "held":
		return 3
	case "proposal":
		return 0
	case "learned":
		return 1
	case "outcome":
		return 0
	case "notice":
		return 1
	}
	return 9
}

// inboxFirstLine is the first line of an item's text, made safe for one row.
func inboxFirstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(sanitize(s))
}

// inboxOutcomeSummary says what a peer decided, in plain words.
func inboxOutcomeSummary(e protocol.OutcomeInboxEntry) string {
	verb := e.Outcome
	switch e.Outcome {
	case "accept":
		verb = "accepted"
	case "reject":
		verb = "refused"
	case "redirect":
		verb = "redirected"
	case "escalate":
		verb = "escalated"
	}
	if verb == "" {
		verb = "decided"
	}
	request := sanitize(e.Request)
	if request == "" {
		request = "the request"
	}
	text := request + " " + verb
	if e.Reason != "" {
		text += ": \"" + sanitize(e.Reason) + "\""
	}
	return text
}

// inboxLearnedSummary describes a learned suggestion without repeating the
// machine name the who column already shows.
func inboxLearnedSummary(v protocol.LearnedSuggestion) string {
	phrase := v.Phrase
	if v.Origin == "owner" && v.RawPhrase != "" {
		phrase = v.RawPhrase
	}
	phrase = sanitize(phrase)
	if v.Field == "edge" {
		verb := "add"
		if v.Kind == "remove" {
			verb = "remove"
		}
		return fmt.Sprintf("%s a %s edge to %s", verb, sanitize(v.EdgeKind), phrase)
	}
	field := inboxPlainField(v.Field)
	switch v.Kind {
	case "remove":
		return fmt.Sprintf("remove \"%s\" from %s", phrase, field)
	case "narrow":
		return fmt.Sprintf("change %s to \"%s\"", field, phrase)
	}
	return fmt.Sprintf("add \"%s\" to %s", phrase, field)
}

func inboxPlainField(field string) string {
	switch field {
	case "refuses":
		return "what it refuses"
	case "accepts":
		return "what it accepts"
	case "feature_requests":
		return "what it wants"
	case "":
		return "its charter"
	}
	return "its " + strings.ReplaceAll(field, "_", " ")
}

// inboxProposalSummary says what the CTO wants to do, with the machine named
// as the gateway knows it, never by its node ID.
func (m Model) inboxProposalSummary(p protocol.CTOProposal) string {
	var a map[string]any
	_ = json.Unmarshal(p.Args, &a)
	value := func(k string) string { v, _ := a[k].(string); return sanitize(v) }
	machineID := value("node_id")
	if machineID == "" {
		machineID = value("on")
	}
	if machineID == "" {
		machineID = p.NodeID
	}
	machine := m.inboxMachineName(machineID)
	if (p.Tool == "session_change" && p.Action == "session_start") || (p.Tool == "project_change" && p.Action == "create") {
		project := value("project")
		if project == "" {
			project = value("path")
		}
		if project == "" {
			project = "a project"
		}
		if p.Action == "create" {
			return fmt.Sprintf("create project %s on %s", project, machine)
		}
		harness := value("harness")
		if harness == "" {
			harness = "claude"
		}
		return fmt.Sprintf("start a %s session in %s, on %s", inboxHarnessName(harness), project, machine)
	}
	if p.Tool == "project_remove" || (p.Tool == "project_change" && p.Action == "remove") {
		project := value("name")
		if project == "" {
			project = value("path")
		}
		return fmt.Sprintf("remove project %s on %s", project, machine)
	}
	switch p.Action {
	case "charter_set":
		node := value("node")
		if node == "" {
			node = machine
		}
		return fmt.Sprintf("change %s's %s", node, strings.ReplaceAll(value("attr"), "_", " "))
	case "edge_set":
		return fmt.Sprintf("set the edge %s → %s to %s", value("from"), value("to"), value("rule"))
	}
	if p.Tool == "machine_remove" || (p.Tool == "settings_change" && p.Action == "machine_remove") {
		return "remove machine " + machine + " and revoke its token"
	}
	if p.Tool == "machine_rotate" || (p.Tool == "settings_change" && p.Action == "machine_rotate") {
		return "rotate token for " + machine
	}
	if p.Tool == "node_rename" || (p.Tool == "settings_change" && p.Action == "machine_name") {
		return fmt.Sprintf("rename %s to %s", machine, value("name"))
	}
	if p.Tool == "session_change" {
		return "session " + strings.ReplaceAll(p.Action, "_", " ")
	}
	return strings.TrimSpace(strings.ReplaceAll(p.Tool+" "+p.Action, "_", " "))
}

func inboxHarnessName(harness string) string {
	switch harness {
	case "pi":
		return "Pi"
	case "claude":
		return "Claude"
	case "opencode":
		return "OpenCode"
	case "codex":
		return "Codex"
	case "cursor":
		return "Cursor"
	}
	if harness == "" {
		return "session"
	}
	return strings.ToUpper(harness[:1]) + harness[1:]
}

// inboxMachineName is the machine name for a node ID. A name the node list
// does not know is shown as a plain phrase, never as an n_ identifier.
func (m Model) inboxMachineName(id string) string {
	if id == "" {
		return "a machine"
	}
	if name := m.nodeName(id, ""); name != "" {
		return name
	}
	if strings.HasPrefix(id, "n_") {
		return "a machine"
	}
	return id
}

// inboxNoticeParts names a connected app and says what it is.
func inboxNoticeParts(item protocol.InboxItem) (string, string) {
	text := appApprovalNoticeText(item.Text)
	if item.Node == "apps" {
		if name, summary, ok := inboxConnectedNotice(text); ok {
			return name, summary
		}
	}
	return "Notice", text
}

func inboxConnectedNotice(text string) (string, string, bool) {
	const prefix = "Connected: "
	if !strings.HasPrefix(text, prefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(text, prefix)
	name := rest
	if i := strings.IndexAny(rest, " ("); i > 0 {
		name = rest[:i]
	}
	if name == "" {
		name = "Connected app"
	}
	open := strings.Index(rest, "(")
	close := strings.Index(rest, ")")
	if open < 0 || close <= open {
		return name, "connected", true
	}
	parts := strings.Split(rest[open+1:close], ", ")
	summary := "connected"
	if kind := strings.TrimSpace(parts[0]); kind != "" {
		summary = "connected as a " + kind
	}
	domain := ""
	for _, p := range parts[1:] {
		if strings.Contains(p, ".") {
			domain = strings.TrimSpace(p)
		}
	}
	if domain != "" {
		summary += " (" + domain + ")"
	}
	return name, summary, true
}

// inboxItemSummary says what an approval or a question asks.
func inboxItemSummary(item protocol.InboxItem) string {
	return inboxFirstLine(item.Text)
}

// inboxWaitingSummary says that a session waits for the owner, after its
// name: "waits for you: <question>" (decision 0057). The words are short so
// the question keeps most of a narrow row.
func inboxWaitingSummary(w *protocol.Waiting) string {
	text := inboxFirstLine(w.Text)
	lead := "waits for you"
	if w.Kind == protocol.WaitingPermission {
		lead = "waits for your approval"
	}
	if text == "" {
		return lead
	}
	return lead + ": " + text
}

// inboxSessionName is the name the Inbox shows for a session.
func (m Model) inboxSessionName(id string) string {
	if a, ok := m.agentByID(id); ok {
		if name := m.needsName(a); name != "" {
			return name
		}
	}
	return id
}

// waitingCount is the number of items waiting for the owner.
func (m Model) waitingCount() int { return len(m.inboxRows()) }

// anySessionWaiting tells whether a table row reports a wait.
func (m Model) anySessionWaiting() bool {
	for _, a := range m.snap.Agents {
		if a.Waiting != nil && a.Worker == nil && !protocol.IsOwner(a.Harness) {
			return true
		}
	}
	return false
}

func (m Model) inboxSelected(rows []inboxRow) (inboxRow, bool) {
	if len(rows) == 0 {
		return inboxRow{}, false
	}
	return rows[min(max(m.inboxIndex, 0), len(rows)-1)], true
}

// inboxTabView draws the tab: a title, the three boxes, and a panel for the
// selected item. On a wide screen the panel sits on the right; on a narrow one
// it sits under the boxes.
func (m Model) inboxTabView(w, h int) []string {
	out := []string{m.titleLine(w)}
	if m.inboxError != "" {
		out = append(out, sBad.Render(clip("  "+m.inboxError, w)))
	}
	if m.inboxNote != "" {
		out = append(out, sDim.Render(clip("  "+m.inboxNote, w)))
	}
	rows := m.inboxRows()
	if len(rows) == 0 {
		for len(out) < h {
			out = append(out, "")
		}
		if h > 1 {
			out[1+max((h-2)/2, 0)] = sDim.Render(centerText("Nothing is waiting for you.", w))
		}
		return out[:max(h, 1)]
	}
	if m.inboxDetail {
		out = append(out, section("Inbox detail", w))
		row, _ := m.inboxSelected(rows)
		for _, line := range m.inboxDetailLines(row) {
			out = append(out, clip("  "+line, w))
		}
		for len(out) < h {
			out = append(out, "")
		}
		return out[:max(h, 1)]
	}

	avail := max(h-len(out), 1)
	if w >= 100 {
		panelW := min(max(w*2/5, 30), 48)
		leftW := max(w-panelW-1, 20)
		panelW = w - leftW - 1
		display, selected := m.inboxBoxes(leftW)
		panelH := min(avail, max(len(display), 12))
		left := windowLines(display, selected, panelH)
		panel := m.inboxPanel(panelW, panelH)
		for i := 0; i < panelH; i++ {
			var l, r string
			if i < len(left) {
				l = left[i]
			}
			if i < len(panel) {
				r = panel[i]
			}
			out = append(out, padStyled(l, leftW)+" "+padStyled(r, panelW))
		}
		for len(out) < h {
			out = append(out, "")
		}
		return out[:max(h, 1)]
	}

	display, selected := m.inboxBoxes(w)
	panelH := min(8, max(avail-min(len(display), avail), 0))
	if panelH < 3 && avail > 6 {
		panelH = 3
	}
	boxRoom := avail - panelH
	if boxRoom < 3 {
		boxRoom = avail
		panelH = 0
	}
	out = append(out, windowLines(display, selected, boxRoom)...)
	if panelH > 0 {
		out = append(out, m.inboxPanel(w, panelH)...)
	}
	for len(out) < h {
		out = append(out, "")
	}
	return out[:max(h, 1)]
}

// inboxBoxes draws the three boxes at width w and returns the line of the
// selected row.
func (m Model) inboxBoxes(w int) ([]string, int) {
	rows := m.inboxRows()
	selectedIndex := min(max(m.inboxIndex, 0), max(len(rows)-1, 0))
	innerW := max(w-4, 4)
	nameW := 12
	for _, row := range rows {
		nameW = max(nameW, ansi.StringWidth(sanitize(row.name)))
	}
	nameW = min(nameW, 20)
	var out []string
	selected := 0
	for _, box := range inboxBoxOrder {
		var index []int
		for i, row := range rows {
			if row.group.box() == box {
				index = append(index, i)
			}
		}
		if len(index) == 0 {
			continue
		}
		body := make([]string, 0, len(index))
		for _, i := range index {
			if i == selectedIndex {
				selected = len(out) + 1 + len(body)
			}
			body = append(body, m.inboxRowLine(rows[i], nameW, innerW, i == selectedIndex))
		}
		title := fmt.Sprintf("%s · %d", box.title(), len(index))
		out = append(out, panelWithBorder(title, body, w, box.color(), roleFaint)...)
	}
	return out, selected
}

// windowLines keeps the selected line in view and never returns more than
// room lines.
func windowLines(lines []string, selected, room int) []string {
	if room <= 0 || len(lines) == 0 {
		return nil
	}
	if len(lines) <= room {
		return lines
	}
	top := 0
	if selected >= room {
		top = selected - room + 1
	}
	top = min(max(top, 0), len(lines)-room)
	return lines[top : top+room]
}

func (m Model) inboxRowLine(row inboxRow, nameW, w int, selected bool) string {
	const ageW = 4
	marker := "  "
	if selected {
		marker = sAccent.Render("▸") + " "
	}
	name := cell(sanitize(row.name), nameW)
	age := sDim.Render(cellR(needsAge(m.now(), row.since), ageW))
	room := max(w-ansi.StringWidth(marker)-nameW-1-ageW-1, 4)
	text := cutWords(sanitize(row.text), room)
	if row.kind == "approval" || row.kind == "question" {
		tag := "[" + row.kind + "] "
		text = tag + cutWords(sanitize(row.text), max(room-ansi.StringWidth(tag), 0))
	}
	line := marker + name + " " + padStyled(text, room) + " " + age
	if selected {
		return selectedRow(padStyled(line, w))
	}
	return clip(line, w)
}

// cutWords cuts a summary to w columns on a word boundary.
func cutWords(s string, w int) string {
	s = sanitize(s)
	if w <= 0 {
		return ""
	}
	if ansi.StringWidth(s) <= w {
		return s
	}
	if w == 1 {
		return "…"
	}
	cut := ansi.Truncate(s, w-1, "")
	if i := strings.LastIndexByte(cut, ' '); i > 0 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ") + "…"
}

// inboxPanel draws the selected item in full. It never grows past h lines.
func (m Model) inboxPanel(w, h int) []string {
	row, ok := m.inboxSelected(m.inboxRows())
	if !ok || h < 3 {
		return nil
	}
	innerW := max(w-4, 4)
	innerH := h - 2
	body := m.inboxPanelBody(row, innerW, innerH)
	for len(body) < innerH {
		body = append(body, "")
	}
	if len(body) > innerH {
		body = body[:innerH]
	}
	return panelWithBorder(m.inboxPanelTitle(row), body, w, roleAccent, roleFaint)
}

func (m Model) inboxPanelBody(row inboxRow, w, h int) []string {
	// A short panel keeps the age on the origin line; a tall one gives it a
	// line of its own.
	withAge := h < 8
	text := wrapText(m.inboxPanelText(row), w)
	origin := wrapText(m.inboxPanelOrigin(row, withAge), w)
	var age []string
	if !withAge {
		age = wrapText(inboxAgeText(m.now(), row.since), w)
	}
	keys := hintLines(m.inboxItemHints(row), w)
	build := func(blank bool) []string {
		var b []string
		b = append(b, text...)
		if blank && len(text) > 0 {
			b = append(b, "")
		}
		b = append(b, origin...)
		b = append(b, age...)
		if blank && len(origin)+len(age) > 0 {
			b = append(b, "")
		}
		return append(b, keys...)
	}
	body := build(true)
	if len(body) <= h {
		return body
	}
	body = build(false)
	for len(body) > h && len(text) > 1 {
		text = text[:len(text)-1]
		body = build(false)
	}
	for len(body) > h && len(origin) > 1 {
		origin = origin[:len(origin)-1]
		body = build(false)
	}
	if len(body) > h {
		body = body[len(body)-h:]
	}
	return body
}

// hintLines wraps the item's keys into the panel width.
func hintLines(hs []hint, w int) []string {
	if len(hs) == 0 {
		return nil
	}
	var out []string
	var line []hint
	width := 0
	for _, h := range hs {
		part := ansi.StringWidth(h.key + " " + h.label)
		if len(line) > 0 && width+2+part > w {
			out = append(out, keyHints(hintParts(line)...))
			line, width = nil, 0
		}
		if len(line) > 0 {
			width += 2
		}
		line = append(line, h)
		width += part
	}
	if len(line) > 0 {
		out = append(out, keyHints(hintParts(line)...))
	}
	return out
}

func (m Model) inboxPanelTitle(row inboxRow) string {
	switch row.kind {
	case "held":
		return m.inboxFromName(row.held) + " held a request"
	case "approval":
		return sanitize(row.name) + " wants approval"
	case "question":
		return sanitize(row.name) + " asks"
	case "waiting":
		return sanitize(row.name) + " waits for you"
	case "outcome":
		return sanitize(row.outcome.From) + " → " + sanitize(row.outcome.To)
	case "learned":
		return sanitize(row.learned.Node) + " (learned)"
	case "proposal":
		return "CTO proposal"
	case "notice":
		if row.name == "Notice" {
			return "Notice"
		}
		return sanitize(row.name) + " connected"
	}
	return sanitize(row.name)
}

func (m Model) inboxPanelText(row inboxRow) string {
	switch row.kind {
	case "held":
		return sanitizeBody(row.held.Body)
	case "outcome":
		text := inboxOutcomeSummary(row.outcome)
		if row.outcome.Recommendation != "" {
			text += "\nRecommendation: " + sanitize(row.outcome.Recommendation)
		}
		return text
	case "learned":
		text := learnedQuestion(row.learned)
		for i, e := range row.learned.Evidence {
			if i > 0 && e.Excerpt != "" {
				text += "\nBecause: " + sanitize(e.Excerpt)
			}
		}
		return text
	case "proposal":
		text := sanitize(row.text)
		if row.proposal.Reason != "" {
			text = sanitize(row.proposal.Reason) + "\n" + text
		}
		return text
	case "notice":
		return sanitize(row.item.Text)
	case "approval", "question":
		text := sanitize(row.item.Text)
		if row.item.Recommendation != "" {
			text += "\nRecommendation: " + sanitize(row.item.Recommendation)
		}
		return text
	case "waiting":
		if a, ok := m.agentByID(row.session); ok && a.Waiting != nil && a.Waiting.Text != "" {
			return sanitize(a.Waiting.Text)
		}
	}
	return sanitize(row.text)
}

func (m Model) inboxPanelOrigin(row inboxRow, withAge bool) string {
	var parts []string
	add := func(s string) {
		if s = strings.TrimSpace(s); s != "" {
			parts = append(parts, s)
		}
	}
	switch row.kind {
	case "held":
		add("from " + m.inboxFromName(row.held))
		add(m.inboxSessionOrigin(row.held.To))
	case "approval", "question", "waiting":
		add(m.inboxSessionOrigin(row.session))
	case "outcome":
		add("edge " + sanitize(row.outcome.From) + " → " + sanitize(row.outcome.To))
	case "learned":
		add("learned on " + sanitize(row.learned.Node))
	case "proposal":
		add("CTO · " + m.inboxMachineName(row.proposal.NodeID))
	case "notice":
		add("connected app")
		if row.item.SessionID != "" {
			add(m.inboxSessionOrigin(row.item.SessionID))
		}
	}
	if withAge {
		add(inboxAgeText(m.now(), row.since))
	}
	return strings.Join(parts, " · ")
}

func (m Model) inboxFromName(msg protocol.Message) string {
	if msg.FromName != "" {
		return sanitize(msg.FromName)
	}
	return sanitize(m.inboxSessionName(msg.From))
}

func (m Model) inboxSessionOrigin(id string) string {
	origin := "session " + sanitize(m.inboxSessionName(id))
	if a, ok := m.agentByID(id); ok {
		project := firstNonEmpty(a.Repo.Project, a.Profile.Project)
		machine := m.nodeName(a.NodeID, a.Hostname)
		switch {
		case project != "" && machine != "":
			origin += " · " + sanitize(project) + " on " + sanitize(machine)
		case project != "":
			origin += " · " + sanitize(project)
		case machine != "":
			origin += " · on " + sanitize(machine)
		}
	}
	return origin
}

// inboxAgeText says how long ago an item arrived, in words.
func inboxAgeText(now time.Time, since int64) string {
	if since <= 0 {
		return "just now"
	}
	d := now.Sub(time.Unix(since, 0))
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		n := int(d.Minutes())
		if n == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", n)
	case d < 24*time.Hour:
		n := int(d.Hours())
		if n == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", n)
	}
	n := int(d.Hours() / 24)
	if n == 1 {
		return "1 day ago"
	}
	return fmt.Sprintf("%d days ago", n)
}

// wrapText wraps sanitized text to w columns on word boundaries.
func wrapText(s string, w int) []string {
	if w <= 0 {
		return nil
	}
	var out []string
	for _, line := range strings.Split(sanitizeBody(s), "\n") {
		line = strings.TrimRight(line, " ")
		if line == "" {
			out = append(out, "")
			continue
		}
		for ansi.StringWidth(line) > w {
			cut := ansi.Truncate(line, w, "")
			if i := strings.LastIndexByte(cut, ' '); i > 0 {
				out = append(out, strings.TrimRight(cut[:i], " "))
				line = strings.TrimLeft(line[len(cut[:i]):], " ")
			} else {
				out = append(out, cut)
				line = line[len(cut):]
			}
		}
		out = append(out, line)
	}
	for len(out) > 0 && out[0] == "" {
		out = out[1:]
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

func hintParts(hs []hint) []string {
	var parts []string
	for _, h := range hs {
		parts = append(parts, h.key, h.label)
	}
	return parts
}

func centerText(s string, w int) string {
	s = sanitize(s)
	return strings.Repeat(" ", max((w-ansi.StringWidth(s))/2, 0)) + s
}

func (m Model) inboxDetailLines(row inboxRow) []string {
	var out []string
	switch row.kind {
	case "held":
		out = append(out, "From: "+sanitize(row.held.FromName), "To: "+sanitize(m.inboxSessionName(row.held.To)), "", sanitizeBody(row.held.Body))
	case "outcome":
		out = append(out, "From: "+sanitize(row.outcome.From), "To: "+sanitize(row.outcome.To),
			"Request: "+sanitize(row.outcome.Request), "Outcome: "+sanitize(row.outcome.Outcome),
			"Reason: "+sanitize(row.outcome.Reason))
		if row.outcome.Recommendation != "" {
			out = append(out, "Recommendation: "+sanitize(row.outcome.Recommendation))
		}
	case "learned":
		out = append(out, "Node: "+sanitize(row.learned.Node), sanitize(row.text))
		for _, e := range row.learned.Evidence {
			if e.Excerpt != "" {
				out = append(out, "Because: "+sanitize(e.Excerpt))
			}
		}
	case "proposal":
		out = append(out, sanitize(row.text))
	case "notice", "approval", "question":
		out = append(out, "Kind: "+sanitize(row.item.Kind), "Status: "+sanitize(row.item.Status),
			"Session: "+sanitize(row.item.SessionID), "Node: "+sanitize(row.item.Node),
			"Text: "+sanitize(row.item.Text))
		if row.item.Recommendation != "" {
			out = append(out, "Recommendation: "+sanitize(row.item.Recommendation))
		}
		if row.item.MessageID != "" {
			out = append(out, "Message: "+sanitize(row.item.MessageID))
		}
		if row.item.ConversationID != "" {
			out = append(out, "Conversation: "+sanitize(row.item.ConversationID))
		}
		if row.item.Answer != "" {
			out = append(out, "Answer: "+sanitize(row.item.Answer))
		}
	case "waiting":
		out = append(out, "Session: "+sanitize(m.inboxSessionName(row.session)), sanitize(row.text))
	}
	return out
}

type inboxAnswerMsg struct {
	id  string
	err error
}

func (m Model) inboxAnswer(id, answer string) tea.Cmd {
	c, ok := m.src.(inboxCaller)
	return func() tea.Msg {
		if !ok {
			return inboxAnswerMsg{id, fmt.Errorf("inbox unavailable")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), Timeout)
		defer cancel()
		_, err := c.Call(ctx, "inbox_answer", map[string]string{"id": id, "answer": answer})
		return inboxAnswerMsg{id, err}
	}
}

// inboxGone tells whether an Inbox action failed because the gateway no
// longer has the item open. The item left the list while it waited, for
// example a notice the gateway closed itself, so the row is dropped rather
// than kept.
func inboxGone(err error) bool {
	if err == nil {
		return false
	}
	text := err.Error()
	return text == "inbox item not found"
}

// dropInboxItem removes an item the gateway no longer has open from the local
// list, so the next draw cannot show a row whose action will never succeed.
func (m *Model) dropInboxItem(id string) {
	kept := make([]protocol.InboxItem, 0, len(m.inboxItems))
	for _, item := range m.inboxItems {
		if item.ID != id {
			kept = append(kept, item)
		}
	}
	m.inboxItems = kept
	m.inboxIndex = min(max(m.inboxIndex, 0), max(m.waitingCount()-1, 0))
}

// inboxTabKey handles the Inbox tab and its detail view.
func (m Model) inboxTabKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	rows := m.inboxRows()
	if m.inboxEditing {
		switch k.Code {
		case tea.KeyEsc:
			m.inboxEditing = false
			m.inboxDeliveryID = ""
			return m, nil
		case tea.KeyEnter:
			answer := strings.TrimSpace(m.inboxInput.Value())
			if answer != "" {
				row, ok := m.inboxSelected(rows)
				if ok {
					if m.inboxDeliveryID != "" {
						if row.item.ID != m.inboxDeliveryID || row.item.Delivery == nil {
							m.inboxEditing = false
							m.inboxDeliveryID = ""
							m.fail("The delivery item changed; select it again.")
							return m, nil
						}
						var fields map[string]string
						if json.Unmarshal([]byte(answer), &fields) != nil || fields == nil {
							m.fail("Enter evidence as a JSON object of strings.")
							return m, nil
						}
						for _, field := range row.item.Delivery.ReportFields {
							if strings.TrimSpace(fields[field]) == "" {
								m.fail("Enter the required evidence field " + field + ".")
								return m, nil
							}
						}
						answer = "approve " + answer
						m.inboxDeliveryID = ""
					}
					m.inboxEditing = false
					return m, m.inboxAnswer(row.item.ID, answer)
				}
			}
			return m, nil
		}
		var cmd tea.Cmd
		m.inboxInput, cmd = m.inboxInput.Update(k)
		return m, cmd
	}
	key := keyAction(contextInbox, k.String())
	switch key {
	case actQuit:
		m.quitting = true
		return m, tea.Quit
	case "help":
		m.openHelp()
		return m, nil
	}
	if m.inboxDetail {
		if key == "back" || key == actClose {
			m.inboxDetail = false
		}
		return m, nil
	}
	switch key {
	case actUp:
		m.inboxIndex = max(0, m.inboxIndex-1)
		return m, nil
	case actDown:
		m.inboxIndex = min(max(len(rows)-1, 0), m.inboxIndex+1)
		return m, nil
	case actPageUp:
		m.inboxIndex = max(0, m.inboxIndex-max(len(rows), 1))
		return m, nil
	case actPageDown:
		m.inboxIndex = min(max(len(rows)-1, 0), m.inboxIndex+max(len(rows), 1))
		return m, nil
	case actFirst:
		m.inboxIndex = 0
		return m, nil
	case actLast:
		m.inboxIndex = max(len(rows)-1, 0)
		return m, nil
	}
	row, ok := m.inboxSelected(rows)
	if !ok {
		return m, nil
	}
	switch key {
	case "details":
		switch row.kind {
		case "held", "waiting":
			m.selected = row.session
			return m.startEnterID(row.session)
		case "proposal":
			m.proposalDetail = &proposalDetail{proposal: row.proposal, loading: true}
			return m, m.proposalDetailCmd(row.proposal)
		default:
			m.inboxDetail = true
		}
	case "approve", "refuse":
		return m.inboxDecide(row, key == "approve")
	case "let stand", "overrule":
		action := "let_stand"
		if key == "overrule" {
			action = "overrule"
		}
		return m, m.inboxResolveOutcome(row, action)
	case "approve all":
		if row.kind == "proposal" {
			m.openProposalConfirm()
		}
	case "answer":
		if row.kind == "approval" || row.kind == "question" {
			m.inboxInput = newTextInput()
			m.inboxInput.CharLimit = protocol.MaxBodyBytes
			m.inboxInput.Focus()
			m.inboxEditing = true
		}
	case "dismiss":
		if row.kind == "notice" || row.kind == "approval" || row.kind == "question" {
			m.inboxError = ""
			m.inboxNote = ""
			return m, m.inboxAnswer(row.item.ID, "dismiss")
		}
	}
	return m, nil
}

func (m Model) inboxDecide(row inboxRow, approve bool) (tea.Model, tea.Cmd) {
	switch row.kind {
	case "held":
		answerer, ok := m.src.(interface {
			AnswerHeld(context.Context, protocol.HeldAnswer) error
		})
		if !ok {
			m.fail("Held request actions unavailable.")
			return m, nil
		}
		action := "a"
		if !approve {
			action = "r"
		}
		answer := protocol.HeldAnswer{MessageID: row.held.ID, Action: action}
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), Timeout)
			defer cancel()
			if err := answerer.AnswerHeld(ctx, answer); err != nil {
				return actionMsg{err: err}
			}
			return actionMsg{text: "Request answered."}
		}
	case "learned":
		c, ok := m.src.(inboxCaller)
		if !ok {
			m.fail("Learned suggestion actions unavailable.")
			return m, nil
		}
		action := "accept"
		if !approve {
			action = "reject"
		}
		id := row.learned.ID
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), Timeout)
			defer cancel()
			_, err := c.Call(ctx, "learned_answer", map[string]any{"suggestion_id": id, "action": action})
			if err != nil {
				return actionMsg{err: err}
			}
			return actionMsg{text: "Suggestion " + action + "ed."}
		}
	case "proposal":
		action := "a"
		if !approve {
			action = "x"
		}
		return m, m.proposalAnswerCmd(row.proposal, action)
	case "approval", "question":
		if approve && row.item.Delivery != nil && len(row.item.Delivery.ReportFields) != 0 {
			fields := map[string]string{}
			for _, field := range row.item.Delivery.ReportFields {
				fields[field] = ""
			}
			value, _ := json.Marshal(fields)
			m.inboxInput = newTextInput()
			m.inboxInput.Prompt = "evidence JSON> "
			m.inboxInput.CharLimit = protocol.MaxBodyBytes
			m.inboxInput.SetValue(string(value))
			m.inboxDeliveryID = row.item.ID
			m.inboxEditing = true
			return m, m.inboxInput.Focus()
		}
		answer := "approve"
		if !approve {
			answer = "refuse"
		}
		return m, m.inboxAnswer(row.item.ID, answer)
	}
	return m, nil
}

func (m Model) inboxResolveOutcome(row inboxRow, action string) tea.Cmd {
	src := m.src
	id := row.outcome.ReplyID
	return func() tea.Msg {
		resolver, ok := src.(outcomeResolver)
		if !ok {
			return actionMsg{err: fmt.Errorf("outcome decisions unavailable")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), Timeout)
		defer cancel()
		if err := resolver.ResolveOutcome(ctx, id, action); err != nil {
			return actionMsg{err: err}
		}
		return actionMsg{text: "Outcome: " + action}
	}
}

// inboxItemHints lists only the keys that act on the selected row. A key
// that acts on every proposal or on the whole inbox is not listed here.
func (m Model) inboxItemHints(row inboxRow) []hint {
	switch row.kind {
	case "held":
		open := bindingHint(contextInbox, "details")
		open.label = "open session"
		return []hint{open, bindingHint(contextInbox, "approve"), bindingHint(contextInbox, "refuse")}
	case "waiting":
		open := bindingHint(contextInbox, "details")
		open.label = "open session"
		return []hint{open}
	case "question":
		return []hint{bindingHint(contextInbox, "answer"), bindingHint(contextInbox, "dismiss"), bindingHint(contextInbox, "details")}
	case "approval":
		return []hint{bindingHint(contextInbox, "approve"), bindingHint(contextInbox, "refuse"), bindingHint(contextInbox, "dismiss"), bindingHint(contextInbox, "details")}
	case "proposal":
		return []hint{bindingHint(contextInbox, "approve"), bindingHint(contextInbox, "refuse")}
	case "outcome":
		return []hint{bindingHint(contextInbox, "let stand"), bindingHint(contextInbox, "overrule")}
	case "learned":
		return []hint{{key: "a", label: "accept", prio: 1}, {key: "r", label: "reject", prio: 1}}
	case "notice":
		return []hint{bindingHint(contextInbox, "dismiss")}
	}
	return nil
}

// inboxHints is the bottom key line: the keys that act on the selected row,
// then the movement and help keys.
func (m Model) inboxHints() []hint {
	help := bindingHint(contextInbox, "help")
	if m.inboxDetail {
		return []hint{bindingHint(contextInbox, "back"), help, bindingHint(contextInbox, actQuit)}
	}
	move := hint{key: "↑↓", label: "select", prio: 1}
	rows := m.inboxRows()
	row, ok := m.inboxSelected(rows)
	if !ok {
		return []hint{bindingPairHint(contextInbox, actUp, actDown), help, bindingHint(contextInbox, actQuit)}
	}
	return append(m.inboxItemHints(row), move, help)
}

// inboxCaller is the local call the Inbox and the CTO box need.
type inboxCaller interface {
	Call(context.Context, string, any) (json.RawMessage, error)
}

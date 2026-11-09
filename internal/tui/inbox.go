package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"image/color"
	"sort"
	"strconv"
	"strings"
	"sync"
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

// box is the one list the owner answers from. Every ask waits for the owner
// in the same "Needs you" section (decision 0090); what moves along without
// them is the In flight section.
func (g inboxGroup) box() inboxBox { return inboxBoxAnswer }

// inboxBox is a section of asks. After decision 0090 there is only one.
type inboxBox int

const inboxBoxAnswer inboxBox = 0

var inboxBoxOrder = []inboxBox{inboxBoxAnswer}

func (b inboxBox) title() string { return "Needs you" }

func (b inboxBox) color() color.Color { return roleWarning }

// inboxRow is one ask. kind names the row's actions, not its group: held,
// outcome, learned, proposal, approval, question, step, notice or waiting (a
// session that reports a wait). The older structs are filled from the ask so
// that the panels and keys that read them keep their words.
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
	ask      protocol.Ask
}

// askItem is the Inbox item an ask stands for.
func askItem(a protocol.Ask) protocol.InboxItem {
	item := protocol.InboxItem{
		ID: a.ID, Kind: a.Kind, SessionID: a.FromSession, Node: a.Node, Text: a.Question, Options: a.Options,
		AskerName: a.From, Recommendation: a.Recommendation, MessageID: a.MessageID, ConversationID: a.ConversationID,
		CreatedAt: a.CreatedAt, Status: "open", Delivery: a.Delivery, SourceRef: a.Ref, Detail: a.Detail,
	}
	if a.AboutKind == protocol.AskAboutChange {
		item.ChangeTitle = a.AboutTitle
	}
	return item
}

// askRefID is the part of a source key after its prefix, "learned:12" -> "12".
func askRefID(ref, prefix string) string { return strings.TrimPrefix(ref, prefix) }

// askRow makes the row of one ask. It is false for an ask that is not the
// owner's to answer now, and for asks of sessions the owner hides.
func (m Model) askRow(a protocol.Ask) (inboxRow, bool) {
	if a.State == protocol.AskWithSomeone {
		return inboxRow{}, false
	}
	since := a.CreatedAt.Unix()
	item := askItem(a)
	row := inboxRow{since: since, item: item, ask: a, session: a.FromSession}
	switch a.Kind {
	case protocol.AskKindHeld:
		recipient := a.AboutID
		if name := m.inboxSessionName(a.AboutID); name != a.AboutID {
			recipient = name
		}
		row.group, row.key, row.kind = inboxHeld, "held:"+a.MessageID, "held"
		row.name, row.text, row.session = firstNonEmpty(a.From, a.FromSession), "held request → "+recipient, a.AboutID
		row.held = protocol.Message{ID: a.MessageID, From: a.FromSession, FromName: a.From, To: a.AboutID, Body: a.Detail, CreatedAt: a.CreatedAt}
	case protocol.AskKindRejection, protocol.AskKindEscalation:
		outcome := "reject"
		if a.Kind == protocol.AskKindEscalation {
			outcome = "escalate"
		}
		request, reason := a.Question, ""
		if first, rest, ok := strings.Cut(a.Question, "\nReason: "); ok {
			request, reason = strings.TrimPrefix(first, "Request: "), rest
		}
		row.outcome = protocol.OutcomeInboxEntry{ReplyID: a.MessageID, From: a.From, To: a.Node, Request: request, Outcome: outcome, Reason: reason, Recommendation: a.Recommendation, At: since * 1e9}
		row.group, row.key, row.kind = inboxOutcome, "outcome:"+a.MessageID, "outcome"
		row.name, row.text = a.From+" → "+a.Node, inboxOutcomeSummary(row.outcome)
		if a.Node == "" {
			row.name = a.From
		}
	case protocol.AskKindDraft:
		// The ask of a node holds all its drafted fields, and an answer applies to every one.
		if strings.HasPrefix(a.Ref, "drafts:") {
			row.learned = protocol.LearnedSuggestion{Node: a.AboutID, Status: "pending", Severity: "major", Origin: "draft"}
			row.group, row.key, row.kind = inboxLearned, "learned:"+a.ID, "learned"
			row.name, row.text = a.AboutID+" (draft)", inboxFirstLine(a.Question)
			break
		}
		fallthrough
	case protocol.AskKindLearned:
		id, _ := strconv.ParseInt(askRefID(a.Ref, a.Kind+":"), 10, 64)
		suffix, origin := " (learned)", ""
		if a.Kind == protocol.AskKindDraft {
			id, suffix, origin = -id, " (draft)", "draft"
		}
		row.learned = protocol.LearnedSuggestion{ID: id, Node: a.AboutID, Status: "pending", Severity: "major", Origin: origin}
		row.group, row.key, row.kind = inboxLearned, fmt.Sprintf("learned:%d", id), "learned"
		row.name, row.text = a.AboutID+suffix, inboxFirstLine(a.Question)
	case protocol.AskKindProposal:
		p := protocol.CTOProposal{ID: a.Ref, Reason: a.Detail, CreatedAt: a.CreatedAt, Status: "open"}
		if node, id, ok := strings.Cut(strings.TrimPrefix(a.Ref, "proposal:"), ":"); ok {
			p.NodeID, p.ID = node, id
		}
		row.text = inboxFirstLine(a.Question)
		for _, known := range m.proposals {
			if known.ID == p.ID && (known.NodeID == p.NodeID || p.NodeID == "") && known.Status == "open" {
				p = known
				row.text = m.inboxProposalSummary(p)
			}
		}
		row.group, row.key, row.kind, row.name, row.proposal = inboxProposal, proposalKey(p), "proposal", "CTO", p
	case protocol.AskKindApproval, protocol.AskKindQuestion, protocol.AskKindPermission:
		kind := a.Kind
		if kind == protocol.AskKindPermission {
			// A permission ask has the same actions as an approval: the
			// numbered options below it are Approve and Reject.
			kind = "approval"
		}
		row.group, row.key, row.kind = inboxApproval, "inbox:"+a.ID, kind
		row.name, row.text = m.inboxAskerName(item), inboxItemSummary(item)
	case protocol.AskKindStep:
		row.group, row.key, row.kind = inboxApproval, "step:"+a.ID, "step"
		row.name, row.text = firstNonEmpty(a.AboutTitle, a.From), inboxFirstLine(a.Question)
	case protocol.AskKindWaiting:
		row.group, row.key, row.kind = inboxApproval, "waiting:"+a.FromSession, "waiting"
		row.name, row.text = m.inboxSessionName(a.FromSession), inboxWaitingText(a)
	case protocol.AskKindCheck, protocol.AskKindStall:
		name, text := inboxNoticeParts(item)
		if a.Kind == protocol.AskKindStall {
			name = m.inboxSessionName(a.FromSession)
		}
		row.group, row.key, row.kind = inboxNotice, "notice:"+a.ID, "notice"
		row.name, row.text = name, text
	case protocol.AskKindBrief:
		// One informational item a day, labelled with its owner-local date.
		row.group, row.key, row.kind = inboxNotice, "brief:"+a.ID, "brief"
		row.name, row.text = firstNonEmpty(a.From, "Daily brief"), inboxFirstLine(a.Question)
	default:
		return inboxRow{}, false
	}
	if !m.askSessionShown(a) {
		return inboxRow{}, false
	}
	return row, true
}

// inboxWaitingText says that a session waits for the owner, after its name.
func inboxWaitingText(a protocol.Ask) string {
	lead := "waits for you"
	if a.Detail == protocol.WaitingPermission {
		lead = "waits for your approval"
	}
	if text := inboxFirstLine(a.Question); text != "" {
		return lead + ": " + text
	}
	return lead
}

// askSessionShown keeps the owner's hide, junk, offline and filter choices for
// the asks that a session raised. An ask whose session the screen does not know
// is shown.
func (m Model) askSessionShown(a protocol.Ask) bool {
	// Only a wait that the session reports follows these choices, as it did
	// before the Desk: a question or an approval stays until it is answered.
	if a.Kind != protocol.AskKindWaiting {
		return true
	}
	if a.FromKind != "" && a.FromKind != protocol.AskFromSession {
		return true
	}
	ag, ok := m.agentByID(a.FromSession)
	if !ok {
		return true
	}
	if protocol.IsOwner(ag.Harness) {
		return true
	}
	if m.hiddenSessions[ag.SessionID] && !m.showHiddenSessions {
		return false
	}
	if isJunkSession(ag) && !m.showJunk() {
		return false
	}
	if (!ag.Online || ag.Status == protocol.StatusDormant) && !m.showOffline {
		return false
	}
	return matches(ag, m.filter)
}

type deskCacheState struct {
	sync.Mutex
	fingerprint uint64
	rows        []inboxRow
	maxNameW    int
	rowLines    map[inboxRowLineKey]string
	whyLines    map[inboxWhyLineKey]string
}

type inboxRowLineKey struct {
	name  string
	text  string
	kind  string
	nameW int
	w     int
	age   string
}

type inboxWhyLineKey struct {
	whyYou   string
	priority int
	w        int
}

// deskAsksFingerprint computes a cheap, 0-alloc 64-bit FNV-1a hash over all asks,
// proposals, filter state, snapshot version, and asksVersion.
func deskAsksFingerprint(m Model) uint64 {
	h := uint64(14695981039346656037)
	const prime = 1099511628211
	hashStr := func(s string) {
		for i := 0; i < len(s); i++ {
			h = (h ^ uint64(s[i])) * prime
		}
	}
	hashInt := func(v int64) {
		h = (h ^ uint64(v)) * prime
	}
	hashBool := func(b bool) {
		v := uint64(0)
		if b {
			v = 1
		}
		h = (h ^ v) * prime
	}

	hashInt(m.asksVersion)
	hashInt(int64(len(m.asks)))
	hashInt(int64(len(m.proposals)))
	hashStr(m.filter)
	hashBool(m.showHiddenSessions)
	hashBool(m.showOffline)
	hashStr(m.snap.Version)

	for _, a := range m.asks {
		hashStr(a.ID)
		hashStr(a.State)
		hashStr(a.Kind)
		hashStr(a.Question)
		hashStr(a.WhyYou)
		hashInt(int64(a.Priority))
		hashStr(a.From)
		hashStr(a.FromSession)
		hashStr(a.AboutID)
		hashStr(a.AboutTitle)
		hashStr(a.Node)
		hashStr(a.Ref)
		hashStr(a.MessageID)
		hashStr(a.Detail)
		hashStr(a.Recommendation)
		hashInt(a.CreatedAt.UnixNano())
		if a.Delivery != nil {
			hashStr(a.Delivery.ChangeID)
		}
	}

	for _, p := range m.proposals {
		hashStr(p.ID)
		hashStr(p.NodeID)
		hashStr(p.Status)
		hashStr(p.Reason)
	}

	return h
}

// inboxRows builds the Needs you rows from the one list of asks.
func (m Model) inboxRows() []inboxRow {
	fp := deskAsksFingerprint(m)
	c := m.deskCache
	if c != nil {
		c.Lock()
		if c.rows != nil && c.fingerprint == fp {
			rows := c.rows
			c.Unlock()
			return rows
		}
		c.Unlock()
	}

	var rows []inboxRow
	for _, a := range m.asks {
		if row, ok := m.askRow(a); ok {
			rows = append(rows, row)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rankI, rankJ := inboxKindRank(rows[i]), inboxKindRank(rows[j]); rankI != rankJ {
			return rankI < rankJ
		}
		if rows[i].since != rows[j].since {
			return rows[i].since > rows[j].since
		}
		return rows[i].key < rows[j].key
	})

	nameW := 12
	for _, row := range rows {
		nameW = max(nameW, ansi.StringWidth(sanitize(row.name)))
	}
	nameW = min(nameW, 20)

	if c != nil {
		c.Lock()
		c.fingerprint = fp
		c.rows = rows
		c.maxNameW = nameW
		if len(c.rowLines) > 500 {
			c.rowLines = make(map[inboxRowLineKey]string)
		}
		if len(c.whyLines) > 500 {
			c.whyLines = make(map[inboxWhyLineKey]string)
		}
		c.Unlock()
	}
	return rows
}

// inboxKindRank orders the rows: what needs an answer comes first, then what
// proposes a change, then what only informs.
func inboxKindRank(row inboxRow) int {
	switch row.kind {
	case "question":
		return 0
	case "approval":
		return 1
	case "step":
		return 2
	case "waiting":
		return 3
	case "held":
		return 4
	case "outcome":
		return 5
	case "proposal":
		return 6
	case "learned":
		return 7
	case "notice", "brief":
		return 8
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

// inboxOutcomeParty is the side a refusal blocked: the node that asked.
func inboxOutcomeParty(row inboxRow) string {
	if s := strings.TrimSpace(sanitize(row.outcome.To)); s != "" {
		return s
	}
	return "the requester"
}

// inboxOutcomeRequest is the request the refusal blocked, short enough to sit
// on a key line.
func inboxOutcomeRequest(row inboxRow) string {
	s := strings.TrimSpace(sanitize(row.outcome.Request))
	if s == "" {
		return "the request"
	}
	return cutWords(s, 44)
}

// inboxOutcomeChoiceLabels say what each outcome key does, with the names and
// the request filled in. Each label stays short enough for the two keys to
// share one 80 column line.
func inboxOutcomeChoiceLabels(row inboxRow) (string, string) {
	const labelMax = 37
	party, request := inboxOutcomeParty(row), inboxOutcomeRequest(row)
	keep := "Keep refusal: " + party + " denied " + request
	if row.outcome.Outcome == "escalate" {
		keep = "Keep escalation: " + party + " denied " + request
	}
	return cutWords(keep, labelMax), cutWords("Overrule: "+party+" gets "+request, labelMax)
}

// inboxOutcomeAsked says who asked whom for what.
func inboxOutcomeAsked(row inboxRow) string {
	requester := firstNonEmpty(sanitize(row.outcome.To), "an agent")
	refuser := firstNonEmpty(sanitize(row.outcome.From), "another agent")
	request := firstNonEmpty(sanitize(row.outcome.Request), "a request")
	return requester + " asked " + refuser + ": " + request
}

// inboxOutcomeNote says who refused and why, in the refuser's own words.
func inboxOutcomeNote(row inboxRow) string {
	who := firstNonEmpty(sanitize(row.outcome.From), "The other side")
	line := who + " refused this request"
	if row.outcome.Outcome == "escalate" {
		line = who + " escalated this request"
	}
	if reason := strings.TrimSpace(sanitize(row.outcome.Reason)); reason != "" {
		line += ": \"" + reason + "\""
	}
	return line
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
	if item.Node == "cto" {
		return "CTO", item.Text
	}
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

func (m Model) inboxAskerName(item protocol.InboxItem) string {
	if item.AskerName != "" && item.AskerName != item.SessionID && !strings.HasPrefix(item.AskerName, "s_") {
		return item.AskerName
	}
	if a, ok := m.agentByID(item.SessionID); ok {
		name := firstNonEmpty(a.Profile.Name, a.Title)
		if name != "" && name != item.SessionID && !strings.HasPrefix(name, "s_") {
			return name
		}
	}
	return "an agent"
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
func (m Model) waitingCount() int { return m.askRowCount() }

// anySessionWaiting tells whether a table row reports a wait.
func (m Model) anySessionWaiting() bool {
	for _, a := range m.snap.Agents {
		if a.Waiting != nil && a.Worker == nil && !protocol.IsOwner(a.Harness) {
			return true
		}
	}
	return false
}

// inboxSelected is the ask under the cursor. It is false when the cursor is on
// a change or a log line.
func (m Model) inboxSelected(rows []inboxRow) (inboxRow, bool) {
	idx := min(max(m.inboxIndex, 0), max(m.deskRowCount()-1, 0))
	if idx >= len(rows) {
		return inboxRow{}, false
	}
	return rows[idx], true
}

func (m Model) inboxQuestionHasExtra(row inboxRow, rows []inboxRow) bool {
	if len(row.item.Options) > 0 || row.item.Recommendation != "" || row.item.ChangeTitle != "" || strings.Contains(row.item.Text, "\n") {
		return true
	}
	width := m.width
	if width >= 100 {
		width -= min(max(width*2/5, 30), 48) + 1
	}
	nameW := 12
	for _, r := range rows {
		nameW = max(nameW, ansi.StringWidth(sanitize(r.name)))
	}
	nameW = min(nameW, 20)
	room := max(max(width-4, 4)-2-nameW-1-4-1, 4)
	room = max(room-ansi.StringWidth("["+row.kind+"] "), 0)
	return cutWords(sanitize(row.text), room) != sanitize(row.text)
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
	case "step":
		return sanitize(row.name) + " needs you"
	case "waiting":
		return sanitize(row.name) + " waits for you"
	case "outcome":
		return sanitize(row.outcome.From) + " → " + sanitize(row.outcome.To)
	case "learned":
		return sanitize(row.learned.Node) + " (learned)"
	case "proposal":
		return "CTO proposal"
	case "notice":
		if row.item.Kind == "session_stall" {
			return sanitize(row.name) + " may be stalled"
		}
		if row.name == "Notice" {
			return "Notice"
		}
		return sanitize(row.name) + " connected"
	case "brief":
		return sanitize(firstNonEmpty(row.name, "Daily brief"))
	}
	return sanitize(row.name)
}

func (m Model) inboxPanelText(row inboxRow) string {
	switch row.kind {
	case "held":
		return sanitizeBody(row.held.Body)
	case "outcome":
		text := inboxOutcomeAsked(row) + " · " + inboxOutcomeNote(row)
		if row.outcome.Recommendation != "" {
			text += "\nRecommendation: " + sanitize(row.outcome.Recommendation)
		}
		return text
	case "learned":
		text := sanitize(row.item.Text)
		if row.ask.Detail != "" {
			text += "\n" + sanitize(row.ask.Detail)
		}
		return text
	case "step":
		text := sanitize(row.item.Text)
		if row.ask.Detail != "" && row.ask.Detail != row.ask.Question {
			text = sanitizeBody(row.ask.Detail)
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
	case "brief":
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
	case "approval", "question":
		origin := m.inboxSessionOrigin(row.session)
		name := m.inboxSessionName(row.session)
		if name == row.session || strings.Contains(name, "s_") {
			origin = "session " + m.inboxAskerName(row.item)
		}
		add(origin)
	case "waiting":
		add(m.inboxSessionOrigin(row.session))
	case "outcome":
		add("edge " + sanitize(row.outcome.From) + " → " + sanitize(row.outcome.To))
	case "learned":
		add(sanitize(strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(row.name, " (learned)"), " (draft)"))) + " charter")
	case "step":
		add("change " + sanitize(row.ask.AboutID))
	case "proposal":
		add("CTO · " + m.inboxMachineName(row.proposal.NodeID))
	case "notice":
		if row.ask.FromKind == protocol.AskFromDisk {
			add("disk check")
		} else if row.item.SessionID != "" {
			add(m.inboxSessionOrigin(row.item.SessionID))
		}
	case "brief":
		add("daily brief")
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
		if row.ask.Detail != "" {
			out = append(out, sanitizeBody(row.ask.Detail))
		}
	case "proposal":
		out = append(out, sanitize(row.text))
		if row.proposal.Reason != "" {
			out = append(out, "", "Reason: "+sanitizeBody(row.proposal.Reason))
		}
	case "step":
		out = append(out, "Change: "+sanitize(row.ask.AboutID), "", sanitizeBody(firstNonEmpty(row.ask.Detail, row.ask.Question)))
	case "notice", "approval", "question":
		name := m.inboxAskerName(row.item)
		out = append(out, "From: "+sanitize(name), "", sanitizeBody(row.item.Text))
		for i, option := range row.item.Options {
			out = append(out, fmt.Sprintf("%d. %s", i+1, sanitize(option)))
		}
		if row.item.Recommendation != "" {
			out = append(out, "", "Recommendation: "+sanitizeBody(row.item.Recommendation))
		}
		if row.item.ChangeTitle != "" {
			out = append(out, "", "Related change: "+sanitize(row.item.ChangeTitle))
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
	kept := make([]protocol.Ask, 0, len(m.asks))
	for _, a := range m.asks {
		if a.ID != id {
			kept = append(kept, a)
		}
	}
	m.asks = kept
	m.inboxIndex = min(max(m.inboxIndex, 0), max(m.deskRowCount()-1, 0))
}

func (m Model) inboxDetailScrollLimit(rows []inboxRow) int {
	row, ok := m.inboxSelected(rows)
	if !ok {
		return 0
	}
	width := max(m.width, 40)
	height := max(m.height, 10) - 1 - m.ctoBoxHeight(m.tab)
	room := height - 3 // the tab line, the Desk line and the section line
	if m.inboxError != "" {
		room--
	}
	if m.inboxNote != "" {
		room--
	}
	count := 0
	for _, line := range m.inboxDetailLines(row) {
		if line == "" {
			count++
		} else {
			count += len(wrapText(line, max(width-2, 1)))
		}
	}
	return max(count-max(room, 0), 0)
}

// inboxTabKey handles the Inbox tab and its detail view.
func (m Model) inboxTabKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	rows := m.inboxRows()
	if m.inboxEditing {
		if m.deskPending != nil {
			return m.deskMarkKey(k)
		}
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
	if m.delivery.detail || m.delivery.reporting {
		mm, cmd := m.deliveryKey(k)
		return mm, cmd
	}
	// x hides the digest while it is shown; it does not dismiss an ask then.
	if m.digest != nil && !m.digest.hidden && !m.inboxDetail && k.String() == "x" {
		m.digest.hidden = true
		return m, nil
	}
	key := keyAction(contextInbox, k.String())
	if row, ok := m.inboxSelected(rows); ok && (strings.HasPrefix(row.item.SourceRef, "app-consent:") || row.item.SourceRef == "apps-public-consent") {
		if k.String() == "y" {
			key = "approve"
		}
		if k.String() == "n" {
			key = "refuse"
		}
	}

	switch key {
	case actQuit:
		m.quitting = true
		return m, tea.Quit
	case "help":
		m.openHelp()
		return m, nil
	case "show all":
		if !m.inboxDetail {
			m.delivery.showAll = !m.delivery.showAll
			m.delivery.detail = false
			m.inboxIndex = m.deskIndex()
			return m, nil
		}
	case "back":
		if !m.inboxDetail && m.delivery.project != "" {
			m.delivery.project = ""
			m.inboxIndex = m.deskIndex()
			return m, nil
		}
	case "log":
		if !m.inboxDetail {
			m.deskLogOpen = !m.deskLogOpen
			m.inboxIndex = m.deskIndex()
			if m.deskLogOpen {
				return m, m.deskLogCmd()
			}
			return m, nil
		}
	}
	if m.inboxDetail {
		limit := m.inboxDetailScrollLimit(rows)
		m.inboxDetailScroll = min(m.inboxDetailScroll, limit)
		switch key {
		case "back", actClose:
			m.inboxDetail = false
			m.inboxDetailScroll = 0
			return m, nil
		case actUp:
			m.inboxDetailScroll = max(m.inboxDetailScroll-1, 0)
			return m, nil
		case actDown:
			m.inboxDetailScroll = min(m.inboxDetailScroll+1, limit)
			return m, nil
		case actPageUp:
			m.inboxDetailScroll = max(m.inboxDetailScroll-max(m.height-8, 1), 0)
			return m, nil
		case actPageDown:
			m.inboxDetailScroll = min(m.inboxDetailScroll+max(m.height-8, 1), limit)
			return m, nil
		case "answer", "approve", "refuse", "dismiss":
		default:
			return m, nil
		}
	}

	total := m.deskRowCount()
	moved := true
	switch key {
	case actUp:
		m.inboxIndex = max(0, m.deskIndex()-1)
	case actDown:
		m.inboxIndex = min(max(total-1, 0), m.deskIndex()+1)
	case actPageUp:
		m.inboxIndex = max(0, m.deskIndex()-10)
	case actPageDown:
		m.inboxIndex = min(max(total-1, 0), m.deskIndex()+10)
	case actFirst:
		m.inboxIndex = 0
	case actLast:
		m.inboxIndex = max(total-1, 0)
	default:
		moved = false
	}
	if moved {
		if sec, i := m.deskPosition(); sec == deskSecChange {
			m.delivery.row = i
		}
		return m, nil
	}
	row, ok := m.inboxSelected(rows)
	if !ok {
		mm, cmd, handled := m.deskRowKey(k, key)
		if handled {
			return mm, cmd
		}
		return m, nil
	}
	switch key {
	case "details":
		switch row.kind {
		case "held", "waiting":
			m.selected = row.session
			return m.startEnterID(row.session)
		case "approval", "question":
			if m.inboxQuestionHasExtra(row, rows) {
				m.inboxDetailScroll = 0
				m.inboxDetail = true
			}
		case "proposal":
			m.proposalDetail = &proposalDetail{proposal: row.proposal, loading: true}
			return m, m.proposalDetailCmd(row.proposal)
		case "step":
			// Go to the change the step is about, when it is in flight.
			for i, ch := range m.deliveryVisible() {
				if ch.ID == row.ask.AboutID {
					m.inboxIndex = len(rows) + i
					return m, nil
				}
			}
			m.inboxDetailScroll = 0
			m.inboxDetail = true
		default:
			m.inboxDetailScroll = 0
			m.inboxDetail = true
		}
	case "didnt_need":
		if row.ask.ID != "" {
			return m.deskMarkStart(row.ask.ID, protocol.MarkDidntNeedMe), nil
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
		if row.kind == "notice" || row.kind == "approval" || row.kind == "question" || row.kind == "brief" {
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
		if strings.HasPrefix(row.item.SourceRef, "drafts:") {
			return m, m.inboxAnswer(row.item.ID, action)
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
		if row.item.SourceRef == "apps-public-consent" {
			if approve {
				return m, openOwnerConsent(row.item.Detail)
			}
			return m, m.inboxAnswer(row.item.ID, "dismiss")
		}
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
	details := bindingHint(contextInbox, "details")
	switch row.kind {
	case "held":
		open := details
		open.label = "open the session"
		to := m.inboxSessionName(row.held.To)
		if to == "" || strings.HasPrefix(to, "s_") {
			to = "the other agent"
		}
		return keepChoices([]hint{open,
			{key: "a", label: "send it to " + sanitize(to), prio: 1},
			{key: "r", label: "keep it held", prio: 1}})
	case "waiting":
		open := details
		open.label = "open " + sanitize(m.inboxSessionName(row.session))
		return keepChoices([]hint{open})
	case "question":
		h := []hint{
			{key: "t", label: "answer " + sanitize(row.name), prio: 1},
			{key: "x", label: "dismiss the ask", prio: 1},
			details,
		}
		if len(row.item.Options) > 0 {
			h = append([]hint{{key: fmt.Sprintf("1-%d", len(row.item.Options)), label: "choose an answer", prio: 1}}, h...)
		}
		return keepChoices(h)
	case "approval":
		name := sanitize(row.name)
		h := []hint{
			{key: "a", label: "approve " + name + "'s request", prio: 1},
			{key: "r", label: "refuse " + name + "'s request", prio: 1},
			{key: "x", label: "dismiss the ask", prio: 1},
			details,
		}
		if len(row.item.Options) > 0 {
			h = append([]hint{{key: fmt.Sprintf("1-%d", len(row.item.Options)), label: "choose an answer", prio: 1}}, h...)
		}
		return keepChoices(h)
	case "proposal":
		return keepChoices([]hint{
			{key: "a", label: "approve the change", prio: 1},
			{key: "r", label: "decline the change", prio: 1},
		})
	case "outcome":
		keep, overrule := inboxOutcomeChoiceLabels(row)
		return keepChoices([]hint{{key: "s", label: keep, prio: 1}, {key: "O", label: overrule, prio: 1}})
	case "learned":
		what := "suggestion"
		if row.learned.Origin == "draft" || strings.HasSuffix(row.name, " (draft)") {
			what = "draft"
		}
		return keepChoices([]hint{{key: "a", label: "accept the " + what, prio: 1}, {key: "r", label: "reject the " + what, prio: 1}})
	case "step":
		open := details
		open.label = "open the change"
		return keepChoices([]hint{open})
	case "notice":
		return keepChoices([]hint{{key: "x", label: "dismiss the notice", prio: 1}})
	case "brief":
		return keepChoices([]hint{{key: "x", label: "dismiss the brief", prio: 1}})
	}
	return nil
}

// keepChoices makes a row's own keys outlast the marks, the log and the help
// key when the bar is too wide for all of them.
func keepChoices(hs []hint) []hint {
	for i := range hs {
		hs[i].prio = 0
	}
	return hs
}

// inboxHints is the bottom key line: the keys that act on the selected row,
// then the movement and help keys.
func (m Model) inboxHints() []hint {
	help := bindingHint(contextInbox, "help")
	help.prio = 0
	if m.inboxDetail {
		row, ok := m.inboxSelected(m.inboxRows())
		var h []hint
		if ok && (row.kind == "question" || row.kind == "approval") {
			if len(row.item.Options) > 0 {
				h = append(h, hint{key: fmt.Sprintf("1-%d", len(row.item.Options)), label: "choose an answer", prio: 1})
			}
			h = append(h, bindingHint(contextInbox, "answer"))
			if row.kind == "approval" {
				h = append(h, bindingHint(contextInbox, "approve"), bindingHint(contextInbox, "refuse"))
			}
		}
		return append(h, bindingHint(contextInbox, "back"))
	}
	move := hint{key: "↑↓", label: "select", prio: 0}
	logHint := bindingHint(contextInbox, "log")
	logHint.prio = 1
	if m.deskOnChange() {
		if m.delivery.reporting || m.delivery.detail {
			return m.deliveryHints()
		}
		if changes := m.deliveryVisible(); len(changes) > 0 {
			_, i := m.deskPosition()
			return append(m.deskChangeHints(changes[min(i, len(changes)-1)]), move, logHint, help)
		}
	}
	if e, ok := m.deskSelectedLog(); ok {
		return append(m.deskLogHints(e), move, help)
	}
	if h, ok := m.deskSelectedHandled(); ok {
		return append(m.deskHandledHints(h), move, logHint, help)
	}
	rows := m.inboxRows()
	row, ok := m.inboxSelected(rows)
	if !ok {
		if m.deskRowCount() == 0 {
			return []hint{logHint, help, bindingHint(contextInbox, actQuit)}
		}
		return []hint{bindingPairHint(contextInbox, actUp, actDown), logHint, help, bindingHint(contextInbox, actQuit)}
	}
	return append(append(m.inboxItemHints(row), bindingHint(contextInbox, "didnt_need")), move, logHint, help)
}

// inboxCaller is the local call the Inbox and the CTO box need.
type inboxCaller interface {
	Call(context.Context, string, any) (json.RawMessage, error)
}

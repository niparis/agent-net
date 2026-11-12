package tui

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/niparis/agent-net/internal/protocol"
)

// messageSubtab is the Messages tab's three views. They are switched the same
// way as the Quotas sub-tabs: tab and shift+tab cycle them.
type messageSubtab int

const (
	messageAgents messageSubtab = iota
	messageWorkers
	messageSystem
)

func (s messageSubtab) name() string {
	switch s {
	case messageWorkers:
		return "Workers"
	case messageSystem:
		return "System"
	}
	return "Agents"
}

// systemKind names what agent-net sent by itself. The order is the order of
// the inventory; the screen groups by kind and recipient, never by message.
type systemKind int

const (
	systemOther systemKind = iota
	systemIdleCheck
	systemAutonomyContinue
	systemReplyReminder
	systemLoopTick
	systemStepTask
	systemStepReminder
	systemStuckStep
	systemChiefBriefWake
	systemChiefAskBatch
	systemUndelivered
	systemWaitingNotice
	systemWakeFailed
	systemWorkerNotStarted
	systemStepResult
	systemStepClosed
	systemMemberBlocked
	systemNativeMCPRemoved
)

type systemKindLabel struct {
	kind     systemKind
	singular string
	plural   string
}

// systemKindLabels is the one table the classifier and the screen share.
var systemKindLabels = []systemKindLabel{
	{systemIdleCheck, "idle check", "idle checks"},
	{systemAutonomyContinue, "autonomy continue", "autonomy continues"},
	{systemReplyReminder, "reply reminder", "reply reminders"},
	{systemLoopTick, "loop tick", "loop ticks"},
	{systemStepTask, "step task", "step tasks"},
	{systemStepReminder, "step reminder", "step reminders"},
	{systemStuckStep, "stuck step", "stuck steps"},
	{systemChiefBriefWake, "chief brief wake", "chief brief wakes"},
	{systemChiefAskBatch, "chief ask batch", "chief ask batches"},
	{systemUndelivered, "undelivered notice", "undelivered notices"},
	{systemWaitingNotice, "waiting notice", "waiting notices"},
	{systemWakeFailed, "wake failed", "wake failures"},
	{systemWorkerNotStarted, "worker not started", "workers not started"},
	{systemStepResult, "step result", "step results"},
	{systemStepClosed, "step closed", "steps closed"},
	{systemMemberBlocked, "member blocked", "members blocked"},
	{systemNativeMCPRemoved, "native MCP removed", "native MCP removed"},
	{systemOther, "other notice", "other notices"},
}

func (k systemKind) label(plural bool) string {
	for _, l := range systemKindLabels {
		if l.kind == k {
			if plural {
				return l.plural
			}
			return l.singular
		}
	}
	return "other notice"
}

// classifySystemMessage names one of agent-net's own messages by its first
// marker or words. It is the only classifier: the tab, the grouping and the
// tests read this table. Native MCP removed is sent by the owner session, so
// that one is matched by its words before the sender check.
func classifySystemMessage(from, body string) (systemKind, bool) {
	b := strings.TrimSpace(body)
	if b == "" {
		return systemOther, false
	}
	lower := strings.ToLower(b)
	if strings.Contains(lower, "moved to agent-net; restart this session") ||
		strings.HasPrefix(lower, "agent-net: the mcp server") {
		return systemNativeMCPRemoved, true
	}
	if from != "agent-net" {
		return systemOther, false
	}
	switch {
	case strings.HasPrefix(b, "[agent-net worker notice]") && strings.Contains(lower, "status: not started"):
		return systemWorkerNotStarted, true
	case strings.HasPrefix(b, "[agent-net step task reminder"):
		return systemStepReminder, true
	case strings.HasPrefix(b, "[agent-net step task"):
		return systemStepTask, true
	case strings.HasPrefix(b, "[agent-net step result]"):
		return systemStepResult, true
	case strings.HasPrefix(b, "[agent-net step closed]"):
		return systemStepClosed, true
	case strings.HasPrefix(b, "[agent-net] check:"):
		return systemIdleCheck, true
	case strings.HasPrefix(b, "[agent-net] continue:"):
		return systemAutonomyContinue, true
	case strings.HasPrefix(b, "[agent-net] reminder:"):
		return systemReplyReminder, true
	case strings.HasPrefix(b, "[agent-net] Loop:"):
		return systemLoopTick, true
	case strings.HasPrefix(b, "Daily brief due for"):
		return systemChiefBriefWake, true
	case strings.HasPrefix(b, "Chief of staff:") || strings.HasPrefix(b, "The chief of staff sent you ask"):
		return systemChiefAskBatch, true
	case strings.Contains(lower, "is waiting for its user's"):
		return systemWaitingNotice, true
	case strings.Contains(lower, "could not be woken"):
		return systemWakeFailed, true
	case strings.Contains(lower, "was not delivered") || strings.Contains(lower, "may not have arrived") ||
		strings.Contains(lower, "expired after"):
		return systemUndelivered, true
	case strings.Contains(lower, "is blocked or waiting"):
		return systemMemberBlocked, true
	case strings.Contains(lower, "has waited") && strings.Contains(lower, "nobody claimed it"):
		return systemStuckStep, true
	}
	return systemOther, true
}

// burstRule is the burst marker's only rule: the busiest hour holds at least
// 20 messages and more than three times the thread's own median hourly count.
func burstRule(peak, median int) bool {
	return peak >= 20 && peak > 3*median
}

// hourlyCounts buckets the given times into hours and returns the busiest and
// median counts. It is used when an older gateway does not send the fields.
func hourlyCounts(times []time.Time) (peak, median int) {
	if len(times) == 0 {
		return 0, 0
	}
	counts := map[int64]int{}
	for _, t := range times {
		counts[t.Unix()/3600]++
	}
	hours := make([]int, 0, len(counts))
	for _, n := range counts {
		hours = append(hours, n)
	}
	sort.Ints(hours)
	peak = hours[len(hours)-1]
	mid := len(hours) / 2
	if len(hours)%2 == 1 {
		median = hours[mid]
	} else {
		median = (hours[mid-1] + hours[mid]) / 2
	}
	return peak, median
}

// threadTab says which sub-tab a row belongs to. A conversation agent-net sent
// a message in is System, a worker run is Workers, and every other pair is
// Agents (including the owner's own threads).
func (m Model) threadTab(c messageConversation) messageSubtab {
	if c.system || c.from == "agent-net" {
		return messageSystem
	}
	if c.worker {
		return messageWorkers
	}
	return messageAgents
}

// partyHarness is the harness the snapshot knows for a session. agent-net's
// reserved sender has no session record.
func (m Model) partyHarness(id string) string {
	if id == "agent-net" {
		return "agent-net"
	}
	if a, ok := m.agentByID(id); ok {
		return a.Harness
	}
	return ""
}

// reportsBlocked tells whether a body reports an action that is blocked. The
// words are narrow on purpose: the worker brief says "if you are blocked", and
// that is not a report.
func reportsBlocked(body string) bool {
	lower := strings.ToLower(body)
	for _, marker := range []string{"blocked on ", "blocked by ", "blocked:", "is blocked", "i am blocked", "i'm blocked", "we are blocked"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// groupSystemKind finds the newest agent-net message in a conversation,
// classifies it, and names its recipient. A native MCP removal comes from the
// owner session, so the fallback checks the latest line's words too.
func (m Model) groupSystemKind(c messageConversation) (systemKind, string, bool) {
	for i := len(c.items) - 1; i >= 0; i-- {
		it := m.feedItemAt(c.items[i])
		if it == nil || !it.isMessage() {
			continue
		}
		if it.From == "agent-net" {
			k, ok := classifySystemMessage(it.From, it.Body)
			return k, it.To, ok
		}
	}
	if c.from == "agent-net" {
		k, ok := classifySystemMessage(c.from, c.lastLine)
		return k, c.to, ok
	}
	if k, ok := classifySystemMessage("", c.lastLine); ok {
		return k, c.to, true
	}
	return systemOther, "", false
}

// threadParticipants is the conversation panel's heading: who is in the
// thread and what kind each side is.
func (m Model) threadParticipants(c messageConversation) string {
	if c.event {
		return ""
	}
	if c.system {
		return c.systemKind.label(true) + " → " + m.partyName(c.to, c.toName) + m.typeTag(c.to, c.toHarness)
	}
	from := m.partyName(c.from, c.fromName) + m.typeTag(c.from, c.fromHarness)
	to := m.partyName(c.to, c.toName) + m.typeTag(c.to, c.toHarness)
	base := from + " ↔ " + to
	if c.worker {
		if task := firstNonEmpty(c.task, m.workerTaskLine(c.workerSession)); task != "" {
			base += " · " + task
		}
	}
	var flags []string
	if c.ownerNamed || c.ownerInstruction {
		flags = append(flags, "owner")
	}
	if c.blocked {
		flags = append(flags, "blocked")
	}
	if burst := c.burst || m.threadBurst(c); burst {
		flags = append(flags, fmt.Sprintf("burst %d/h", max(c.peakHour, 20)))
	}
	if len(flags) > 0 {
		base += " · " + strings.Join(flags, " · ")
	}
	return base
}

// partyType names a sender's kind: session, worker, app or system. The owner's
// pseudo-session is a session. A bot session is a connected app. An empty
// harness with no live session is an ended session.
func (m Model) partyType(id, harness string) string {
	if id == "agent-net" || harness == "agent-net" {
		return "system"
	}
	if a, ok := m.agentByID(id); ok {
		if a.Worker != nil {
			return "worker"
		}
		if a.Harness == "bot" {
			return "app"
		}
		if protocol.IsOwner(a.Harness) {
			return "session"
		}
		return "session"
	}
	switch harness {
	case "bot":
		return "app"
	case "agent-net":
		return "system"
	case "owner":
		return "session"
	case "":
		return "ended"
	}
	return "session"
}

// typeTag is the compact sender type shown beside a name. A plain session is
// left unmarked to save room; the legend says so.
func (m Model) typeTag(id, harness string) string {
	switch m.partyType(id, harness) {
	case "app":
		return " (app)"
	case "worker":
		return " (worker)"
	case "system":
		return " (system)"
	}
	return ""
}

// sessionProject is the project a session works in. The repo check wins over
// the profile's claim, as everywhere else in the TUI.
func (m Model) sessionProject(id string) string {
	if a, ok := m.agentByID(id); ok {
		return firstNonEmpty(firstNonEmpty(a.Repo.Project, a.Profile.Project), a.ProjectDisplayName)
	}
	return ""
}

// sessionTeam is the team a session belongs to, from the graph edges the
// snapshot already holds.
func (m Model) sessionTeam(id string) string {
	a, ok := m.agentByID(id)
	if !ok {
		return ""
	}
	for _, e := range m.snap.Edges {
		if e.Kind != "member_of" || e.ToRef == nil || e.ToRef.Kind != "team" {
			continue
		}
		if e.FromRef == nil {
			continue
		}
		if e.FromRef.Kind == "agent" && (e.FromRef.ID == a.AgentID || e.FromRef.ID == a.SessionID || e.FromRef.ID == a.Profile.Name) {
			return e.ToRef.ID
		}
		if e.FromRef.Kind == "repo" && e.FromRef.ID == firstNonEmpty(a.Repo.Project, a.Profile.Project) {
			return e.ToRef.ID
		}
	}
	return ""
}

// threadMatchesProject and threadMatchesTeam apply the two filters. A thread
// matches when either party does; an ended session with no project or team
// stays out of a named filter and in the all view.
func (m Model) threadMatchesProject(c messageConversation) bool {
	if m.threadProject == "" {
		return true
	}
	for _, id := range []string{c.from, c.to} {
		if m.sessionProject(id) == m.threadProject {
			return true
		}
	}
	return false
}

func (m Model) threadMatchesTeam(c messageConversation) bool {
	if m.threadTeam == "" {
		return true
	}
	for _, id := range []string{c.from, c.to} {
		if m.sessionTeam(id) == m.threadTeam {
			return true
		}
	}
	return false
}

// threadMatchesText is the existing / search, over the same facts the list
// shows. Bodies are searched when the feed holds them.
func (m Model) threadMatchesText(c messageConversation) bool {
	needle := strings.ToLower(firstNonEmpty(m.uiFilter, m.filter))
	if needle == "" {
		return true
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s %s %s %s %s %s %s ", c.from, c.to, c.fromName, c.toName,
		c.lastLine, c.task, m.conversationName(c.from), m.conversationName(c.to))
	if c.system {
		b.WriteString(c.systemKind.label(false) + " " + c.systemKind.label(true) + " " + c.recipient)
	}
	for _, i := range c.items {
		if it := m.feedItemAt(i); it != nil {
			fmt.Fprintf(&b, "%s %s %s %s %s ", it.From, it.To, it.Snippet, it.Type, it.Body)
		}
	}
	return strings.Contains(strings.ToLower(b.String()), needle)
}

// visibleConversations applies the active sub-tab, the project and team
// filters, the text search, and the sort. Grouping runs on rows the screen
// already holds: no gateway call is made here.
func (m Model) visibleConversations(rows []messageConversation) []messageConversation {
	out := make([]messageConversation, 0, len(rows))
	for _, r := range rows {
		if m.threadTab(r) != m.messageSubtab {
			continue
		}
		if !m.threadMatchesProject(r) || !m.threadMatchesTeam(r) || !m.threadMatchesText(r) {
			continue
		}
		out = append(out, r)
	}
	if m.messageSubtab == messageSystem {
		out = m.systemThreads(out)
		needle := strings.ToLower(firstNonEmpty(m.uiFilter, m.filter))
		for i, it := range m.feed.items {
			if it.isMessage() {
				continue
			}
			if needle != "" && !strings.Contains(strings.ToLower(m.infoText(it)), needle) {
				continue
			}
			out = append(out, messageConversation{id: fmt.Sprintf("event-%d", i), items: []int{i}, at: it.At, event: true})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if m.messageSubtab == messageSystem {
			if out[i].today != out[j].today {
				return out[i].today > out[j].today
			}
		} else {
			ri, rj := m.threadRank(out[i]), m.threadRank(out[j])
			if ri != rj {
				return ri < rj
			}
		}
		if out[i].at.Equal(out[j].at) {
			return out[i].id > out[j].id
		}
		return out[i].at.After(out[j].at)
	})
	return out
}

// threadRank puts what needs the owner first: an owner instruction, then an
// unanswered request, then everything else.
func (m Model) threadRank(c messageConversation) int {
	switch {
	case c.ownerNamed || c.ownerInstruction:
		return 0
	case m.threadState(c) == "waiting":
		return 1
	default:
		return 2
	}
}

// systemThreads groups system messages by kind and recipient. Each stored
// system message is its own conversation, so the count and the today figure
// are sums over the rows the TUI holds.
func (m Model) systemThreads(rows []messageConversation) []messageConversation {
	groups := map[string]*messageConversation{}
	for _, r := range rows {
		to := r.recipient
		if to == "" {
			to = r.to
		}
		key := fmt.Sprintf("%d\x00%s", r.systemKind, to)
		g := groups[key]
		if g == nil {
			g = &messageConversation{
				id: "system:" + key, system: true, systemKind: r.systemKind,
				from: "agent-net", to: to, recipient: to, toName: r.toName,
				at: r.at, convID: r.convID, links: append([]string(nil), r.links...),
			}
			groups[key] = g
		}
		g.count += max(r.count, 1)
		g.items = append(g.items, r.items...)
		if r.at.After(g.at) {
			g.at = r.at
			g.lastLine = r.lastLine
			g.convID = r.convID
			g.links = append(g.links, r.links...)
		}
		if r.lastLine != "" {
			g.systemLines = append(g.systemLines, m.clock(r.at)+"  "+r.lastLine)
		}
		if m.sameDay(r.at, m.now()) {
			g.today++
		}
		if g.toName == "" {
			name := m.conversationName(to)
			if name == "ended session" {
				if to == r.to {
					name = r.toName
				} else {
					name = ""
				}
			}
			g.toName = name
		}
	}
	out := make([]messageConversation, 0, len(groups))
	for _, g := range groups {
		out = append(out, *g)
	}
	return out
}

func (m Model) sameDay(a, b time.Time) bool {
	a, b = a.In(m.loc), b.In(m.loc)
	return a.Year() == b.Year() && a.YearDay() == b.YearDay()
}

// threadState is the label the owner asked for: waiting for a reply, answered,
// or held at the gateway. A failed or expired request keeps its own word.
func (m Model) threadState(c messageConversation) string {
	if c.system {
		return ""
	}
	typ, state := c.typ, c.state
	if len(c.items) > 0 {
		if it := m.feedItemAt(c.items[len(c.items)-1]); it != nil {
			typ, state = it.Type, it.State
		}
	}
	switch {
	case state == protocol.StateFailed:
		return "failed"
	case state == protocol.StateExpired:
		return "timed out"
	case typ == protocol.TypeRequest && state != protocol.StateReplied && state != protocol.StateAcknowledged:
		if held := m.gatewayHeld(c); held > 0 {
			return fmt.Sprintf("gw %d", held)
		}
		return "waiting"
	case typ == protocol.TypeReply || state == protocol.StateReplied || state == protocol.StateAcknowledged:
		return "answered"
	}
	return ""
}

// gatewayHeld is the recipient's held count from the snapshot. The gateway
// already reports it; no extra read is made.
func (m Model) gatewayHeld(c messageConversation) int {
	to := c.to
	if len(c.items) > 0 {
		if it := m.feedItemAt(c.items[len(c.items)-1]); it != nil && it.To != "" {
			to = it.To
		}
	}
	if a, ok := m.agentByID(to); ok {
		return a.GatewayHeld
	}
	return 0
}

// threadBurst applies the rule to the stored fields, or to the feed times when
// an older gateway did not send them.
func (m Model) threadBurst(c messageConversation) bool {
	if burstRule(c.peakHour, c.medianHour) {
		return true
	}
	var times []time.Time
	for _, i := range c.items {
		if it := m.feedItemAt(i); it != nil && it.isMessage() {
			times = append(times, it.At)
		}
	}
	peak, median := hourlyCounts(times)
	return burstRule(peak, median)
}

var (
	changeLinkRE = regexp.MustCompile(`\bch_[0-9a-f]{8,}\b`)
	askLinkRE    = regexp.MustCompile(`\bi_[0-9a-f]{8,}\b`)
	prURLRE      = regexp.MustCompile(`https?://github\.com/[^\s/]+/[^\s/]+/pull/\d+`)
	prNumberRE   = regexp.MustCompile(`(?i)\bPR\s*#?(\d+)\b`)
)

// threadLinks finds the PRs, changes and Desk asks named in a thread's bodies.
// The gateway sends the last line and the task; the feed and an opened
// conversation add the full bodies.
func (m Model) threadLinks(c messageConversation) []string {
	var texts []string
	texts = append(texts, c.lastLine, c.task)
	for _, i := range c.items {
		if it := m.feedItemAt(i); it != nil {
			texts = append(texts, it.Snippet, it.Body)
		}
	}
	if d, ok := m.convDetail[c.convID]; ok {
		for _, msg := range d.msgs {
			texts = append(texts, msg.Body)
		}
	}
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if s == "" || seen[s] || len(out) >= 6 {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	for _, text := range texts {
		for _, u := range prURLRE.FindAllString(text, -1) {
			add(u)
		}
		for _, id := range changeLinkRE.FindAllString(text, -1) {
			add(id)
		}
		for _, id := range askLinkRE.FindAllString(text, -1) {
			add("ask " + id)
		}
		for _, m := range prNumberRE.FindAllStringSubmatch(text, -1) {
			add("PR #" + m[1])
		}
	}
	return out
}

// linkLabel shortens a link for the conversation header.
func linkLabel(link string) string {
	if len(link) > 52 {
		return link[:51] + "…"
	}
	return link
}

// threadLinkJump opens the selected thread's change or ask in the Desk when it
// is already loaded. A PR URL has no open action in the TUI, so it is named in
// the notice instead.
func (m Model) threadLinkJump() (Model, tea.Cmd) {
	rows := m.messageConversations()
	if len(rows) == 0 {
		return m, nil
	}
	row := rows[m.selectedConversationIndex(rows)]
	links := m.threadLinks(row)
	if len(links) == 0 {
		m.fail("This thread names no PR, change or ask.")
		return m, nil
	}
	for _, link := range links {
		if strings.HasPrefix(link, "ch_") {
			if cmd := m.openDeskChange(link); cmd != nil {
				return m, cmd
			}
		}
	}
	for _, link := range links {
		if strings.HasPrefix(link, "ask i_") {
			if m.openDeskAsk(strings.TrimPrefix(link, "ask ")) {
				return m, nil
			}
		}
	}
	m.fail("Links: " + strings.Join(links, " · "))
	return m, nil
}

// openDeskChange selects a loaded change and opens its evidence.
func (m *Model) openDeskChange(id string) tea.Cmd {
	visible := m.deliveryVisible()
	for i, ch := range visible {
		if ch.ID != id {
			continue
		}
		m.tab = inboxTab
		m.delivery.project = ""
		m.delivery.detail = true
		m.delivery.scroll = 0
		m.delivery.row = i
		m.delivery.detailLoading = true
		return m.deliveryDetailCmd(id)
	}
	return nil
}

// openDeskAsk selects a loaded ask in the Desk.
func (m *Model) openDeskAsk(id string) bool {
	rows := m.inboxRows()
	for i, row := range rows {
		if row.ask.ID != id {
			continue
		}
		m.tab = inboxTab
		m.inboxIndex = i
		return true
	}
	return false
}

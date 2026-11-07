package tui

import (
	"fmt"
	"github.com/charmbracelet/x/ansi"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/niparis/agent-net/internal/protocol"
)

// The session finder: one overlay that ranks every session the gateway knows,
// including the offline ones that the normal views leave out.

// rankedSessions is the list the finder draws, for the query typed so far.
func (m Model) rankedSessions() []protocol.AgentView {
	return rankSessions(m.finderQuery, m.snap.Agents, m.now())
}

// rankSessions matches a query against the name, project, folder, machine and
// task of each session. A name that equals the query comes first, then a name
// that starts with it, then the rest by most recent activity. A later change
// can add owner-use weighting here.
func rankSessions(query string, agents []protocol.AgentView, now time.Time) []protocol.AgentView {
	q := strings.TrimSpace(query)
	type ranked struct {
		agent    protocol.AgentView
		nameRank int
		activity int64
	}
	var rows []ranked
	for _, a := range agents {
		if protocol.IsOwner(a.Harness) || a.Harness == "bot" {
			continue
		}
		// A worker that ended is not a row: its next message starts it again.
		if a.Worker != nil && !a.Online {
			continue
		}
		if isJunkSession(a) && !exactSessionMatch(a, q) {
			continue
		}
		if !sessionMatches(a, q) {
			continue
		}
		rows = append(rows, ranked{agent: a, nameRank: nameMatchRank(a, q), activity: sessionActivity(a)})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].nameRank != rows[j].nameRank {
			return rows[i].nameRank < rows[j].nameRank
		}
		if rows[i].activity != rows[j].activity {
			return rows[i].activity > rows[j].activity
		}
		return strings.ToLower(sessionName(rows[i].agent)) < strings.ToLower(sessionName(rows[j].agent))
	})
	out := make([]protocol.AgentView, len(rows))
	for i := range rows {
		out[i] = rows[i].agent
	}
	return out
}

func sessionName(a protocol.AgentView) string {
	return rowName(a)
}

func sessionActivity(a protocol.AgentView) int64 {
	if a.LastActive > 0 {
		return a.LastActive
	}
	return a.LastSeen
}

func nameMatchRank(a protocol.AgentView, query string) int {
	if query == "" {
		return 1
	}
	name, q := strings.ToLower(sessionName(a)), strings.ToLower(query)
	switch {
	case name == q:
		return 0
	case strings.HasPrefix(name, q):
		return 1
	}
	return 2
}

// sessionMatches is true when every word of the query is a substring or a
// subsequence of one of the session's searchable fields.
func sessionMatches(a protocol.AgentView, query string) bool {
	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 {
		return true
	}
	fields := sessionSearchFields(a)
	for _, word := range words {
		found := false
		for _, field := range fields {
			if fuzzyWord(word, field) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func sessionSearchFields(a protocol.AgentView) []string {
	return []string{
		sessionName(a),
		firstNonEmpty(firstNonEmpty(a.Repo.Project, a.Profile.Project), a.ProjectDisplayName),
		firstNonEmpty(firstNonEmpty(a.Repo.Worktree, a.Repo.Root), a.Cwd),
		firstNonEmpty(a.Hostname, a.NodeID),
		firstNonEmpty(a.Profile.Task, a.Title),
	}
}

func fuzzyWord(word, field string) bool {
	word, field = strings.ToLower(word), strings.ToLower(field)
	if word == "" {
		return true
	}
	if strings.Contains(field, word) {
		return true
	}
	runes := []rune(word)
	i := 0
	for _, r := range field {
		if i < len(runes) && r == runes[i] {
			i++
		}
	}
	return i == len(runes)
}

func exactSessionMatch(a protocol.AgentView, query string) bool {
	q := strings.TrimSpace(query)
	if q == "" {
		return false
	}
	for _, value := range []string{sessionName(a), a.Cwd, a.Repo.Root, a.Repo.Worktree, a.SessionID} {
		if value != "" && strings.EqualFold(strings.TrimSpace(value), q) {
			return true
		}
	}
	return false
}

// isJunkSession is true for a session in a temp folder, such as a test that
// left one behind. The complete view shows them; every other view leaves them
// out.
func isJunkSession(a protocol.AgentView) bool {
	return isTempFolder(a.Cwd) || isTempFolder(a.Repo.Root) || isTempFolder(a.Repo.Worktree)
}

func isTempFolder(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	lower := strings.ToLower(filepath.ToSlash(path))
	roots := []string{"/tmp", "/var/tmp", "/private/tmp", "/private/var/tmp", "/var/folders", "/private/var/folders", "/dev/shm"}
	if temp := os.TempDir(); temp != "" {
		roots = append(roots, filepath.ToSlash(temp))
	}
	for _, root := range roots {
		root = strings.TrimSuffix(strings.ToLower(filepath.ToSlash(root)), "/")
		if root == "" || root == "." {
			continue
		}
		if lower == root || strings.HasPrefix(lower, root+"/") {
			return true
		}
	}
	return strings.Contains(lower, "/scratchpad/")
}

// showJunk says whether the current view is complete enough for junk sessions:
// all, with hidden on.
func (m Model) showJunk() bool {
	return m.showHiddenSessions && !m.activeOnly
}

func (m Model) openFinder() Model {
	m.finder = true
	m.finderQuery = ""
	m.finderSel = 0
	m.finderTop = 0
	return m
}

func (m Model) finderRoom() int {
	content := max(m.height, 10) - 1
	return min(12, max(content-7, 3))
}

func (m *Model) clampFinder() {
	rows := m.rankedSessions()
	if len(rows) == 0 {
		m.finderSel, m.finderTop = 0, 0
		return
	}
	m.finderSel = min(max(m.finderSel, 0), len(rows)-1)
	room := m.finderRoom()
	if m.finderSel < m.finderTop {
		m.finderTop = m.finderSel
	}
	if m.finderSel >= m.finderTop+room {
		m.finderTop = m.finderSel - room + 1
	}
	m.finderTop = max(m.finderTop, 0)
}

func (m Model) finderKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc", "ctrl+p":
		m.finder, m.finderQuery = false, ""
		return m, nil
	case "enter":
		rows := m.rankedSessions()
		if len(rows) == 0 {
			return m, nil
		}
		a := rows[min(max(m.finderSel, 0), len(rows)-1)]
		m.finder, m.finderQuery, m.finderSel, m.finderTop = false, "", 0, 0
		m.selected = a.SessionID
		return m.startEnterID(a.SessionID)
	case "up":
		m.finderSel--
		m.clampFinder()
	case "down":
		m.finderSel++
		m.clampFinder()
	case "pgup":
		m.finderSel -= m.finderRoom()
		m.clampFinder()
	case "pgdown":
		m.finderSel += m.finderRoom()
		m.clampFinder()
	case "home":
		m.finderSel = 0
		m.clampFinder()
	case "end":
		m.finderSel = 1 << 20
		m.clampFinder()
	case "backspace", "ctrl+h":
		m.finderQuery = trimLastRune(m.finderQuery)
		m.finderSel, m.finderTop = 0, 0
	default:
		if k.Text != "" && k.Mod&(tea.ModCtrl|tea.ModAlt) == 0 {
			m.finderQuery += k.Text
			m.finderSel, m.finderTop = 0, 0
		}
	}
	return m, nil
}

func trimLastRune(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	return string(r[:len(r)-1])
}

type finderColumn struct {
	name  string
	width int
}

func finderColumns(inner int) []finderColumn {
	age := finderColumn{"AGE", 4}
	state := finderColumn{"STATE", 8}
	if inner < 40 {
		return []finderColumn{{"NAME", max(inner-2-age.width-state.width, 6)}, state, age}
	}
	if inner < 50 {
		project := finderColumn{"PROJECT", 12}
		return []finderColumn{{"NAME", max(inner-3-project.width-age.width-state.width, 6)}, project, state, age}
	}
	harness := finderColumn{"HARNESS", 9}
	rest := inner - 5 - age.width - state.width - harness.width
	machineW := min(max(rest*26/100, 8), 18)
	projectW := min(max(rest*30/100, 8), 24)
	nameW := rest - machineW - projectW
	if nameW < 10 {
		short := 10 - nameW
		projectW = max(projectW-short, 8)
		nameW = rest - machineW - projectW
	}
	if nameW < 8 {
		machineW = max(machineW-(8-nameW), 8)
		nameW = max(rest-machineW-projectW, 8)
	}
	return []finderColumn{
		{"NAME", nameW},
		{"PROJECT", projectW},
		{"MACHINE", machineW},
		harness,
		state,
		age,
	}
}

func finderHeader(cols []finderColumn) string {
	cells := make([]string, len(cols))
	for i, c := range cols {
		label := c.name
		if i == 0 {
			label = "  " + label
		}
		if c.name == "AGE" {
			cells[i] = cellR(label, c.width)
		} else {
			cells[i] = cell(label, c.width)
		}
	}
	return sDim.Render(strings.Join(cells, " "))
}

func (m Model) finderRow(a protocol.AgentView, cols []finderColumn, selected bool) string {
	cells := make([]string, len(cols))
	for i, c := range cols {
		var value string
		switch c.name {
		case "NAME":
			marker := "  "
			if selected {
				marker = "› "
			}
			value = marker + sanitize(sessionName(a))
		case "PROJECT":
			value = firstNonEmpty(firstNonEmpty(a.Repo.Project, a.Profile.Project), a.ProjectDisplayName)
		case "MACHINE":
			value = firstNonEmpty(m.nodeName(a.NodeID, a.Hostname), a.NodeID)
		case "HARNESS":
			value = a.Harness
		case "STATE":
			value = finderState(a)
		case "":
			if m.finderQuery != "" && !sessionMatchesName(a, m.finderQuery) {
				labels := []string{"name", "project", "folder", "machine", "task"}
				for j, field := range sessionSearchFields(a) {
					if j > 0 && fuzzyWord(strings.ToLower(m.finderQuery), field) {
						value = "matched: " + labels[j]
						break
					}
				}
			}
		case "AGE":
			value = needsAge(m.now(), sessionActivity(a))
		}
		if c.name != "" {
			value = dashIfEmpty(value)
		}
		if c.name == "AGE" {
			cells[i] = cellR(value, c.width)
		} else {
			cells[i] = cell(value, c.width)
			if c.name == "NAME" {
				cells[i] = highlightFinderName(cells[i], m.finderQuery)
			}
			if c.name == "" {
				cells[i] = sDim.Render(cells[i])
			}
		}
	}
	line := strings.Join(cells, " ")

	switch {
	case selected:
		return selectedRow(line)
	case !a.Online || a.Status == protocol.StatusDormant:
		return sDim.Render(line)
	}
	return line
}

func finderState(a protocol.AgentView) string {
	switch {
	case a.StalledSince > 0 && a.Worker == nil:
		return "stalled"
	case !a.Online:
		return "offline"
	case a.Status == protocol.StatusDormant:
		return "dormant"
	}
	if s := waitingStatus(a); s != "" {
		return s
	}
	if a.Status == "unknown" {
		return "unknown"
	}
	return statusShort(a.Status)
}

func (m Model) finderQueryLine(inner int) string {
	if m.finderQuery == "" {
		return sAccent.Render("› ▌ ") + sDim.Render(clip("type a name, project, folder, machine or task", max(inner-4, 0)))
	}
	return sAccent.Render("› ") + clip(sanitize(m.finderQuery), max(inner-3, 0)) + sAccent.Render("▌")
}

func finderCount(n int) string {
	if n == 1 {
		return "1 match"
	}
	return fmt.Sprintf("%d matches", n)
}

func (m Model) finderView(w int) []string {
	width := min(max(w, 40), 120)
	inner := width - 4
	rows := m.rankedSessions()
	cols := finderColumns(inner)
	shown := rows[min(max(m.finderTop, 0), len(rows)):min(len(rows), max(m.finderTop, 0)+m.finderRoom())]
	if len(cols) == 6 {
		name, project := 6, 7
		for _, a := range shown {
			name = max(name, min(32, ansi.StringWidth(sessionName(a)))+2)
			project = max(project, min(24, ansi.StringWidth(firstNonEmpty(firstNonEmpty(a.Repo.Project, a.Profile.Project), a.ProjectDisplayName))))
		}
		if name+project <= cols[0].width+cols[1].width {
			cols[0].width, cols[1].width = name, project
		}
		used := len(cols) - 1
		for _, c := range cols {
			used += c.width
		}
		if inner-used >= 17 {
			cols = append(cols, finderColumn{"", 16})
			used += 17
		}
		inner = used
		width = inner + 4
	}

	body := []string{m.finderQueryLine(inner), finderHeader(cols)}
	room := m.finderRoom()
	top := min(max(m.finderTop, 0), max(len(rows)-1, 0))
	if len(rows) == 0 {
		body = append(body, sDim.Render("  no sessions match"))
	} else {
		for i := top; i < len(rows) && i < top+room; i++ {
			body = append(body, m.finderRow(rows[i], cols, i == m.finderSel))
		}
	}
	hints := keyHints("enter", "open", "up/down", "select", "esc", "close")
	body = append(body, sDim.Render(clip(hints, inner)))
	return panelWithRightHeader("Find session", finderCount(len(rows)), body, width, roleAccent)
}

func sessionMatchesName(a protocol.AgentView, q string) bool {
	for _, word := range strings.Fields(q) {
		if !fuzzyWord(word, sessionName(a)) {
			return false
		}
	}
	return true
}

func highlightFinderName(value, query string) string {
	q := []rune(strings.ToLower(strings.Join(strings.Fields(query), "")))
	if len(q) == 0 {
		return value
	}
	pos := 0
	var out strings.Builder
	for _, r := range value {
		if pos < len(q) && strings.EqualFold(string(r), string(q[pos])) {
			out.WriteString(sAccent.Bold(true).Render(string(r)))
			pos++
		} else {
			out.WriteRune(r)
		}
	}
	return out.String()
}

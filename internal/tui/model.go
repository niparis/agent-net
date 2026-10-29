package tui

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/niparis/agent-net/internal/client"
	"github.com/niparis/agent-net/internal/protocol"
)

// RefreshEvery is how often the screen asks the daemon for news.
const RefreshEvery = time.Second

// backlog is how many events the first refresh asks for.
const backlog = 200

type focus int

const (
	focusSessions focus = iota
	focusFeed
)

// openMsg is the message whose body is on screen.
type openMsg struct {
	id      string
	loading bool
	msg     protocol.Message
	err     string
	scroll  int
}

// Model is the whole state of the screen. Update changes it; view.go only
// reads it.
type Model struct {
	src Source
	// loc and now are replaced in tests.
	loc *time.Location
	now func() time.Time

	width, height int

	// What the daemon last told us. It stays on screen when a refresh fails.
	snap     Snapshot
	haveData bool
	lastOK   time.Time
	problem  Problem
	problemT string // text of the last error
	feed     feed
	fetching bool

	// What the person chose.
	selected    string // session ID
	selIdx      int    // position in the visible list, to move on when the session leaves
	tableTop    int    // first table row on screen
	showOffline bool
	filter      string
	filtering   bool
	input       textinput.Model
	focus       focus
	feedCur     int // index in the feed items when the feed has focus
	open        *openMsg
	quitting    bool
}

// New returns a model that reads from src.
func New(src Source) Model {
	ti := textinput.New()
	ti.Prompt = "/"
	ti.CharLimit = 80
	ti.Cursor.SetMode(cursor.CursorHide)
	return Model{src: src, loc: time.Local, now: time.Now, width: 80, height: 24, feed: newFeed(), input: ti}
}

// ---- messages ----

type tickMsg struct{}

type fetchedMsg struct {
	snap      Snapshot
	err       error
	events    []protocol.FeedEvent
	eventsErr error
	at        time.Time
}

type bodyMsg struct {
	id  string
	msg protocol.Message
	err error
}

// Init starts the first refresh.
func (m Model) Init() tea.Cmd { return m.fetchCmd() }

func tick() tea.Cmd {
	return tea.Tick(RefreshEvery, func(time.Time) tea.Msg { return tickMsg{} })
}

// fetchCmd reads a snapshot and the new events. One runs at a time.
func (m Model) fetchCmd() tea.Cmd {
	src, after := m.src, m.feed.lastID
	skipFeed := m.feed.unavailable != ""
	now := m.now
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), Timeout)
		defer cancel()
		var res fetchedMsg
		res.at = now()
		res.snap, res.err = src.Snapshot(ctx)
		if res.err != nil || !res.snap.Connected || skipFeed {
			return res
		}
		limit := 100
		if after == 0 {
			limit = backlog
		}
		res.events, res.eventsErr = src.Events(ctx, after, limit)
		return res
	}
}

func (m Model) bodyCmd(id string) tea.Cmd {
	src := m.src
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), Timeout)
		defer cancel()
		msg, err := src.Message(ctx, id)
		return bodyMsg{id: id, msg: msg, err: err}
	}
}

// ---- update ----

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.fixScroll()
		return m, nil

	case tickMsg:
		if m.fetching {
			return m, tick()
		}
		m.fetching = true
		return m, tea.Batch(m.fetchCmd(), tick())

	case fetchedMsg:
		m.fetching = false
		m.applyFetch(msg)
		return m, nil

	case bodyMsg:
		if m.open != nil && m.open.id == msg.id {
			m.open.loading = false
			if msg.err != nil {
				m.open.err = bodyError(msg.err)
			} else {
				m.open.msg = msg.msg
			}
		}
		return m, nil

	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

// bodyError explains why a body is not there, in plain words.
func bodyError(err error) string {
	var re client.RemoteError
	if errors.As(err, &re) && strings.Contains(err.Error(), "unknown message") {
		return "The body is not available here. Only the machines with a session in the conversation can read it, and old messages are deleted."
	}
	if ClassifyError(err) == ProblemDaemon {
		return "The daemon cannot be reached."
	}
	return "Could not fetch the message: " + sanitize(err.Error())
}

func (m *Model) applyFetch(r fetchedMsg) {
	if r.err != nil {
		m.problem = ClassifyError(r.err)
		m.problemT = r.err.Error()
		return
	}
	m.snap.NodeID, m.snap.Version = r.snap.NodeID, r.snap.Version
	if !r.snap.Connected {
		// Keep the last sessions and machines on screen.
		m.problem, m.problemT = ProblemGateway, ErrGatewayDown.Error()
		return
	}
	m.problem, m.problemT = ProblemNone, ""
	m.snap, m.haveData, m.lastOK = r.snap, true, r.at
	if r.eventsErr != nil {
		if errors.Is(r.eventsErr, ErrFeedUnavailable) {
			m.feed.unavailable = "no message feed: the daemon or the gateway is older than the event feed"
		}
		// Any other error shows up as a problem on the next refresh.
	} else {
		wasLast := m.feedCur >= len(m.feed.items)-1
		m.feed.apply(r.events)
		if m.focus != focusFeed || wasLast {
			m.feedCur = len(m.feed.items) - 1
		}
	}
	m.ensureSelection()
	m.fixScroll()
}

// ---- keys ----

func (m Model) key(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.Type == tea.KeyCtrlC {
		m.quitting = true
		return m, tea.Quit
	}
	if m.filtering {
		switch k.Type {
		case tea.KeyEsc:
			m.filtering, m.filter = false, ""
			m.input.SetValue("")
			m.input.Blur()
		case tea.KeyEnter:
			m.filtering = false
			m.input.Blur()
		default:
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(k)
			m.filter = m.input.Value()
			m.ensureSelection()
			m.fixScroll()
			return m, cmd
		}
		m.ensureSelection()
		m.fixScroll()
		return m, nil
	}
	if m.open != nil {
		switch k.String() {
		case "q":
			m.quitting = true
			return m, tea.Quit
		case "esc", "enter", "backspace", "m":
			m.open = nil
		case "up", "k":
			m.open.scroll = max(m.open.scroll-1, 0)
		case "down", "j":
			m.open.scroll++
		case "pgup":
			m.open.scroll = max(m.open.scroll-10, 0)
		case "pgdown":
			m.open.scroll += 10
		}
		return m, nil
	}
	switch k.String() {
	case "q":
		m.quitting = true
		return m, tea.Quit
	case "/":
		m.filtering = true
		m.input.SetValue(m.filter)
		m.input.CursorEnd()
		return m, m.input.Focus()
	case "esc":
		if m.filter != "" {
			m.filter = ""
			m.input.SetValue("")
			m.ensureSelection()
			m.fixScroll()
		}
	case "o":
		m.showOffline = !m.showOffline
		m.ensureSelection()
		m.fixScroll()
	case "tab":
		if m.focus == focusSessions {
			m.focus = focusFeed
			m.feedCur = len(m.feed.items) - 1
		} else {
			m.focus = focusSessions
		}
	case "r":
		if !m.fetching {
			m.fetching = true
			return m, m.fetchCmd()
		}
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case "pgup":
		m.move(-m.layout().sessions)
	case "pgdown":
		m.move(m.layout().sessions)
	case "home", "g":
		m.move(-1 << 20)
	case "end", "G":
		m.move(1 << 20)
	case "enter":
		if m.focus == focusFeed {
			if it := m.feedItemAt(m.feedCur); it != nil && it.isMessage() {
				m.open = &openMsg{id: it.MsgID, loading: true}
				return m, m.bodyCmd(it.MsgID)
			}
		}
	}
	return m, nil
}

// move changes the selection by delta, in the list that has the focus.
func (m *Model) move(delta int) {
	if m.focus == focusFeed {
		m.feedCur = min(max(m.feedCur+delta, 0), max(len(m.feed.items)-1, 0))
		return
	}
	vis := m.visible()
	if len(vis) == 0 {
		return
	}
	i := m.indexOf(vis, m.selected)
	if i < 0 {
		i = 0
	}
	i = min(max(i+delta, 0), len(vis)-1)
	m.selected, m.selIdx = vis[i].SessionID, i
	m.fixScroll()
}

func (m Model) feedItemAt(i int) *feedItem {
	if i < 0 || i >= len(m.feed.items) {
		return nil
	}
	return m.feed.items[i]
}

// ---- the list of sessions ----

// group is the sessions of one machine.
type group struct {
	nodeID string
	host   string
	agents []protocol.AgentView
}

// matches tells whether an agent passes the filter: every word must appear in
// one of the fields, ignoring case.
func matches(a protocol.AgentView, filter string) bool {
	words := strings.Fields(strings.ToLower(filter))
	if len(words) == 0 {
		return true
	}
	hay := strings.ToLower(strings.Join([]string{a.Profile.Name, a.Harness, a.Hostname, a.Repo.Project, a.Profile.Project,
		a.Repo.Branch, a.Model, a.Mode, modeShort(a.Mode), a.Status, a.Profile.Function, a.Profile.Task, a.Cwd, a.SessionID,
		a.Repo.Remote}, "\n"))
	for _, w := range words {
		if !strings.Contains(hay, w) {
			return false
		}
	}
	return true
}

// groups returns the sessions that pass the filter and the offline switch,
// grouped by machine. The order is stable between refreshes: machines by name,
// online sessions first, then by name.
func (m Model) groups() []group {
	by := map[string]*group{}
	var order []*group
	for _, a := range m.snap.Agents {
		if !a.Online && !m.showOffline {
			continue
		}
		if !matches(a, m.filter) {
			continue
		}
		g := by[a.NodeID]
		if g == nil {
			g = &group{nodeID: a.NodeID, host: a.Hostname}
			by[a.NodeID] = g
			order = append(order, g)
		}
		g.agents = append(g.agents, a)
	}
	sort.Slice(order, func(i, j int) bool {
		if hi, hj := order[i].nodeID == m.snap.NodeID, order[j].nodeID == m.snap.NodeID; hi != hj {
			return hi // this machine first
		}
		a, b := strings.ToLower(order[i].host), strings.ToLower(order[j].host)
		if a != b {
			return a < b
		}
		return order[i].nodeID < order[j].nodeID
	})
	out := make([]group, 0, len(order))
	for _, g := range order {
		sort.SliceStable(g.agents, func(i, j int) bool {
			a, b := g.agents[i], g.agents[j]
			if a.Online != b.Online {
				return a.Online
			}
			an, bn := strings.ToLower(a.Profile.Name), strings.ToLower(b.Profile.Name)
			if an != bn {
				return an < bn
			}
			return a.SessionID < b.SessionID
		})
		out = append(out, *g)
	}
	return out
}

// visible is the sessions in the order they are drawn.
func (m Model) visible() []protocol.AgentView {
	var out []protocol.AgentView
	for _, g := range m.groups() {
		out = append(out, g.agents...)
	}
	return out
}

func (m Model) indexOf(list []protocol.AgentView, id string) int {
	for i, a := range list {
		if a.SessionID == id {
			return i
		}
	}
	return -1
}

// ensureSelection keeps the selected session valid after the data, the filter
// or the offline switch changed: it stays where it is, or moves to the session
// that took its place in the list.
func (m *Model) ensureSelection() {
	vis := m.visible()
	if len(vis) == 0 {
		m.selected, m.selIdx = "", 0
		return
	}
	if i := m.indexOf(vis, m.selected); i >= 0 {
		m.selIdx = i
		return
	}
	i := min(m.selIdx, len(vis)-1)
	m.selected, m.selIdx = vis[i].SessionID, i
}

// selectedAgent returns the selected session.
func (m Model) selectedAgent() (protocol.AgentView, bool) {
	for _, a := range m.snap.Agents {
		if a.SessionID == m.selected {
			return a, true
		}
	}
	return protocol.AgentView{}, false
}

// agentByID finds a session by its ID in the last snapshot.
func (m Model) agentByID(id string) (protocol.AgentView, bool) {
	for _, a := range m.snap.Agents {
		if a.SessionID == id {
			return a, true
		}
	}
	return protocol.AgentView{}, false
}

// tableRow is one line of the sessions table: a machine heading or a session.
type tableRow struct {
	header bool
	g      group
	agent  protocol.AgentView
}

func (m Model) tableRows() []tableRow {
	var rows []tableRow
	for _, g := range m.groups() {
		rows = append(rows, tableRow{header: true, g: g})
		for _, a := range g.agents {
			rows = append(rows, tableRow{agent: a})
		}
	}
	return rows
}

// fixScroll moves the table so the selected session is on screen.
func (m *Model) fixScroll() {
	rows := m.tableRows()
	n := m.layout().sessions
	sel := -1
	for i, r := range rows {
		if !r.header && r.agent.SessionID == m.selected {
			sel = i
		}
	}
	if sel < 0 || n <= 0 {
		m.tableTop = 0
		return
	}
	if sel < m.tableTop {
		m.tableTop = sel
		// Show the machine heading above the first session of a machine.
		if sel > 0 && rows[sel-1].header {
			m.tableTop = sel - 1
		}
	}
	if sel >= m.tableTop+n {
		m.tableTop = sel - n + 1
	}
	m.tableTop = min(max(m.tableTop, 0), max(len(rows)-n, 0))
}

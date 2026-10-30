package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	focusCTO
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
	inboxItems                         []protocol.InboxItem
	inboxOn, inboxDetail, inboxEditing bool
	inboxIndex                         int
	inboxError                         string
	inboxInput                         textinput.Model
	cto                                *ctoState
	ctoGeneration                      int
	src                                Source
	// loc and now are replaced in tests.
	loc *time.Location
	now func() time.Time

	width, height     int
	tab               int
	quotaScroll       int
	quotaShowExcluded bool

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
	activeOnly  bool
	// events says how much of the quiet session and worker news the feed
	// shows (l).
	events    feedEvents
	filter    string
	filtering bool
	input     textinput.Model
	focus     focus
	feedCur   int // index in the feed items when the feed has focus
	open      *openMsg
	quitting  bool

	// Actions (forms.go).
	mode        inputMode
	notice      notice
	compose     textinput.Model // the message prompt
	composeTo   string          // session ID the message goes to
	composeName string
	form        workerForm
	confirmID   string // worker the stop question is about
	confirmName string
	pol         policyForm
	intg        integrationsForm // the setup integrations of the whole network
	rename      textinput.Model  // the machine name prompt
	renameNode  string           // node ID the name is for
	renameShown string           // the name shown when the prompt opened
	renameSet   bool             // that name is the owner's, not the reported one

	// tmuxSocket is the socket of the tmux server this terminal runs in, or "".
	// execProcess replaces tea.Exec in tests.
	tmuxSocket string
	// herdrSocket is the herdr server this terminal runs in, or "".
	herdrSocket string
	// enterNote is the daemon's note for the command that runs now.
	enterNote   string
	execProcess func(tea.ExecCommand, tea.ExecCallback) tea.Cmd

	// CPU, memory and counts from the daemon (usage.go).
	usage usageData
	// top caches the top processes of sessions that were selected.
	top map[string]*topEntry
	// conv is the open conversation, or nil (conv.go).
	conv *convState
	// mapOn shows the read-only graph map instead of the session list, and mp
	// is its data (map.go).
	mapOn bool
	mp    mapState
	// help shows the key overlay; learned shows the playbook notes; expand
	// gives the detail pane the room of the message feed.
	help    bool
	learned bool
	expand  bool
}

// New returns a model that reads from src.
func New(src Source) Model {
	ti := textinput.New()
	ti.Prompt = "/"
	ti.CharLimit = 80
	ti.Cursor.SetMode(cursor.CursorHide)
	m := Model{src: src, loc: time.Local, now: time.Now, width: 80, height: 24, feed: newFeed(), input: ti, fetching: true,
		top: map[string]*topEntry{}}
	ct := textinput.New()
	ct.Prompt = "> "
	ct.Placeholder = "write to the CTO..."
	ct.CharLimit = 65536
	m.ctoGeneration = 1
	m.cto = &ctoState{input: ct, generation: 1}
	if _, ok := src.(ctoSource); ok {
		m.cto.polling = true
	}

	return m
}

// ---- messages ----

type tickMsg struct{}

type fetchedMsg struct {
	inboxItems []protocol.InboxItem
	inboxOK    bool
	snap       Snapshot
	err        error
	events     []protocol.FeedEvent
	eventsErr  error
	at         time.Time
	// usage and counts are set when this refresh asked for them.
	askedUsage bool
	usage      Usage
	usageErr   error
	counts     protocol.SessionCounts
	countsErr  error
}

type topMsg struct {
	id  string
	at  time.Time
	top protocol.UsageTop
	err error
}

type bodyMsg struct {
	id  string
	msg protocol.Message
	err error
}

// Init starts the first refresh and the clock that asks for the next ones.
// New marks a fetch as running, so the first tick does not start a second.
func (m Model) Init() tea.Cmd {
	if m.cto != nil && m.cto.polling {
		return tea.Batch(m.fetchCmd(), tick(), m.ctoPollCmd())
	}
	return tea.Batch(m.fetchCmd(), tick())
}

// tickDelay is RefreshEvery; tests shorten it.
var tickDelay = RefreshEvery

func tick() tea.Cmd {
	return tea.Tick(tickDelay, func(time.Time) tea.Msg { return tickMsg{} })
}

// fetchCmd reads a snapshot and the new events. One runs at a time.
func (m Model) fetchCmd() tea.Cmd {
	src, after := m.src, m.feed.lastID
	skipFeed := m.feed.unavailable != ""
	askUsage := m.usageDue()
	askCounts := askUsage && !m.usage.noCounts
	askU := askUsage && m.usage.unavailable == ""
	now := m.now
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), Timeout)
		defer cancel()
		var res fetchedMsg
		res.at = now()
		res.snap, res.err = src.Snapshot(ctx)
		if res.err != nil || !res.snap.Connected {
			return res
		}
		if askUsage {
			res.askedUsage = true
			if askU {
				res.usage, res.usageErr = src.Usage(ctx)
			} else {
				res.usageErr = ErrUsageUnavailable
			}
			if askCounts {
				res.counts, res.countsErr = src.Counts(ctx)
			} else {
				res.countsErr = ErrUsageUnavailable
			}
		}
		if c, ok := src.(inboxCaller); ok {
			var data struct {
				Items []protocol.InboxItem `json:"items"`
			}
			raw, err := c.Call(ctx, "inbox_list", nil)
			if err == nil && json.Unmarshal(raw, &data) == nil {
				res.inboxItems, res.inboxOK = data.Items, true
			}
		}
		if skipFeed {
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

func (m Model) topCmd(id string) tea.Cmd {
	src, now := m.src, m.now
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), Timeout)
		defer cancel()
		at := now()
		top, err := src.UsageTop(ctx, id)
		return topMsg{id: id, at: at, top: top, err: err}
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
	next, cmd := m.update(msg)
	nm := next.(Model)
	// After any change, fetch the top processes the detail pane wants.
	cmds := []tea.Cmd{cmd}
	if !nm.mapOn {
		if c := nm.wantTop(); c != nil {
			cmds = append(cmds, c)
		}
		if c := nm.wantSnippets(); c != nil {
			cmds = append(cmds, c)
		}
	}
	return nm, tea.Batch(cmds...)
}

func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.fixScroll()
		return m, nil

	case tickMsg:
		cmds := []tea.Cmd{tick()}
		if m.mapOn && !m.mp.loading {
			m.mp.loading = true
			cmds = append(cmds, m.mapCmd())
		}
		if _, ok := m.src.(ctoSource); ok && m.cto != nil && !m.cto.polling {
			m.cto.polling = true
			cmds = append(cmds, m.ctoPollCmd())
		}
		if m.conv != nil && m.conv.loaded {
			cmds = append(cmds, m.convCmd(convNewer)) // follow, even while a refresh runs
		}
		if !m.fetching {
			m.fetching = true
			cmds = append(cmds, m.fetchCmd())
		}
		return m, tea.Batch(cmds...)

	case ctoPollMsg:
		if m.cto == nil || m.cto.generation != msg.generation {
			return m, nil
		}
		oldID, loaded := m.cto.lastGraphID, m.cto.historyLoaded
		m.applyCTOPoll(msg)
		if msg.err == nil {
			m.cto.historyLoaded = true
			if len(msg.history) > 0 {
				m.cto.lastGraphID = msg.history[len(msg.history)-1].ID
			}
			if loaded && m.mapOn && !m.mp.loading && oldID != m.cto.lastGraphID {
				m.mp.loading = true
				return m, m.mapCmd()
			}
		}
		return m, nil
	case ctoUndoMsg:
		if m.cto == nil || m.cto.generation != msg.generation {
			return m, nil
		}
		if msg.err != nil {
			m.cto.err = sanitize(msg.err.Error())
			return m, nil
		}
		line := fmt.Sprintf("undid graph change #%d", msg.id)
		m.cto.local = append(m.cto.local, protocol.CTOAction{Text: line, At: m.now().UnixNano()})
		m.cto.messages = append(m.cto.messages, line)
		if m.mapOn {
			m.mp.loading = true
			return m, m.mapCmd()
		}
		return m, nil
	case ctoSendMsg:
		if m.cto != nil && m.cto.generation == msg.generation {
			if msg.err != nil {
				line := "error: " + sanitize(msg.err.Error())
				m.cto.local = append(m.cto.local, protocol.CTOAction{Text: line, At: m.now().UnixNano()})
				m.cto.messages = append(m.cto.messages, line)
			} else if !m.cto.polling {
				m.cto.polling = true
				return m, m.ctoPollCmd()
			}
		}
		return m, nil
	case ctoNewMsg:
		if m.cto != nil && m.cto.generation == msg.generation {
			if msg.err != nil {
				m.cto.err = sanitize(msg.err.Error())
			} else {
				m.cto.messages = nil
				m.cto.local = nil
				m.cto.history = nil
				m.cto.historyLoaded = false
				m.ctoGeneration++
				m.cto.generation = m.ctoGeneration
				m.cto.polling = true
				return m, m.ctoPollCmd()
			}
		}
		return m, nil
	case convMsg:
		m.applyConv(msg)
		return m, nil

	case topMsg:
		if e := m.top[msg.id]; e != nil {
			e.loading, e.at = false, msg.at
			if msg.err != nil {
				e.err, e.have = sanitize(msg.err.Error()), false
			} else {
				e.err, e.have, e.top = "", true, msg.top
			}
		}
		return m, nil

	case inboxAnswerMsg:
		if msg.err != nil {
			m.inboxError = sanitize(msg.err.Error())
		} else {
			m.inboxDetail = false
			m.inboxError = ""
			for i := range m.inboxItems {
				if m.inboxItems[i].ID == msg.id {
					m.inboxItems[i].Status = "answered"
				}
			}
		}
		return m, nil
	case fetchedMsg:
		if msg.inboxOK {
			m.inboxItems = msg.inboxItems
		}
		m.fetching = false
		m.applyFetch(msg)
		return m, nil

	case mapMsg:
		m.applyMap(msg)
		return m, nil

	case enterMsg:
		return m.onEnter(msg)

	case enterDoneMsg:
		return m.onEnterDone(msg)

	case actionMsg:
		return m.onAction(msg)

	case policyPreviewMsg:
		return m.onPolicyPreview(msg)

	case policySavedMsg:
		return m.onPolicySaved(msg)

	case integrationsLoadedMsg:
		return m.onIntegrationsLoaded(msg)

	case integrationsSavedMsg:
		return m.onIntegrationsSaved(msg)

	case snippetMsg:
		m.applySnippet(msg)
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
	m.applyUsage(r)
	if r.eventsErr != nil {
		if errors.Is(r.eventsErr, ErrFeedUnavailable) {
			m.feed.unavailable = "no message feed: the daemon or the gateway is older than the event feed"
		}
		// Any other error shows up as a problem on the next refresh.
	} else {
		wasLast := m.feedCur >= m.feedLastVisible()
		m.feed.apply(r.events)
		if m.focus != focusFeed || wasLast {
			m.feedCur = m.feedLastVisible()
		} else {
			m.feedClamp()
		}
	}
	m.ensureSelection()
	m.fixScroll()
}

// applyUsage keeps the usage and counts of a refresh that asked for them. An
// older daemon or gateway turns the feature off with a reason; another error
// keeps the last figures, which go stale by themselves.
func (m *Model) applyUsage(r fetchedMsg) {
	if !r.askedUsage {
		return
	}
	m.usage.at = r.at
	if r.usageErr == nil {
		m.usage.raw, m.usage.have, m.usage.unavailable = r.usage, true, ""
	} else if isUnavailable(r.usageErr) {
		m.usage.have, m.usage.unavailable = false, "CPU and memory need a newer daemon and gateway"
	}
	if r.countsErr == nil {
		c := r.counts
		m.usage.counts, m.usage.noCounts = &c, false
	} else if isUnavailable(r.countsErr) {
		m.usage.counts, m.usage.noCounts = nil, true
	}
}

// ---- keys ----

func (m Model) key(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.Type == tea.KeyCtrlC {
		m.quitting = true
		return m, tea.Quit
	}
	switch m.mode {
	case modeCompose:
		return m.composeKey(k)
	case modeForm:
		return m.formKey(k)
	case modeConfirm:
		return m.confirmKey(k)
	case modeTrust:
		return m.trustKey(k)
	case modePolicy:
		return m.policyKey(k)
	case modeRename:
		return m.renameKey(k)
	case modeIntegrations:
		return m.integrationsKey(k)
	}
	if m.inboxOn {
		return m.inboxKey(k)
	}
	if m.help {
		switch k.String() {
		case "q":
			m.quitting = true
			return m, tea.Quit
		case "esc", "?", "enter", "backspace":
			m.help = false
		}
		return m, nil
	}
	if m.learned {
		switch k.String() {
		case "q":
			m.quitting = true
			return m, tea.Quit
		case "esc", "L", "enter", "backspace":
			m.learned = false
		}
		return m, nil
	}
	if m.focus == focusCTO && m.cto != nil {
		return m.ctoKey(k)
	}
	if k.String() == "C" && !m.filtering {
		m.tab, m.mapOn, m.focus = 0, false, focusCTO
		m.open, m.conv = nil, nil
		if m.cto == nil {
			return m.startCTO()
		}
		m.cto.editing = true
		return m, m.cto.input.Focus()
	}
	if m.mapOn {
		return m.mapKey(k)
	}
	if m.conv != nil {
		return m.convKey(k)
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
	for i := range tabNames {
		if k.String() == fmt.Sprint(i+1) {
			m.tab = i
			m.focus = focusSessions
			if i == 1 {
				m.focus = focusFeed
				m.feedCur = m.feedLastVisible()
			}
			return m, nil
		}
	}
	if m.tab == 2 {
		switch k.String() {
		case "q":
			m.quitting = true
			return m, tea.Quit
		case "s":
			m.quotaShowExcluded = !m.quotaShowExcluded
			m.quotaScroll = 0
		case "down", "j":
			m.quotaScroll++
		case "up", "k":
			m.quotaScroll = max(0, m.quotaScroll-1)
		case "?":
			m.help = true
		case "r":
			if !m.fetching {
				m.fetching = true
				return m, m.fetchCmd()
			}
		}
		return m, nil
	}
	m.notice = notice{} // the result of the last action goes with the next key
	if m.focus == focusSessions {
		for _, held := range m.snap.Held {
			if held.ID == m.selected && (k.String() == "a" || k.String() == "A" || k.String() == "r" || k.String() == "R") {
				answerer, ok := m.src.(interface {
					AnswerHeld(context.Context, protocol.HeldAnswer) error
				})
				if !ok {
					m.fail("Held request actions unavailable.")
					return m, nil
				}
				answer := protocol.HeldAnswer{MessageID: held.ID, Action: k.String()}
				return m, func() tea.Msg {
					ctx, cancel := context.WithTimeout(context.Background(), Timeout)
					defer cancel()
					err := answerer.AnswerHeld(ctx, answer)
					if err != nil {
						return actionMsg{err: err}
					}
					return actionMsg{text: "Request answered."}
				}
			}
		}
	}
	switch k.String() {
	case "q":
		m.quitting = true
		return m, tea.Quit
	case "i":
		m.inboxOn = true
		m.inboxIndex = 0
		return m, nil
	case "m":
		return m.startCompose()
	case "s":
		if cmd := m.resolveOutcome("let_stand"); cmd != nil {
			return m, cmd
		}
		return m.startForm()
	case "e":
		return m.startEnter()
	case "x":
		return m.startConfirm()
	case "t":
		return m.startTrust()
	case "p":
		return m.startPolicy()
	case "I":
		return m.startIntegrations()
	case "N":
		return m.startRename()
	case "L":
		m.learned = true
	case "M":
		return m.startMap()
	case "?":
		m.help = true
	case "d":
		m.expand = !m.expand
		m.fixScroll()
	case "c":
		return m, m.openConv()
	case "R":
		for _, e := range m.snap.Outcomes {
			if m.selected == "outcome:"+e.ReplyID {
				return m, m.openConvFor(e.Sender)
			}
		}
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
		if cmd := m.resolveOutcome("overrule"); cmd != nil {
			return m, cmd
		}
		m.showOffline = !m.showOffline
		m.ensureSelection()
		m.fixScroll()
	case "a":
		m.activeOnly = !m.activeOnly
		m.ensureSelection()
		m.fixScroll()
	case "l":
		m.events = (m.events + 1) % 3
		m.feedClamp()
	case "v":
		if m.focus == focusFeed {
			if it := m.feedItemAt(m.feedCur); it != nil && it.isMessage() {
				m.open = &openMsg{id: it.MsgID, loading: true}
				return m, m.bodyCmd(it.MsgID)
			}
		}
	case "tab":
		if m.tab == 0 && m.cto != nil {
			m.focus = focusCTO
			m.cto.editing = true
			return m, m.cto.input.Focus()
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
			it := m.feedItemAt(m.feedCur)
			if it == nil || !it.isMessage() {
				return m, nil
			}
			// Open the conversation of the end on this machine; when there is
			// none, the message body explains why it cannot be read here.
			if id := m.feedConvID(it); id != "" {
				return m, m.openConvFor(id)
			}
			m.open = &openMsg{id: it.MsgID, loading: true}
			return m, m.bodyCmd(it.MsgID)
		}
		return m, m.openConv()
	}
	return m, nil
}

// convKey handles the keys of the conversation view.
func (m Model) convKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	c := m.conv
	switch k.String() {
	case "q":
		m.quitting = true
		return m, tea.Quit
	case "esc", "backspace", "c":
		m.conv = nil
	case "?":
		m.help = true
	case "up", "k":
		return m, m.convScroll(1)
	case "down", "j":
		return m, m.convScroll(-1)
	case "pgup", "ctrl+u":
		return m, m.convScroll(m.convRows() - 1)
	case "pgdown", "ctrl+d":
		return m, m.convScroll(-(m.convRows() - 1))
	case "home", "g":
		return m, m.convScroll(1 << 20)
	case "end", "G", "f":
		c.back = 0
	}
	return m, nil
}

// move changes the selection by delta, in the list that has the focus.
func (m *Model) move(delta int) {
	if m.focus == focusFeed {
		m.moveFeed(delta)
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
	// hidden is how many sessions the active-only filter hides on this
	// machine. It is 0 when the filter is off.
	hidden int
	// marks says how a worker row is drawn, by session ID. A session with no
	// mark is a plain row.
	marks map[string]treeMark
}

// idleAfter is how long a session may sit idle before the screen calls it
// quiet: dim on every screen, and hidden by the active-only filter.
const idleAfter = time.Hour

// idleLong tells whether a session is idle and has had no activity for more
// than idleAfter. LastSeen is the last time its machine reported it.
func (m Model) idleLong(a protocol.AgentView) bool {
	if !a.Online || a.Status != "idle" || a.LastSeen == 0 {
		return false
	}
	return m.now().Sub(time.Unix(a.LastSeen, 0)) > idleAfter
}

// sessionStart is the order key of a session: when the gateway first saw it.
// Older gateways do not say, so those sessions keep the order of their IDs.
func sessionStart(a protocol.AgentView) int64 {
	if a.FirstSeen > 0 {
		return a.FirstSeen
	}
	if a.Worker != nil && a.Worker.StartedAt > 0 {
		return a.Worker.StartedAt
	}
	return 0
}

// treeMark is how a worker row hangs under the session that started it.
type treeMark struct {
	prefix string // the tree glyphs before the name: "├─ ", "│  └─ ", or "↳ " for a lone worker
	host   string // the worker's machine, when it differs from the group's
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

// groups returns the sessions that pass the filter and the switches, grouped
// by machine. The order is stable between refreshes: machines by name, and
// within a machine the sessions by when they were first seen, oldest first.
// No other key sorts them.
func (m Model) groups() []group {
	by := map[string]*group{}
	var order []*group
	workers := false // any worker on screen
	for _, a := range m.snap.Agents {
		if protocol.IsOwner(a.Harness) {
			continue // the owner is a sender, not a row
		}
		// Dormant sessions are reachable but quiet: hidden with the offline ones.
		if (!a.Online || a.Status == protocol.StatusDormant) && !m.showOffline {
			continue
		}
		// A worker that ended is not a row, even when offline sessions are
		// shown: it is offline until its owner's next message starts it again.
		if a.Worker != nil && !a.Online {
			continue
		}
		if !matches(a, m.filter) {
			continue
		}
		g := by[a.NodeID]
		if g == nil {
			g = &group{nodeID: a.NodeID, host: m.nodeName(a.NodeID, a.Hostname)}
			by[a.NodeID] = g
			order = append(order, g)
		}
		if m.activeOnly && m.idleLong(a) {
			g.hidden++
			continue
		}
		g.agents = append(g.agents, a)
		if a.Worker != nil {
			workers = true
		}
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
			if sa, sb := sessionStart(a), sessionStart(b); sa != sb {
				return sa < sb
			}
			return a.SessionID < b.SessionID
		})
		out = append(out, *g)
	}
	return m.nestWorkers(out, workers)
}

// nestWorkers moves each worker under the session that started it, in that
// session's machine group, sorted by start time. A worker whose caller is not
// on screen (the owner, or a session hidden by the filter or the offline
// switch) stays in its own group and gets a lone marker. A worker is a root
// too when its chain of callers loops.
func (m Model) nestWorkers(groups []group, workers bool) []group {
	if !workers {
		return groups
	}
	shown := map[string]protocol.AgentView{}
	for _, g := range groups {
		for _, a := range g.agents {
			shown[a.SessionID] = a
		}
	}
	parentOf := func(a protocol.AgentView) string {
		if a.Worker == nil {
			return ""
		}
		if _, ok := shown[a.Worker.Owner]; !ok || a.Worker.Owner == a.SessionID {
			return ""
		}
		// Walk up: a loop of callers has no root, so none of it is nested.
		id := a.Worker.Owner
		for range shown {
			p, ok := shown[id]
			if !ok || p.Worker == nil {
				return a.Worker.Owner
			}
			if p.Worker.Owner == a.SessionID {
				return ""
			}
			id = p.Worker.Owner
		}
		return ""
	}
	children := map[string][]protocol.AgentView{}
	for _, a := range shown {
		if p := parentOf(a); p != "" {
			children[p] = append(children[p], a)
		}
	}
	for _, c := range children {
		sort.Slice(c, func(i, j int) bool {
			if c[i].Worker.StartedAt != c[j].Worker.StartedAt {
				return c[i].Worker.StartedAt < c[j].Worker.StartedAt
			}
			return c[i].SessionID < c[j].SessionID
		})
	}
	out := make([]group, 0, len(groups))
	for _, g := range groups {
		ng := group{nodeID: g.nodeID, host: g.host, hidden: g.hidden, marks: map[string]treeMark{}}
		var walk func(a protocol.AgentView, lead string)
		walk = func(a protocol.AgentView, lead string) {
			ng.agents = append(ng.agents, a)
			cs := children[a.SessionID]
			for i, c := range cs {
				glyph, next := "├─ ", lead+"│  "
				if i == len(cs)-1 {
					glyph, next = "└─ ", lead+"   "
				}
				mk := treeMark{prefix: lead + glyph}
				if c.NodeID != g.nodeID {
					mk.host = m.nodeName(c.NodeID, c.Hostname)
				}
				ng.marks[c.SessionID] = mk
				walk(c, next)
			}
		}
		for _, a := range g.agents {
			if parentOf(a) != "" {
				continue
			}
			if a.Worker != nil {
				ng.marks[a.SessionID] = treeMark{prefix: "↳ "}
			}
			walk(a, "")
		}
		if len(ng.agents) > 0 || ng.hidden > 0 { // a machine whose sessions all hang under others or are hidden still shows its heading
			out = append(out, ng)
		}
	}
	return out
}

// visible is the sessions in the order the keys move through: the "Needs you"
// rows first, then the table. A waiting session is reached from the section,
// so it is not selectable a second time in the table.
func (m Model) visible() []protocol.AgentView {
	needs := m.needsRows()
	out := append([]protocol.AgentView(nil), needs...)
	seen := map[string]bool{}
	for _, a := range needs {
		seen[a.SessionID] = true
	}
	for _, g := range m.groups() {
		for _, a := range g.agents {
			if !seen[a.SessionID] {
				out = append(out, a)
			}
		}
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

// nodeName is the name of a machine as the gateway knows it. A session's own
// hostname can differ (a Mac reports "Nicolass-MacBook-Pro-950.local" where
// the gateway says "NicolassMBP950"), so headings use the node list and fall
// back to the session's hostname only when the node is not listed.
func (m Model) nodeName(nodeID, fallback string) string {
	for _, n := range m.snap.Nodes {
		if n.NodeID == nodeID && n.Hostname != "" {
			return n.Hostname
		}
	}
	return fallback
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
	mark   treeMark // how a worker hangs under its caller
}

func (m Model) tableRows() []tableRow {
	var rows []tableRow
	for _, g := range m.groups() {
		rows = append(rows, tableRow{header: true, g: g})
		for _, a := range g.agents {
			rows = append(rows, tableRow{agent: a, mark: g.marks[a.SessionID]})
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
	// A table scrolled past its machine heading repeats it on its first line,
	// which leaves one row less for sessions.
	room := func(top int) int {
		if top > 0 && top < len(rows) && !rows[top].header {
			return n - 1
		}
		return n
	}
	for sel >= m.tableTop+room(m.tableTop) {
		m.tableTop++
	}
	m.tableTop = min(max(m.tableTop, 0), max(len(rows)-1, 0))
}

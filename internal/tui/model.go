package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/niparis/agent-net/internal/client"
	"github.com/niparis/agent-net/internal/operator"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/tui/graphview"
	"github.com/niparis/agent-net/internal/tui/palette"
	"github.com/niparis/agent-net/internal/tui/tabs"
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
	restart               *restartState
	archive               *sessionsArchiveForm
	removeIDs             []string
	removeAll             bool
	workerLogDetail       int
	workerLogDetailsFocus bool
	workersLog            bool
	workerLogRow          int
	delivery              deliveryState
	deliverySrc           deliverySource
	proposals             []protocol.CTOProposal
	proposalDetail        *proposalDetail
	proposalConfirm       bool
	proposalConfirmItems  []protocol.CTOProposal
	proposalConfirmScroll int
	proposalHistory       bool
	proposalHistoryIndex  int
	asks                  []protocol.Ask
	deskLog               []protocol.DeskLogEntry
	deskLogOpen           bool
	// The chief of staff (decision 0114, part 2): its status, what it handled
	// in the last day, the digest of the Desk, the owner's last key and the mark
	// whose why is being typed.
	chief                               protocol.ChiefStatus
	chiefLoaded                         bool
	handled                             []protocol.ChiefSort
	digest                              *deskDigest
	lastInput                           time.Time
	asksVersion                         int64
	deskCache                           *deskCacheState
	deskPending                         *deskMark
	inboxDetail, inboxEditing           bool
	inboxDeliveryID                     string
	inboxIndex, inboxDetailScroll       int
	inboxDetailOption                   int
	inboxError                          string
	inboxNote                           string
	inboxInput                          textinput.Model
	cto                                 *ctoState
	ctoGeneration                       int
	onboarding                          protocol.Onboarding
	onboardingLoaded, onboardingPending bool
	onboardingRevision                  int
	onboardingQueue                     []onboardingWriteRequest
	onboardingCTOLast                   string
	onboardingCTOLoaded                 bool
	onboardingSawNoAgents               bool
	onboardingToolsCount                int
	onboardingToolsKnown                bool
	src                                 Source
	// loc and now are replaced in tests.
	loc  *time.Location
	now  func() time.Time
	home string // the local home folder, used by ~ paths
	// zoneName is the display zone the owner's setting resolved to, and
	// zoneSource says where it came from ("set by you", "auto, from X").
	zoneName   string
	zoneSource string

	width, height       int
	tab                 int
	quotaScroll         int
	quotaSelected       int
	quotaConfirm        string
	quotaConfirmDisplay string
	quotaUnprotect      bool
	quotaShowUnset      bool
	qualityDetail       bool
	// The picker table of the Quality subtab. It changes only on writes, so it
	// is read when the subtab opens and on R, not on every tick.
	picker            protocol.PickerAnalysis
	pickerLoaded      bool
	pickerErr         string
	pickerWanted      bool
	pickerSelModel    int
	pickerSelBucket   int
	pickerShowOlder   bool
	weeklyScores      []protocol.ScoresWeeklyRow
	weeklyLoaded      bool
	weeklyErr         string
	weeklyWanted      bool
	showWeekly        bool
	messageShowEvents bool
	hideWorkerThreads bool
	messageSubtab     messageSubtab
	threadProject     string
	threadTeam        string
	threadExpanded    map[string]bool

	// What the daemon last told us. It stays on screen when a refresh fails.
	snap                   Snapshot
	haveData               bool
	lastOK                 time.Time
	problem                Problem
	problemT               string // text of the last error
	graphState             graphview.State
	graphPixel             *graphPixelState
	graphGeneration        int
	graphTalkLoading       bool
	graphTalkLastID        int64
	graphTalkPending       []graphview.FeedEvents
	graphTalkAt            time.Time
	graphDragging          bool
	graphDragX, graphDragY int
	graphLastClick         time.Time
	graphLastNode          string
	feed                   feed
	fetching               bool
	topicActive            bool
	topicSubscribing       bool
	topicRetryAt           time.Time
	topicLastFull          time.Time
	// topicFetchedAt is when the last read that finished began. Changed topics
	// are read again no sooner than topicRefreshGap after it.
	topicFetchedAt time.Time
	topicVersions  map[string]uint64
	topicDirty     map[string]bool
	// dataVersions is the topic version each part of snap was read at, and
	// dataVersionsAt is when that read happened. A safety refresh sends the
	// version only while the read is younger than the worker window, because
	// some rows change with time even when their table does not.
	dataVersions   map[string]uint64
	dataVersionsAt map[string]time.Time
	topicView      string
	// inboxSeen is the Snapshot signature the Inbox and proposals were last
	// fetched at. A different signature, or the Inbox or CTO pane on screen,
	// asks for them again. inboxAt is when they were last asked for away from
	// that screen, so the count cannot stay stale.
	inboxSeen string
	inboxAt   time.Time
	// proposalsAt is when the proposals were last asked for.
	proposalsAt time.Time

	// Stored conversations for the Messages tab. The gateway answers a page;
	// View only reads convs, never calls the source.
	storedConvs       []protocol.ConversationView
	convs             []messageConversation
	convTotal         int
	convVersion       uint64
	convNext          string
	convLoaded        bool
	convLoading       bool
	convErr           string
	convSel           int
	convDetail        map[string]convDetail
	convDetailLoading map[string]bool
	// The owner's scroll in the open conversation: the first line shown, for
	// the conversation threadFor. Without it the pane follows the newest line
	// or the selected message.
	threadScrolled bool
	threadTop      int
	threadFor      string

	// What the person chose.
	collapsedTrees            map[string]bool
	sessionOrder              map[string]sessionOrder
	sessionNext, sessionFront int64
	orderFrozenUntil          time.Time
	frozenOrder               map[string]int
	selected                  string // session ID
	selIdx                    int    // position in the visible list, to move on when the session leaves
	tableTop                  int    // first table row on screen
	resumeConfirm             *protocol.AgentView
	moveConfirm               *protocol.AgentView // a hand-started Claude session the owner may move to the background
	showOffline               bool
	activeOnly                bool
	hiddenSessions            map[string]bool
	pendingStops              map[string]pendingStop
	hiddenNodes               map[string]bool
	showHiddenSessions        bool
	showHiddenNodes           bool
	settingsRow               int
	settingValues             []protocol.SettingView
	nudgeStats                protocol.NudgeStatsResult
	nudgeStatsAt              time.Time
	nudgeStatsErr             error
	standing                  []protocol.StandingOrder
	notes                     []protocol.StandingOrder
	standingRemoved           *protocol.StandingOrder
	settingEdit               int
	settingRepo               string
	settingUndoRevision       int64
	sessionCell               int
	filter                    string
	uiFilter                  string
	filtering                 bool
	finder                    bool
	finderQuery               string
	finderSel                 int
	finderTop                 int
	input                     textinput.Model
	focus                     focus
	feedCur                   int // index in the feed items when the feed has focus
	open                      *openMsg
	quitting                  bool

	// Actions (forms.go).
	mode            inputMode
	notice          notice
	compose         textinput.Model // the message prompt
	composeTo       string          // session ID the message goes to
	composeName     string
	sform           sessionForm
	leadPicker      leadPicker
	sessionPending  *sessionAsk // the create or trust question of a start
	confirmID       string      // session the stop question is about
	confirmClose    bool        // selected owner session closes in the OpenCode service
	confirmTerminal bool        // selected session runs in a terminal
	confirmOwner    bool        // owner session rather than a worker
	confirmDefect   bool        // confirm reporting a defect found later
	confirmMachine  string      // machine_remove or machine_rotate
	confirmName     string
	defect          textinput.Model // the one-line defect note
	defectSeverity  textinput.Model // the 1 to 4 severity prompt
	defectWorker    string          // worker session the defect is about
	defectName      string          // how the prompt names the worker
	defectLevel     int             // the severity the owner chose
	pol             policyForm
	apps            appsForm
	intg            integrationsForm // the setup integrations of the whole network
	rename          textinput.Model  // the machine name prompt
	renameNode      string           // node ID the name is for
	renameShown     string           // the name shown when the prompt opened
	renameSet       bool             // that name is the owner's, not the reported one

	// herdrSocket is the herdr server this terminal runs in, or "".
	herdrSocket         string
	herdrPaneID         string
	sessionNoticeSerial uint64
	// enterNote is the daemon's note for the command that runs now.
	enterNote   string
	execProcess func(tea.ExecCommand, tea.ExecCallback) tea.Cmd

	// CPU, memory and counts from the daemon (usage.go).
	usage usageData
	// top caches the top processes of sessions that were selected.
	top map[string]*topEntry
	// mapOn shows the read-only graph map instead of the session list, and mp
	// is its data (map.go).
	mapOn bool
	mp    mapState
	// teamFilter is the team ID the Graph shows, or "" for all. teamIndex and
	// teamForm drive the Team list on the Graph tab (teams.go).
	teamFilter  string
	teamIndex   int
	teamForm    teamForm
	teamUndo    *teamUndoStep
	charterForm charterForm
	// tl is the Tools tab (tools.go).
	tl toolsState
	// help shows the key overlay; expand gives the detail pane the room of
	// the message feed.
	help       bool
	helpAll    bool // the full list of every screen, not just this one
	helpScroll int
	expand     bool
}

// New returns a model that reads from src.
func New(src Source) Model {
	ti := newTextInput()
	ti.Prompt = "/"
	ti.CharLimit = 80
	ti.SetVirtualCursor(false)
	m := Model{src: src, loc: time.Local, now: time.Now, home: localHomeDir(), width: 80, height: 24, feed: newFeed(), input: ti, fetching: true, activeOnly: true,
		top: map[string]*topEntry{}, hiddenSessions: map[string]bool{}, hiddenNodes: map[string]bool{},
		convDetail: map[string]convDetail{}, convDetailLoading: map[string]bool{},
		graphPixel: &graphPixelState{imageID: graphview.ImageID},
		deskCache: &deskCacheState{
			rowLines: make(map[inboxRowLineKey]string),
			whyLines: make(map[inboxWhyLineKey]string),
		}}
	if _, ok := src.(interface {
		SubscribeTopics(context.Context) (*operator.TopicSubscription, error)
	}); ok {
		m.topicSubscribing = true
	}
	if c, ok := src.(inboxCaller); ok {
		m.deliverySrc = newGatewayDelivery(c.Call, time.Now)
	}
	m.ctoGeneration = 1
	m.cto = &ctoState{generation: 1}
	if _, ok := src.(ctoSource); ok {
		m.cto.polling = true
	}

	return m
}

// ---- messages ----

type tickMsg struct{}

type topicSubscribedMsg struct {
	sub *operator.TopicSubscription
	err error
}
type topicsChangedMsg struct {
	sub      *operator.TopicSubscription
	versions map[string]uint64
	ok       bool
}

type pendingStop struct {
	verb string
	at   time.Time
}

type fetchedMsg struct {
	proposals       []protocol.CTOProposal
	proposalsOK     bool
	proposalsAsked  bool // the proposals were asked for this refresh
	asks            []protocol.Ask
	inboxOK         bool
	chief           protocol.ChiefStatus
	chiefOK         bool
	handled         []protocol.ChiefSort
	handledOK       bool
	deskLog         []protocol.DeskLogEntry
	deskLogOK       bool
	listsAsked      bool // the Inbox and proposals were asked for this refresh
	snap            Snapshot
	err             error
	topicVersions   map[string]uint64
	fetchedVersions map[string]uint64
	topicFull       bool
	topicView       string
	events          []protocol.FeedEvent
	eventsErr       error
	at              time.Time
	// conversations is the first stored page, asked for only on the Messages
	// tab.
	convAsked bool
	convPage  protocol.ConversationPage
	convErr   error
	// usage and counts are set when this refresh asked for them.
	askedUsage bool
	usage      Usage
	usageErr   error
	counts     protocol.SessionCounts
	countsErr  error
	hidden     protocol.HiddenItems
	hiddenErr  error
	// picker is read only when the Quality subtab opens or on R. It changes
	// on writes, not on every refresh.
	pickerAsked          bool
	picker               protocol.PickerAnalysis
	pickerErr            error
	weeklyAsked          bool
	weekly               []protocol.ScoresWeeklyRow
	weeklyErr            error
	onboarding           protocol.Onboarding
	onboardingErr        error
	onboardingAsked      bool
	onboardingRevision   int
	onboardingToolsCount int
	onboardingToolsKnown bool
}

type notificationSettingMsg struct {
	disabled bool
	err      error
}

type hiddenItemsMsg struct {
	items protocol.HiddenItems
	err   error
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
	cmds := []tea.Cmd{tea.RequestBackgroundColor, m.fetchCmd(), tick()}
	if _, ok := m.src.(interface {
		SubscribeTopics(context.Context) (*operator.TopicSubscription, error)
	}); ok {
		cmds = append(cmds, m.subscribeTopicsCmd())
	}
	if m.deliverySrc != nil {
		cmds = append(cmds, func() tea.Msg { return deliveryStartMsg{} })
	}
	if m.cto != nil && m.cto.polling && m.showCTOBox(m.tab) {
		cmds = append(cmds, m.ctoPollCmd())
	}
	return tea.Batch(cmds...)
}

func (m Model) subscribeTopicsCmd() tea.Cmd {
	src := m.src.(interface {
		SubscribeTopics(context.Context) (*operator.TopicSubscription, error)
	})
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), Timeout)
		defer cancel()
		sub, err := src.SubscribeTopics(ctx)
		return topicSubscribedMsg{sub: sub, err: err}
	}
}

func waitTopicsCmd(sub *operator.TopicSubscription) tea.Cmd {
	return func() tea.Msg {
		versions, ok := sub.Wait()
		return topicsChangedMsg{sub: sub, versions: versions, ok: ok}
	}
}

// forgetReadVersions drops the held topic versions and read times. Versions
// are counters in the gateway process, so a new subscription, or a lost one,
// cannot trust the old values.
func (m Model) forgetReadVersions() Model {
	m.dataVersions = nil
	m.dataVersionsAt = nil
	m.delivery.dataVersion = 0
	m.delivery.dataAt = time.Time{}
	return m
}

// topicRefreshGap is the shortest time between two reads that a changed topic
// asks for. On a busy network some topic moves every second (every session
// report writes the sessions table), and a read for each move was a full read
// of every list each second. The first change after a quiet time is read at
// once, and the changes of a busy time are read together, so a part of the
// screen is at most this old. The reads the owner asks for do not wait.
const topicRefreshGap = 3 * time.Second

func (m Model) topicRefreshDue() bool {
	changed := len(m.topicDirty) != 0 && (m.topicFetchedAt.IsZero() || m.now().Sub(m.topicFetchedAt) >= topicRefreshGap)
	return !m.topicActive || changed || m.topicLastFull.IsZero() || m.now().Sub(m.topicLastFull) >= 30*time.Second || m.topicView != m.topicViewKey()
}

func (m Model) topicViewKey() string {
	return fmt.Sprintf("%d:%d:%t:%t:%t", m.tab, m.focus, m.workersLog, m.deskLogOpen, m.mapOn)
}

// tickDelay is RefreshEvery; tests shorten it.
var tickDelay = RefreshEvery

func tick() tea.Cmd {
	return tea.Tick(tickDelay, func(time.Time) tea.Msg { return tickMsg{} })
}

// nextTick refreshes faster while a result is on the key line, so its fade is
// visible. The normal refresh is too slow for three colour steps.
func (m Model) nextTick() tea.Cmd {
	delay := tickDelay
	if m.activeNotice() != nil {
		delay = noticeAnimStep
	}
	return tea.Tick(delay, func(time.Time) tea.Msg { return tickMsg{} })
}

// fetchCmd reads a snapshot and the new events. One runs at a time.
func (m Model) fetchCmd() tea.Cmd {
	src, after := m.src, m.feed.lastID
	full := !m.topicActive || len(m.topicDirty) == 0 || m.topicLastFull.IsZero() || m.now().Sub(m.topicLastFull) >= 30*time.Second || m.topicView != m.topicViewKey()
	topics := make(map[string]bool, len(m.topicDirty))
	versions := make(map[string]uint64, len(m.topicDirty))
	for topic := range m.topicDirty {
		topics[topic], versions[topic] = true, m.topicVersions[topic]
	}
	view := m.topicViewKey()
	previous := m.snap
	// Every fetch keeps what it does not ask for, and a part the gateway says
	// is unchanged. The full safety refresh therefore only pays for parts
	// whose topic moved since the last read.
	allVersions := make(map[string]uint64, len(m.topicVersions))
	for topic, v := range m.topicVersions {
		allVersions[topic] = v
	}
	// A held version is only good while the part was read inside the worker
	// window. OwnerUses decays and ended workers leave the list with time,
	// though no table write moves the topic.
	readAtNow := m.now()
	ifVersions := make(map[string]uint64, len(m.dataVersions))
	for topic, v := range m.dataVersions {
		at, ok := m.dataVersionsAt[topic]
		if ok && readAtNow.Sub(at) < protocol.RecentWorkerWindow {
			ifVersions[topic] = v
		}
	}
	wants := func(topic string) bool { return full || topics[topic] }
	onboardingRevision, onboardingPending := m.onboardingRevision, m.onboardingPending
	skipFeed := m.feed.unavailable != ""
	askUsage := full && m.usageDue() || topics[protocol.TopicUsage]
	askCounts := askUsage && !m.usage.noCounts
	askU := askUsage && m.usage.unavailable == ""
	askTools := m.onboardingWantsTools()
	inboxOnScreen := m.tab == inboxTab || m.focus == focusCTO
	inboxSeen, inboxAt, proposalsAt := m.inboxSeen, m.inboxAt, m.proposalsAt
	deskLogOpen := m.deskLogOpen
	wantWorkerLog := m.tab == quotasTab && m.workersLog
	wantPicker := m.tab == quotasTab && !m.workersLog && m.pickerWanted
	wantWeekly := m.tab == quotasTab && m.qualityDetail && (m.weeklyWanted || !m.weeklyLoaded)
	now := m.now
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), Timeout)
		defer cancel()
		var res fetchedMsg
		res.at, res.topicVersions, res.fetchedVersions, res.topicFull, res.topicView = now(), versions, allVersions, full, view
		if with, ok := src.(interface {
			SnapshotWith(context.Context, SnapshotOptions) (Snapshot, error)
		}); ok {
			opts := SnapshotOptions{WorkerLog: wantWorkerLog, Previous: previous, IfVersions: ifVersions, Lite: true}
			if !full {
				opts.Topics = topics
			}
			res.snap, res.err = with.SnapshotWith(ctx, opts)
		} else {
			res.snap, res.err = src.Snapshot(ctx)
		}
		if res.err != nil || !res.snap.Connected {
			return res
		}
		res.hiddenErr = errors.New("not requested")
		// The rest are independent reads. They run together, each with its own
		// deadline, so one slow read no longer makes the others wait for it,
		// or run out of the deadline it used up. Each group sets its own
		// fields of res.
		var wg sync.WaitGroup
		group := func(read func(ctx context.Context)) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ctx, cancel := context.WithTimeout(context.Background(), Timeout)
				defer cancel()
				read(ctx)
			}()
		}
		if wants(protocol.TopicAgents) || wants(protocol.TopicWorkers) {
			group(func(ctx context.Context) { res.hidden, res.hiddenErr = src.HiddenItems(ctx) })
		}

		if wantPicker {
			res.pickerAsked = true
			group(func(ctx context.Context) {
				if caller, ok := src.(interface {
					Call(context.Context, string, any) (json.RawMessage, error)
				}); ok {
					raw, err := caller.Call(ctx, protocol.OpPickerGet, nil)
					if err == nil {
						err = json.Unmarshal(raw, &res.picker)
					}
					res.pickerErr = err
				} else {
					res.pickerErr = ErrTooOld
				}
			})
		}

		if wantWeekly {
			res.weeklyAsked = true
			group(func(ctx context.Context) {
				if caller, ok := src.(interface {
					Call(context.Context, string, any) (json.RawMessage, error)
				}); ok {
					raw, err := caller.Call(ctx, protocol.OpScoresWeekly, nil)
					if err == nil {
						err = json.Unmarshal(raw, &res.weekly)
					}
					res.weeklyErr = err
				} else {
					res.weeklyErr = ErrTooOld
				}
			})
		}

		if caller, ok := src.(inboxCaller); ok && !onboardingPending && (wants(protocol.TopicInbox) || wants(protocol.TopicWorkers)) {
			res.onboardingAsked, res.onboardingRevision = true, onboardingRevision
			group(func(ctx context.Context) {
				var raw json.RawMessage
				raw, res.onboardingErr = caller.Call(ctx, "onboarding_get", nil)
				if res.onboardingErr == nil {
					res.onboardingErr = json.Unmarshal(raw, &res.onboarding)
				}
			})
		}
		if d, ok := src.(interface {
			ToolsDifferences(context.Context) (int, error)
		}); ok && askUsage && askTools && len(res.snap.Nodes) > 1 {
			group(func(ctx context.Context) {
				count, err := d.ToolsDifferences(ctx)
				if err == nil {
					res.onboardingToolsCount, res.onboardingToolsKnown = count, true
				}
			})
		}
		if askUsage {
			res.askedUsage = true
			group(func(ctx context.Context) {
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
			})
		}
		// The list is needed at once on the screen that shows it, or when the
		// Snapshot says that what waits for the owner changed. Away from that
		// screen it is still checked now and then, because gateway items and
		// proposals made on another machine do not change the Snapshot.
		//
		// The gateway's Inbox topic moves with every session report, as the
		// agents topic does, so it is no reason to ask for the proposals: they
		// are the machines' own, and each ask goes to every online machine. A
		// proposal on this machine changes the count in the Snapshot, and one
		// on another machine is found by the wait.
		markerMoved := inboxMarkerOf(res.snap) != inboxSeen
		listsWanted := wants(protocol.TopicInbox) || wants(protocol.TopicAgents) || wants(protocol.TopicLearned)
		c, ok := src.(inboxCaller)
		if ok && listsWanted && (markerMoved || res.at.Sub(proposalsAt) >= inboxRefreshEvery || (inboxOnScreen && full)) {
			res.proposalsAsked = true
			group(func(ctx context.Context) {
				if raw, err := c.Call(ctx, protocol.OpProposalList, nil); err == nil && json.Unmarshal(raw, &res.proposals) == nil {
					res.proposalsOK = true
				}
			})
		}
		if ok && listsWanted && (markerMoved || res.at.Sub(inboxAt) >= inboxRefreshEvery || topics[protocol.TopicInbox] || (inboxOnScreen && full)) {
			res.listsAsked = true
			group(func(ctx context.Context) {
				var data protocol.AsksResult
				raw, err := c.Call(ctx, protocol.OpAsksList, nil)
				if err == nil && json.Unmarshal(raw, &data) == nil {
					res.asks, res.inboxOK = data.Asks, true
				}
			})
			group(func(ctx context.Context) {
				var status protocol.ChiefStatus
				if raw, err := c.Call(ctx, protocol.OpChiefGet, nil); err == nil && json.Unmarshal(raw, &status) == nil {
					res.chief, res.chiefOK = status, true
				}
			})
			group(func(ctx context.Context) {
				var sorts protocol.ChiefSortsResult
				if raw, err := c.Call(ctx, protocol.OpChiefSorts, nil); err == nil && json.Unmarshal(raw, &sorts) == nil {
					res.handled, res.handledOK = sorts.Sorts, true
				}
			})
			if deskLogOpen {
				group(func(ctx context.Context) {
					var log protocol.DeskLogResult
					raw, err := c.Call(ctx, protocol.OpDeskLog, protocol.DeskLogQuery{Limit: deskLogShown})
					if err == nil && json.Unmarshal(raw, &log) == nil {
						res.deskLog, res.deskLogOK = log.Entries, true
					}
				})
			}
		}
		if !skipFeed && wants(protocol.TopicChanges) {
			limit := 100
			if after == 0 {
				limit = backlog
			}
			group(func(ctx context.Context) {
				res.events, res.eventsErr = src.Events(ctx, after, limit)
			})
			if m.tab == messagesTab {
				res.convAsked = true
				group(func(ctx context.Context) {
					res.convPage, res.convErr = src.Conversations(ctx, protocol.QueryConversations{
						Limit: conversationPageSize, Lite: true, IfVersion: m.convVersion,
					})
				})
			}
		}
		wg.Wait()
		return res
	}
}

// inboxMarkerOf describes what the Snapshot says waits for the owner. It is a
// change marker, not the list itself: when it differs the Inbox and proposals
// are fetched again, even on a tab that does not show them.
func inboxMarkerOf(s Snapshot) string {
	var b strings.Builder
	fmt.Fprintf(&b, "p%d;", s.ProposalCount)
	for _, learned := range s.Learned {
		if learned.Status == "pending" && learned.Severity == "major" {
			fmt.Fprintf(&b, "l%d;", learned.ID)
		}
	}
	for _, a := range s.Agents {
		if a.Waiting == nil || a.Worker != nil || protocol.IsOwner(a.Harness) {
			continue
		}
		fmt.Fprintf(&b, "w%s:%d:%s;", a.SessionID, a.Waiting.Since, a.Waiting.Text)
	}
	return b.String()
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
	nm.tellScreen()
	cmds := []tea.Cmd{cmd}
	if !nm.graphMouseActive() || nm.quitting {
		nm.graphDragging = false
		nm.graphLastNode = ""
	}

	if !nm.mapOn {
		if c := nm.wantTop(); c != nil {
			cmds = append(cmds, c)
		}
		if c := nm.wantSnippets(); c != nil {
			cmds = append(cmds, c)
		}
		if c := nm.wantConversations(); c != nil {
			cmds = append(cmds, c)
		}
		if c := nm.wantConversationDetails(); c != nil {
			cmds = append(cmds, c)
		}
	}
	if c := nm.scheduleGraphRender(); c != nil {
		cmds = append(cmds, c)
	}
	return nm, tea.Batch(cmds...)
}

func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case removeSessionsMsg:
		return m.removeSessionsResult(msg)
	case graphRemovedMsg:
		return m.onGraphRemoved(msg)
	case sessionsArchiveMsg:
		return m.sessionsArchiveResult(msg)
	case sessionPinMsg:
		return m.sessionPinResult(msg)
	case tea.MouseMsg:
		if m.messagesMouseActive() {
			return m.messagesMouse(msg), nil
		}
		return m.graphMouse(msg), nil
	case tea.PasteMsg:
		return m.paste(msg)
	case tea.BackgroundColorMsg:
		// The reply arrives after the first frame, so the screen starts dark
		// and switches to the light palette here without a restart.
		palette.SetDark(msg.IsDark())
		m.smoothCamera()
		return m, nil
	case tea.ColorProfileMsg:
		palette.SetProfile(msg.Profile)
		if g := m.graphPixel; g != nil && g.profile != msg.Profile {
			g.profile = msg.Profile
			// Reject frames started under the old profile, including when a
			// terminal changes profile more than once while one is in flight.
			g.key = [32]byte{}
		}
		m.smoothCamera()
		return m, nil
	case protocol.UIRequest:
		return m.applyCTOUI(msg)
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.fixScroll()
		return m, nil

	case graphTickMsg:
		if m.tab == graphTab && !m.mapOn && msg.generation == m.graphGeneration {
			m.smoothCamera()
			m.graphState, _ = graphview.Update(m.graphState, graphview.Tick{Now: m.now()})
			return m, graphTickAfter(m.graphGeneration, m.graphFrameInterval())
		}
		return m, nil

	case graphFrameMsg:
		g := m.graphPixel
		if g == nil || msg.key != g.key {
			if g != nil {
				g.rendering = false
			}
			return m, nil
		}
		g.rendering = false
		g.size = msg.size
		g.lines = msg.lines
		g.hits = msg.hits
		g.hash = msg.hash
		g.have = msg.have
		g.transmit = msg.transmit
		g.shown = msg.id
		g.ready = len(msg.lines) > 0
		return m, nil

	case graphTalkMsg:
		m.graphTalkLoading = false
		m.graphTalkAt = msg.at
		if msg.err == nil {
			m.graphTalkLastID = msg.result.LastID
			m.graphState, _ = graphview.Update(m.graphState, graphview.TalkLoaded{Counts: msg.result.Counts, Activity: msg.result.Activity})
		}
		if msg.err == nil {
			for _, pending := range m.graphTalkPending {
				pending.CountAfter = m.graphTalkLastID
				pending.CountsOnly = true
				m.graphState, _ = graphview.Update(m.graphState, pending)
			}
		}
		m.graphTalkPending = nil
		return m, nil

	case topicSubscribedMsg:
		m.topicSubscribing = false
		if msg.err != nil || msg.sub == nil {
			m.topicActive = false
			m.topicRetryAt = m.now().Add(10 * time.Second)
			m = m.forgetReadVersions()
			return m, nil
		}
		m.topicActive = true
		m.topicVersions = make(map[string]uint64)
		m.topicDirty = make(map[string]bool)
		// Versions belong to one gateway process. A new subscription starts
		// from new ones, so no read may claim to be at the old version.
		m = m.forgetReadVersions()
		return m, waitTopicsCmd(msg.sub)

	case topicsChangedMsg:
		if !msg.ok {
			m.topicActive = false
			m.topicRetryAt = m.now()
			m = m.forgetReadVersions()
			return m, nil
		}
		for topic, v := range msg.versions {
			if old, ok := m.topicVersions[topic]; !ok || v > old {
				m.topicVersions[topic], m.topicDirty[topic] = v, true
			}
		}
		return m, waitTopicsCmd(msg.sub)

	case tickMsg:
		if m.restartTick() {
			return m, tea.Quit
		}
		m.expirePendingStops()
		cmds := []tea.Cmd{m.nextTick()}
		if !m.topicActive && !m.topicSubscribing && !m.now().Before(m.topicRetryAt) {
			if _, ok := m.src.(interface {
				SubscribeTopics(context.Context) (*operator.TopicSubscription, error)
			}); ok {
				m.topicSubscribing = true
				cmds = append(cmds, m.subscribeTopicsCmd())
			}
		}
		if m.tab == graphTab && !m.mapOn && !m.graphTalkLoading && (m.graphTalkAt.IsZero() || m.now().Sub(m.graphTalkAt) >= time.Minute) {
			m.graphTalkLoading = true
			m.graphTalkPending = nil
			cmds = append(cmds, m.graphTalkCmd())
		}
		mapDue := !m.topicActive || !m.mp.loaded || m.now().Sub(m.topicLastFull) >= 30*time.Second || m.topicView != m.topicViewKey()
		for _, topic := range []string{protocol.TopicGraph, protocol.TopicAgents, protocol.TopicNodes} {
			if v, ok := m.topicVersions[topic]; ok {
				if old, seen := m.mp.topicVersions[topic]; !seen || old != v {
					mapDue = true
				}
			}
		}
		if (m.mapOn || m.tab == graphTab) && !m.mp.loading && mapDue {
			m.mp.loading = true
			cmds = append(cmds, m.mapCmd())
		}
		if _, ok := m.src.(ctoSource); ok && m.cto != nil && !m.cto.polling && m.showCTOBox(m.tab) {
			m.cto.polling = true
			cmds = append(cmds, m.ctoPollCmd())
		}
		if !m.fetching && m.topicRefreshDue() {
			m.fetching = true
			cmds = append(cmds, m.fetchCmd())
		}
		var deliveryCmd tea.Cmd
		m, deliveryCmd = m.deliveryTick()
		cmds = append(cmds, deliveryCmd)
		return m, tea.Batch(cmds...)

	case deliveryStartMsg:
		return m.startDeliveryLoad()
	case deliveryLoadedMsg:
		return m.deliveryLoaded(msg)
	case deliveryDetailMsg:
		return m.deliveryDetailLoaded(msg), nil
	case deliveryReportMsg:
		if msg.err != nil {
			m.fail(msg.err.Error())
			return m, nil
		}
		m.say(msg.text)
		return m.startDeliveryLoad()
	case ctoPollMsg:
		if m.cto == nil || m.cto.generation != msg.generation {
			return m, nil
		}
		oldID, loaded := m.cto.lastGraphID, m.cto.historyLoaded
		last := m.onboardingLastUserMessage(msg.events)
		ctoDone := msg.err == nil && m.onboardingCTOLoaded && last != "" && last != m.onboardingCTOLast
		if msg.err == nil {
			m.onboardingCTOLast, m.onboardingCTOLoaded = last, true
		}
		m.applyCTOPoll(msg)
		if msg.err != nil && operator.IsSlow(msg.err) && !m.cto.retried {
			// The first timeout asks once more at once; the next one waits
			// for the regular poll.
			m.cto.retried, m.cto.polling = true, true
			return m, m.ctoPollCmd()
		}
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
		if ctoDone {
			return m.onboardingComplete("cto")
		}
		return m, nil
	case ctoOlderMsg:
		if m.cto == nil || m.cto.generation != msg.generation {
			return m, nil
		}
		m.cto.olderLoading = false
		if msg.err != nil {
			m.cto.err = sanitize(msg.err.Error())
			return m, nil
		}
		if len(msg.events.Messages) == 0 && len(msg.events.Actions) == 0 {
			m.cto.olderCursor = 0
			return m, nil
		}
		m.cto.olderPages = append([]protocol.CTOEvents{msg.events}, m.cto.olderPages...)
		if msg.events.Older >= m.cto.olderCursor {
			m.cto.olderCursor = 0
		} else {
			m.cto.olderCursor = msg.events.Older
		}
		m.cto.scroll = int(^uint(0) >> 1)
		poll := m.cto.lastPoll
		poll.olderPage = true
		m.applyCTOPoll(poll)
		return m, nil
	case ctoWindowMsg:
		if m.cto != nil && m.cto.generation == msg.generation {
			if msg.err != nil {
				m.cto.err = sanitize(msg.err.Error())
			} else {
				m.cto.windowUntil = msg.window.Until
				m.cto.err = ""
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
	case ctoSettingsMsg:
		if m.cto != nil && m.cto.generation == msg.generation {
			if msg.err != nil {
				m.cto.err = sanitize(msg.err.Error())
			} else {
				m.cto.err = ""
				m.cto.harness = msg.settings.Harness
				m.cto.configuredModel = msg.settings.Model
				m.cto.opencodeModel = msg.settings.OpenCodeModel
				m.cto.claudeModel = msg.settings.ClaudeModel
				if m.cto.opencodeModel == "" && m.cto.claudeModel == "" && msg.settings.Model != "" {
					if msg.settings.Harness == "claude" {
						m.cto.claudeModel = msg.settings.Model
					} else {
						m.cto.opencodeModel = msg.settings.Model
					}
				}
				m.cto.model = msg.settings.Model
				m.cto.title = protocol.CTOTitleOrDefault(msg.settings.Title)
				if m.cto.harness == "" {
					m.cto.harness = "opencode"
				}
				if m.cto.model == "" {
					m.cto.model = "default"
				}
			}
		}
		return m, nil
	case ctoNewMsg:
		if m.cto != nil && m.cto.generation == msg.generation {
			if msg.err != nil {
				m.cto.err = sanitize(msg.err.Error())
			} else {
				m.cto.messages = nil
				m.cto.olderPages = nil
				m.cto.olderCursor = 0
				m.cto.olderLoading = false
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

	case deskDigestMsg:
		m.onDeskDigest(msg)
		return m, nil
	case deskAskMsg:
		return m.onDeskAsk(msg), nil
	case deskLogMsg:
		if msg.err == nil {
			m.deskLog = msg.entries
		}
		return m, nil
	case askDiscussMsg:
		return m.onAskDiscuss(msg)
	case deskUndoneMsg:
		m.say("Undone.")
		for i := range m.deskLog {
			if m.deskLog[i].ID == msg.id {
				m.deskLog[i].Options = nil
				m.deskLog[i].Text = "Undone: " + m.deskLog[i].Text
			}
		}
		return m, m.deskLogCmd()
	case inboxAnswerMsg:
		switch {
		case msg.err == nil:
			m.inboxDetail, m.inboxDetailOption = false, 0
			m.inboxError, m.inboxNote = "", ""
			m.dropInboxItem(msg.id)
		case inboxGone(msg.err):
			// The item left the gateway while the row waited. Keep the list
			// honest: drop the row and say it is gone, not the raw error.
			m.inboxDetail, m.inboxDetailOption = false, 0
			m.inboxError = ""
			m.inboxNote = "already gone"
			m.dropInboxItem(msg.id)
		default:
			m.inboxError = sanitize(msg.err.Error())
			m.inboxNote = ""
		}
		return m, nil
	case inboxSignInMsg:
		if msg.err != nil {
			m.fail("Cannot open sign-in: " + explain(msg.err))
			return m, nil
		}
		if msg.plan.OpenedHerdr {
			m.say(sanitize(msg.plan.Note))
			return m, nil
		}
		if len(msg.plan.Argv) == 0 {
			m.fail(sanitize(firstNonEmpty(msg.plan.Note, "The sign-in pane could not be opened.")))
			return m, nil
		}
		return m.onEnter(enterMsg{name: msg.name, plan: msg.plan})
	case onboardingMsg:
		if msg.revision != m.onboardingRevision {
			return m, nil
		}
		if msg.write {
			m.onboardingPending = false
		}
		if msg.err != nil {
			if msg.write {
				m.fail("Could not save tips: " + explain(msg.err))
			}
		} else {
			m.onboarding, m.onboardingLoaded = msg.value, true
		}
		if msg.write && len(m.onboardingQueue) > 0 {
			request := m.onboardingQueue[0]
			m.onboardingQueue = m.onboardingQueue[1:]
			return m.onboardingWrite(request.method, request.params)
		}
		return m, nil

	case fetchedMsg:
		if msg.proposalsOK {
			m.proposals = msg.proposals
		}
		if msg.onboardingAsked && msg.onboardingErr == nil && msg.onboardingRevision == m.onboardingRevision && !m.onboardingPending {
			m.onboarding, m.onboardingLoaded = msg.onboarding, true
		}
		before := m.onboardingStep().id
		if msg.inboxOK {
			m.asks = msg.asks
			m.asksVersion++
		}
		if msg.chiefOK {
			m.chief, m.chiefLoaded = msg.chief, true
		}
		if msg.handledOK {
			m.handled = msg.handled
		}
		if msg.inboxOK {
			m.inboxIndex = min(max(m.inboxIndex, 0), max(m.deskRowCount()-1, 0))
		}
		if msg.deskLogOK {
			m.deskLog = msg.deskLog
		}
		m.fetching = false
		if msg.err == nil && msg.snap.Connected {
			m.topicFetchedAt = msg.at
			if msg.topicFull {
				m.topicLastFull = msg.at
				m.topicView = msg.topicView
			}
			for topic, v := range msg.topicVersions {
				if m.topicVersions[topic] == v {
					delete(m.topicDirty, topic)
				}
			}
			failed := make(map[string]bool, len(msg.snap.FailedCalls))
			for _, method := range msg.snap.FailedCalls {
				failed[operator.SnapshotTopic(method)] = true
			}
			unchanged := make(map[string]bool, len(msg.snap.Unchanged))
			for _, method := range msg.snap.Unchanged {
				unchanged[operator.SnapshotTopic(method)] = true
			}
			read := msg.topicVersions
			if msg.topicFull {
				read = msg.fetchedVersions
			}
			if m.dataVersions == nil {
				m.dataVersions = make(map[string]uint64)
			}
			if m.dataVersionsAt == nil {
				m.dataVersionsAt = make(map[string]time.Time)
			}
			for topic, v := range read {
				if failed[topic] {
					continue
				}
				m.dataVersions[topic] = v
				// A NotModified part is still current, but its rows were not
				// read again, so its read time must not move.
				if !unchanged[topic] {
					m.dataVersionsAt[topic] = msg.at
				}
			}
		}
		m.applyFetch(msg)
		if msg.pickerAsked {
			m.pickerWanted = false
			if msg.pickerErr != nil {
				m.pickerErr = sanitize(msg.pickerErr.Error())
			} else {
				wasLoaded := m.pickerLoaded
				m.picker, m.pickerErr = msg.picker, ""
				if !wasLoaded {
					m.defaultPickerSelection()
				}
				m.pickerLoaded = true
				m.clampPickerSelection()
			}
		}
		if msg.weeklyAsked {
			m.weeklyWanted = false
			if msg.weeklyErr != nil {
				m.weeklyErr = sanitize(msg.weeklyErr.Error())
			} else {
				m.weeklyScores, m.weeklyErr = msg.weekly, ""
				m.weeklyLoaded = true
			}
		}
		if msg.err == nil && msg.snap.Connected {
			m.inboxSeen = inboxMarkerOf(m.snap)
		}
		if msg.listsAsked {
			m.inboxAt = msg.at
		}
		if msg.proposalsAsked {
			m.proposalsAt = msg.at
		}
		if msg.convAsked {
			m.applyConversationPage(conversationPageMsg{page: msg.convPage, err: msg.convErr, first: true})
		}
		if msg.onboardingToolsKnown {
			m.onboardingToolsCount, m.onboardingToolsKnown = msg.onboardingToolsCount, true
		}
		if msg.err == nil && msg.snap.Connected {
			return m.onboardingAfterFetch(before, msg.events)
		}
		return m, nil
	case conversationPageMsg:
		m.applyConversationPage(msg)
		return m, nil
	case conversationMessagesMsg:
		m.applyConversationMessages(msg)
		return m, nil
	case notificationSettingMsg:
		if msg.err != nil {
			m.fail("Could not change desktop notifications: " + explain(msg.err))
		} else {
			m.snap.NotificationsDisabled = msg.disabled
		}
		return m, nil
	case hiddenItemsMsg:
		if msg.err != nil {
			m.fail("Could not save hidden items: " + sanitize(msg.err.Error()))
			return m, nil
		}
		m.setHiddenItems(msg.items)
		m.ensureSelection()
		m.fixScroll()
		if m.mapOn {
			m.mapEnsureSelection()
		}
		m.say("Hidden items saved for every machine.")
		return m, nil

	case mapMsg:
		m.applyMap(msg)
		return m, nil

	case toolsMsg:
		m.applyTools(msg)
		return m, nil

	case toolsModalMsg:
		next, cmd := m.onToolsModal(msg)
		if msg.onboarding {
			var save tea.Cmd
			next, save = next.onboardingComplete("tools")
			return next, tea.Batch(cmd, save)
		}
		return next, cmd

	case enterMsg:
		return m.onEnter(msg)

	case enterDoneMsg:
		next, cmd := m.onEnterDone(msg)
		if msg.err == nil && msg.name != "CTO" {
			var save tea.Cmd
			next, save = next.onboardingComplete("see")
			return next, tea.Batch(cmd, save)
		}
		return next, cmd

	case resumeMsg:
		return m.onResume(msg)
	case sessionStartedMsg:
		return m.onSessionStarted(msg)
	case charterSavedMsg:
		if msg.err != nil {
			m.charterForm.err = msg.err.Error()
			return m, nil
		}
		m.mode = modeNormal
		m.say("Charter field saved.")
		return m, m.mapCmd()
	case teamWriteMsg:
		return m.onTeamWrite(msg)
	case actionMsg:
		next, cmd := m.onAction(msg)
		if msg.err == nil && msg.onboarding != "" {
			var save tea.Cmd
			next, save = next.onboardingComplete(msg.onboarding)
			return next, tea.Batch(cmd, save)
		}
		return next, cmd
	case proposalDetailMsg:
		if d := m.proposalDetail; d != nil && d.proposal.ID == msg.id && d.proposal.NodeID == msg.node {
			d.loading = false
			d.diff = msg.diff
			if msg.err != nil {
				d.err = explain(msg.err)
			}
		}
		return m, nil
	case proposalAnswerMsg:
		var detailCmd tea.Cmd
		if msg.items != nil {
			m.proposals = msg.items
			if m.proposalDetail == nil && msg.action != "A" {
				for _, p := range msg.items {
					if p.ID == msg.id && p.NodeID == msg.node {
						m.proposalDetail = &proposalDetail{proposal: p, loading: true}
						detailCmd = m.proposalDetailCmd(p)
						break
					}
				}
			}
			if d := m.proposalDetail; d != nil {
				for _, p := range msg.items {
					if p.ID == d.proposal.ID && p.NodeID == d.proposal.NodeID {
						d.proposal = p
						break
					}
				}
			}
		}
		if msg.action == "A" && msg.items != nil {
			m.proposalHistory = true
			m.proposalHistoryIndex = 0
		}
		if msg.err != nil {
			m.fail("Proposal failed: " + explain(msg.err))
		} else if msg.action != "x" && m.sessionNoticeSerial != msg.sessionBefore {
			// Keep the session start result that arrived during approval.
		} else if msg.action == "A" {
			m.say("Proposals approved and applied.")
		} else {
			for _, p := range msg.items {
				if p.ID == msg.id && p.NodeID == msg.node {
					m.say(p.Note)
					break
				}
			}
		}
		m.ensureSelection()
		m.fixScroll()
		return m, detailCmd

	case policyPreviewMsg:
		return m.onPolicyPreview(msg)

	case policySavedMsg:
		return m.onPolicySaved(msg)

	case standingMsg:
		return m.onStanding(msg)
	case nudgeStatsMsg:
		m.nudgeStatsAt = m.now()
		if msg.err != nil {
			m.nudgeStatsErr = msg.err
			return m, nil
		}
		m.nudgeStatsErr = nil
		if msg.stats.Since != "" {
			m.nudgeStats = msg.stats
		}
		return m, nil
	case settingValuesMsg:
		if msg.err != nil {
			m.fail("Settings: " + explain(msg.err))
			return m, nil
		}
		if msg.rows != nil {
			m.settingValues = msg.rows
		}
		if msg.notice != "" {
			m.say(msg.notice)
			return m, m.loadSettingValues()
		}
		return m, nil
	case appsResult:
		var selected settingsItem
		if m.tab == settingsTab {
			items := m.settingsItems()
			selected = items[min(max(m.settingsRow, 0), len(items)-1)]
		}
		next, cmd := m.onAppsResult(msg)
		if next.tab == settingsTab && msg.op == "apps.list" {
			cmd = tea.Batch(cmd, next.loadSettingValues(), next.loadStandingOrders(), next.loadNudgeStats())
		}
		if next.tab == settingsTab {
			items := next.settingsItems()
			next.settingsRow = min(next.settingsRow, len(items)-1)
			for i, item := range items {
				if item.kind == selected.kind && item.index == selected.index {
					next.settingsRow = i
					break
				}
			}
		}
		if next.tab == settingsTab && next.mode == modeApps && next.apps.stage == "" {
			next.mode = modeNormal
		}
		return next, cmd

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

	case tea.KeyPressMsg:
		// Any key dismisses a result at once, on every screen.
		m.notice = notice{}
		m.frozenOrder = make(map[string]int)
		for i, a := range m.visible() {
			m.frozenOrder[a.SessionID] = i
		}
		m.orderFrozenUntil = m.now().Add(3 * time.Second)
		digest := m.deskDigestStart()
		next, cmd := m.key(msg)
		cmd = tea.Batch(cmd, digest)
		if changed, ok := next.(Model); ok && changed.tab == messagesTab && m.tab != messagesTab && changed.onboardingHasPeerThread() {
			var save tea.Cmd
			changed, save = changed.onboardingComplete("talk")
			next, cmd = changed, tea.Batch(cmd, save)
		}
		if m.mapOn {
			if changed, ok := next.(Model); ok && (!changed.mapOn || changed.quitting) {
				mark := m.markMapViewed(m.mp.selected)
				if changed.quitting && mark != nil {
					return next, tea.Sequence(mark, cmd)
				}
				return next, tea.Batch(mark, cmd)
			}
		}
		return next, cmd
	}
	return m, nil
}

// bodyError explains why a body is not there, in plain words.
func bodyError(err error) string {
	var re client.RemoteError
	if errors.As(err, &re) && strings.HasPrefix(err.Error(), "body held on ") {
		return sanitize(err.Error())
	}
	if errors.As(err, &re) && strings.Contains(err.Error(), "unknown message") {
		return "The body is not available here. Old messages are deleted."
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
		if m.problem == ProblemSlow {
			m.problemT = slowText
		}
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
	m.prunePendingStops()
	if m.mp.loaded {
		m.applyTeamView(false)
	}
	m.applyTimeZone()
	m.updateSessionOrder(r.events)
	if r.hiddenErr == nil {
		m.setHiddenItems(r.hidden)
	}
	m.applyUsage(r)
	if r.eventsErr != nil {
		if errors.Is(r.eventsErr, ErrFeedUnavailable) {
			m.feed.unavailable = "no message feed: the daemon or the gateway is older than the event feed"
		}
		// Any other error shows up as a problem on the next refresh.
	} else {
		wasLast := m.feedCur >= m.feedLastVisible()
		lastID := m.feed.lastID
		m.feed.apply(r.events)
		projects := make(map[string]string)
		for _, a := range r.snap.Agents {
			p := a.Repo.Project
			if p == "" {
				p = a.Profile.Project
			}
			if a.Harness == "cto" {
				p = protocol.CTONodeName
			}
			projects[a.SessionID] = p
		}
		events := graphview.FeedEvents{Events: r.events, LastID: lastID, CountAfter: m.graphTalkLastID, Now: r.at, Projects: projects}
		if m.graphTalkLoading {
			m.graphTalkPending = append(m.graphTalkPending, events)
		}
		m.graphState, _ = graphview.Update(m.graphState, events)
		if m.focus != focusFeed || wasLast {
			m.feedCur = m.feedLastVisible()
		} else {
			m.feedClamp()
		}
	}
	m.rebuildConversations()
	m.ensureSelection()
	m.fixScroll()
}

// stopSettled says a pending stop is over: the session left, ended, or went
// dormant. A session that wakes on message (the CTO) stays online after a
// stop and only turns dormant, so online alone does not mean the stop is
// still running.
func stopSettled(a protocol.AgentView) bool {
	return !a.Online ||
		strings.EqualFold(a.Status, "ended") ||
		strings.EqualFold(a.Status, "offline") ||
		strings.EqualFold(a.Status, protocol.StatusDormant)
}

func (m *Model) prunePendingStops() {
	for id := range m.pendingStops {
		found, done := false, false
		for _, a := range m.snap.Agents {
			if a.SessionID != id {
				continue
			}
			found = true
			done = stopSettled(a)
			break
		}
		if !found || done {
			delete(m.pendingStops, id)
		}
	}
}

func (m *Model) expirePendingStops() {
	for id, pending := range m.pendingStops {
		if m.now().Sub(pending.at) < 30*time.Second {
			continue
		}
		name, settled := id, false
		for _, a := range m.snap.Agents {
			if a.SessionID == id {
				name, settled = rowName(a), stopSettled(a)
				break
			}
		}
		delete(m.pendingStops, id)
		if !settled {
			m.say("still running: " + name)
		}
	}
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

func (m Model) key(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k = normalizeKey(k)
	if k.String() == "ctrl+c" {
		m.quitting = true
		return m, tea.Quit
	}
	if m.mp.removal != nil {
		return m.graphRemovalKey(k)
	}
	if m.finder {
		return m.finderKey(k)
	}
	if m.archive != nil {
		return m.sessionsArchiveKey(k)
	}
	if m.resumeConfirm != nil {
		a := *m.resumeConfirm
		yes, _ := confirmAnswer(k)
		m.resumeConfirm = nil
		if yes {
			return m.resumeOwnerSession(a)
		}
		return m, nil
	}
	if m.moveConfirm != nil {
		a := *m.moveConfirm
		yes, _ := confirmAnswer(k)
		m.moveConfirm = nil
		if yes {
			return m.resumeCmd(a, true)
		}
		return m, nil
	}
	if m.sessionPending != nil {
		return m.sessionPendingKey(k)
	}
	if m.proposalConfirm {
		return m.proposalConfirmKey(k)
	}
	if m.proposalHistory && !m.help {
		return m.proposalHistoryKey(k)
	}
	if m.proposalDetail != nil && !m.help {
		return m.proposalDetailKey(k)
	}
	switch m.mode {
	case modeCompose:
		return m.composeKey(k)
	case modeSessionForm:
		return m.sessionFormKey(k)
	case modeConfirm:
		return m.confirmKey(k)
	case modeDefectNote:
		return m.defectKey(k)
	case modeDefectSeverity:
		return m.defectSeverityKey(k)
	case modeQuotaConfirm:
		return m.quotaConfirmKey(k)
	case modeQuotaFloor:
		return m.quotaFloorKey(k)
	case modeTrust:
		return m.trustKey(k)
	case modePolicy:
		return m.policyKey(k)
	case modeRename:
		return m.renameKey(k)
	case modeProjectRename:
		return m.projectRenameKey(k)
	case modeTimeZone:
		return m.timeZoneKey(k)
	case modeStandingAdd:
		return m.standingAddKey(k)
	case modeSettingValue:
		return m.settingValueKey(k)
	case modeSettingRepo:
		return m.settingRepoKey(k)
	case modeSettingUndo:
		return m.settingUndoKey(k)
	case modeIntegrations:
		return m.integrationsKey(k)
	case modeLeadPicker:
		return m.leadPickerKey(k)
	case modeTeams:
		return m.teamMenuKey(k)
	case modeCharterForm:
		return m.charterFormKey(k)
	case modeTeamForm:
		return m.teamFormKey(k)
	case modeApps:
		next, cmd := m.appsKey(k)
		updated := next.(Model)
		if updated.tab == settingsTab && updated.apps.stage == "" {
			updated.mode = modeNormal
		}
		return updated, cmd
	}
	if m.help {
		switch keyAction(contextHelp, k.String()) {
		case actQuit:
			m.quitting = true
			return m, tea.Quit
		case actClose:
			m.help = false
			m.helpAll = false
			m.helpScroll = 0
		case actAllScreens:
			m.helpAll = !m.helpAll
			m.helpScroll = 0
		case actDown:
			m.helpScroll++
		case actUp:
			m.helpScroll--
		case actPageDown:
			m.helpScroll += max(m.height-4, 1)
		case actPageUp:
			m.helpScroll -= max(m.height-4, 1)
		case actFirst:
			m.helpScroll = 0
		case actLast:
			m.helpScroll = len(m.helpContent(max(m.width, 40)))
		}
		room := max(max(m.height, 10)-4, 1)
		m.helpScroll = min(max(m.helpScroll, 0), max(len(m.helpContent(max(m.width, 40)))-room, 0))
		return m, nil
	}
	if m.tab == quotasTab && !m.help && (k.String() == "tab" || k.String() == "shift+tab") {
		if k.String() == "shift+tab" {
			m, _ = m.nextQuotasSubtab()
		}
		return m.nextQuotasSubtab()
	}
	if m.qualityDetail && m.tab == quotasTab && keyAction(contextQualityDetails, k.String()) != "" {
		switch keyAction(contextQualityDetails, k.String()) {
		case "help":
			m.openHelp()
		case actClose:
			// Escape leaves the selected sub tab in place.
		case actDown:
			m.movePickerModel(1)
		case actUp:
			m.movePickerModel(-1)
		case actPageDown:
			m.movePickerModel(5)
		case actPageUp:
			m.movePickerModel(-5)
		case "next bucket":
			m.movePickerBucket(1)
		case "previous bucket":
			m.movePickerBucket(-1)
		case "older":
			m.pickerShowOlder = !m.pickerShowOlder
			m.clampPickerSelection()
		case "weekly":
			m.showWeekly = !m.showWeekly
			m.weeklyWanted = true
			if !m.fetching {
				m.fetching = true
				return m, m.fetchCmd()
			}
		case "refresh":
			m.pickerWanted = true
			m.weeklyWanted = true
			if !m.fetching {
				m.fetching = true
				return m, m.fetchCmd()
			}
		}
		return m, nil
	}
	if m.focus == focusCTO && m.cto != nil {
		return m.ctoKey(k)
	}
	if m.tab == graphTab && m.mp.finding {
		return m.graphKey(k)
	}
	if m.tab == toolsTab && !m.mapOn && m.tl.capturing() {
		return m.toolsKey(k)
	}
	// c selects the CTO session's row and opens it in one key; C focuses the box for its own keys
	// (new conversation, undo, settings, scrolling).
	if keyAction(contextGlobal, k.String()) == "open CTO" && !m.filtering {
		var poll tea.Cmd
		if m.cto == nil {
			next, cmd := m.startCTO()
			m, poll = next.(Model), cmd
			if m.cto == nil {
				return m, nil
			}
		}
		id := m.ctoSessionID()
		m.selectSession(id)
		next, enter := m.openCTO(id)
		return next, tea.Batch(poll, enter)
	}
	if keyAction(contextGlobal, k.String()) == "focus CTO" && !m.filtering {
		m.tab, m.mapOn, m.focus = 0, false, focusCTO
		m.open = nil
		if m.cto == nil {
			return m.startCTO()
		}
		return m, nil
	}
	if !m.filtering && m.open == nil {
		switch keyAction(contextGlobal, k.String()) {
		case "graph tab":
			return m.startGraph()
		case "tools tab":
			return m.startTools()
		case "sessions tab", "quotas tab", "messages tab", "settings tab":
			m.mapOn = false
		}
	}
	if m.mapOn {
		return m.mapKey(k)
	}
	if m.tab == sessionsTab && m.focus == focusSessions && m.sessionCell != 0 && k.Code == tea.KeyEsc {
		m.sessionCell = 0
		return m, nil
	}
	if m.filtering {
		switch k.Code {
		case tea.KeyEsc:
			m.filter = ""
			m.input.SetValue("")
			m.filtering = false
			m.input.Blur()
			m.rebuildConversations()
		case tea.KeyEnter:
			m.filtering = false
			m.input.Blur()
		default:
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(k)
			m.filter = m.input.Value()
			m.rebuildConversations()
			m.ensureSelection()
			m.fixScroll()
			return m, cmd
		}
		m.ensureSelection()
		m.fixScroll()
		return m, nil
	}
	if m.open != nil {
		switch keyAction(contextBody, k.String()) {
		case "help":
			m.openHelp()
		case actQuit:
			m.quitting = true
			return m, tea.Quit
		case actClose:
			m.open = nil
		case actUp:
			m.open.scroll = max(m.open.scroll-1, 0)
		case actDown:
			m.open.scroll++
		case actPageUp:
			m.open.scroll = max(m.open.scroll-10, 0)
		case actPageDown:
			m.open.scroll += 10
		}
		return m, nil
	}

	if keyAction(contextGlobal, k.String()) == "find session" && m.mode == modeNormal && !m.help && !m.filtering && m.open == nil {
		return m.openFinder(), nil
	}

	for i, tab := range tabs.All {
		if keyAction(contextGlobal, k.String()) == actionID(tab.Action) {
			m.tab = i
			m.uiFilter = ""
			m.focus = focusSessions
			if i == quotasTab {
				// The Quotas panel and the Quality subtab both read the picker table.
				m.pickerWanted = true
			}
			if i == sessionsTab {
				m.sessionCell = 0
			}
			if i == inboxTab {
				m.inboxDetail, m.inboxDetailOption, m.inboxEditing = false, 0, false
				m.delivery.project, m.delivery.detail = "", false
				m.inboxIndex = min(max(m.inboxIndex, 0), max(m.deskRowCount()-1, 0))
				return m.startDeliveryLoad()
			}
			if i == settingsTab {
				m.settingsRow = 0
				m.apps = appsForm{loading: true}
				return m, m.appsCall("apps.list", struct{}{})
			}
			if i == messagesTab {
				m.focus = focusFeed
				m.feedCur = m.feedLastVisible()
				m.convSel = 0
				if rows := m.messageConversations(); len(rows) > 0 && !m.convLoaded {
					m.feedCur = rows[0].items[0]
				}
				return m, m.conversationCmd("", true)
			}
			return m, nil
		}
	}
	if m.tab == graphTab {
		return m.graphKey(k)
	}
	if m.tab == toolsTab {
		return m.toolsKey(k)
	}
	if m.tab == settingsTab {
		return m.settingsKey(k)
	}
	if m.tab == quotasTab {
		if keyAction(contextQuotas, k.String()) == "workers log" {
			return m.nextQuotasSubtab()
		}
		if m.workersLog {
			return m.workersLogKey(k)
		}
		key := keyAction(contextQuotas, k.String())
		switch key {
		case "protect":
			rows := m.selectableQuotaRows()
			if len(rows) == 0 {
				m.fail("No subscription selected.")
				return m, nil
			}
			row := rows[min(m.quotaSelected, len(rows)-1)]
			provider := m.quotaModelProvider(row.provider.Provider)
			m.notice = notice{}
			m.quotaConfirm, m.quotaUnprotect = provider, m.quotaProviderProtected(row.provider.Provider)
			m.quotaConfirmDisplay = m.quotaRowDisplayName(row)
			m.mode = modeQuotaConfirm
		case "floor":
			rows := m.selectableQuotaRows()
			if len(rows) == 0 {
				m.fail("No subscription selected.")
				return m, nil
			}
			row := rows[min(m.quotaSelected, len(rows)-1)]
			provider := m.quotaModelProvider(row.provider.Provider)
			current := m.snap.WorkerFloors.Floor(provider)
			m.notice = notice{}
			m.quotaConfirm = provider
			m.quotaConfirmDisplay = m.quotaRowDisplayName(row)
			m.mode = modeQuotaFloor
			target := sanitize(m.quotaConfirm)
			if m.quotaConfirmDisplay != "" && !strings.EqualFold(m.quotaConfirmDisplay, m.quotaConfirm) {
				target = sanitize(m.quotaConfirmDisplay) + " (" + target + ")"
			}
			m.rename = newTextInput()
			m.rename.Prompt = "floor % for " + target + ": "
			m.rename.CharLimit = 2
			m.rename.SetVirtualCursor(false)
			if current > 0 {
				m.rename.SetValue(fmt.Sprintf("%d", current))
			} else {
				m.rename.SetValue("")
			}
			m.rename.CursorEnd()
			return m, m.rename.Focus()
		case "show unset":
			m.quotaSelected = 0
			m.quotaShowUnset = !m.quotaShowUnset
			m.quotaScroll = 0
		case actQuit:
			m.quitting = true
			return m, tea.Quit
		case actDown:
			m.quotaSelected = min(m.quotaSelected+1, max(0, len(m.selectableQuotaRows())-1))
			m.quotaScroll++
		case actUp:
			m.quotaSelected = max(0, m.quotaSelected-1)
			m.quotaScroll = max(0, m.quotaScroll-1)
		case "help":
			m.openHelp()
		case "refresh":
			if !m.fetching {
				m.fetching = true
				return m, m.fetchCmd()
			}
		}
		return m, nil
	}
	if m.tab == inboxTab {
		return m.inboxTabKey(k)
	}

	context := contextSessions
	if m.tab == messagesTab {
		context = contextMessages
	}
	key := keyAction(context, k.String())
	switch key {
	case actQuit:
		m.quitting = true
		return m, tea.Quit
	case "compose":
		return m.startCompose()
	case "new session":
		if m.tab == sessionsTab {
			return m.startSessionForm()
		}
	case "defect":
		if m.tab == messagesTab {
			return m.startDefect()
		}
	case "cycle message filter":
		if m.tab == messagesTab {
			return m.cycleMessageFilter()
		}
	case "next message subtab":
		if m.tab == messagesTab {
			return m.setMessageSubtab((m.messageSubtab + 1) % 3)
		}
	case "previous message subtab":
		if m.tab == messagesTab {
			next := m.messageSubtab - 1
			if next < 0 {
				next = 2
			}
			return m.setMessageSubtab(next)
		}
	case "thread project":
		if m.tab == messagesTab {
			return m.cycleThreadProject()
		}
	case "thread team":
		if m.tab == messagesTab {
			return m.cycleThreadTeam()
		}
	case "open thread link":
		if m.tab == messagesTab {
			return m.threadLinkJump()
		}
	case "expand worker":
		if m.tab == messagesTab {
			rows := m.messageConversations()
			if len(rows) > 0 {
				row := rows[m.selectedConversationIndex(rows)]
				if row.worker {
					if m.threadExpanded == nil {
						m.threadExpanded = map[string]bool{}
					}
					m.threadExpanded[row.id] = !m.threadExpanded[row.id]
					return m, nil
				}
			}
			return m, nil
		}
	case "stop worker":
		if a, ok := m.selectedAgent(); ok && a.Harness == "bot" && (a.Online || m.tab != sessionsTab) {
			m.mode = modeApps
			m.apps = appsForm{stage: "confirm", apps: []connectedApp{{ID: a.NodeID, Name: a.Profile.Name, Kind: "bot"}}}
			return m, m.appsCall("apps.public.status", struct{}{})
		}
		return m.startConfirm()
	case "remove offline":
		return m.startRemoveOffline()
	case "hide":
		if m.tab == sessionsTab && m.focus == focusSessions {
			return m.toggleHiddenSession()
		}
	case "pin":
		return m.toggleSessionPin()
	case "help":
		m.openHelp()
	case "details":
		m.expand = !m.expand
		m.fixScroll()
	case "filter":
		m.filtering = true
		m.input.SetValue(m.filter)
		m.input.CursorEnd()
		return m, m.input.Focus()
	case "back":
		if m.tab == sessionsTab && m.sessionCell != 0 {
			m.sessionCell = 0
			return m, nil
		}
		if m.filter != "" {
			m.filter = ""
			m.input.SetValue("")
			m.rebuildConversations()
			m.ensureSelection()
			m.fixScroll()
		}
		if m.tab == messagesTab && (m.threadProject != "" || m.threadTeam != "") {
			m.threadProject, m.threadTeam = "", ""
			if m.convLoaded {
				m.rebuildConversations()
			}
		}
	case "archive offline":
		if m.tab == sessionsTab && m.showOffline {
			return m.startSessionsArchive()
		}
	case "offline":
		m.showOffline = !m.showOffline
		m.ensureSelection()
		m.fixScroll()
	case "active only":
		m.activeOnly = !m.activeOnly
		m.ensureSelection()
		m.fixScroll()
	case "toggle tree":
		m.toggleSessionTrees(false)
	case "toggle all trees":
		m.toggleSessionTrees(true)
	case "cycle view":
		m.cycleSessionView()
		m.ensureSelection()
		m.fixScroll()
	case "show hidden":
		m.showHiddenSessions = !m.showHiddenSessions
		m.ensureSelection()
		m.fixScroll()
	case "cell":
		if m.tab == sessionsTab {
			if a, ok := m.selectedAgent(); ok && !protocol.IsOwner(a.Harness) && a.Harness != "bot" {
				if k.String() == "right" {
					last := 2
					if canChangeSessionAgent(a) {
						last = 3
					}
					m.sessionCell = min(m.sessionCell+1, last)
				} else {
					m.sessionCell = max(m.sessionCell-1, 0)
				}
				if m.sessionCell == 1 && firstNonEmpty(a.Repo.Project, a.Profile.Project) == "" {
					if k.String() == "right" {
						m.sessionCell = 2
					} else {
						m.sessionCell = 0
					}
				}
			}
		}
	case "change cell":
		if m.tab == sessionsTab && m.sessionCell == 3 {
			return m.cycleSessionAgent()
		}
		if m.tab == sessionsTab && m.sessionCell == 1 {
			return m.startTrust()
		}
	case "message body", "open session":
		if m.tab == sessionsTab {
			if m.sessionCell == 3 {
				return m.cycleSessionAgent()
			}
			if m.sessionCell == 2 {
				return m.startPolicy()
			}
			if m.sessionCell == 1 {
				return m, nil
			}
		}
		if m.focus == focusFeed {
			if it := m.selectedConversationItem(); it != nil && it.isMessage() {
				m.open = &openMsg{id: it.MsgID, loading: true}
				return m, m.bodyCmd(it.MsgID)
			}
			rows := m.messageConversations()
			if len(rows) > 0 {
				row := rows[m.selectedConversationIndex(rows)]
				if row.lastMsgID != "" {
					m.open = &openMsg{id: row.lastMsgID, loading: true}
					return m, m.bodyCmd(row.lastMsgID)
				}
			}
		}
		if m.tab == sessionsTab {
			return m.startEnter()
		}
	case "refresh":
		if !m.fetching {
			m.fetching = true
			return m, m.fetchCmd()
		}
	case "scroll thread up":
		m.scrollThread(-1)
		return m, nil
	case "scroll thread down":
		m.scrollThread(1)
		return m, nil
	case "previous message", "next message":
		if m.tab == messagesTab && m.focus == focusFeed {
			rows := m.messageConversations()
			if len(rows) > 0 {
				r := rows[m.selectedConversationIndex(rows)]
				if len(r.items) == 0 {
					// A stored conversation has no marked message to move
					// along, so the keys scroll it.
					if key == "next message" {
						m.scrollThread(wheelLines)
					} else {
						m.scrollThread(-wheelLines)
					}
					return m, nil
				}
				m.threadScrolled = false
				pos := 0
				for i, v := range r.items {
					if v == m.feedCur {
						pos = i
						break
					}
				}
				if key == "next message" {
					pos++
				} else {
					pos--
				}
				m.feedCur = r.items[min(max(pos, 0), len(r.items)-1)]
			}
			return m, nil
		}
	case actUp:
		m.move(-1)
	case actDown:
		m.move(1)
	case actPageUp:
		m.move(-m.layout().sessions)
	case actPageDown:
		m.move(m.layout().sessions)
	case actFirst:
		m.move(-1 << 20)
	case actLast:
		if m.focus == focusFeed {
			rows := m.messageConversations()
			if len(rows) == 0 {
				return m, nil
			}
			row := rows[m.selectedConversationIndex(rows)]
			m.threadScrolled = false
			if len(row.items) == 0 {
				return m, m.wantConversationDetails()
			}
			m.feedCur = row.items[len(row.items)-1]
			return m, nil
		}
		m.move(1 << 20)
	}
	return m, nil
}

// move changes the selection by delta, in the list that has the focus.
func (m *Model) move(delta int) {
	if m.focus == focusFeed {
		if m.tab == messagesTab {
			rows := m.messageConversations()
			if len(rows) > 0 {
				pos := m.selectedConversationIndex(rows)
				pos = min(max(pos+delta, 0), len(rows)-1)
				if pos != m.convSel {
					// A thread opens at its newest message, not where the
					// last one was left.
					m.threadScrolled = false
				}
				m.convSel = pos
				if !m.convLoaded {
					m.feedCur = rows[pos].items[0]
				} else if len(rows[pos].items) > 0 {
					m.feedCur = rows[pos].items[len(rows[pos].items)-1]
				}
			}
			return
		}
		m.moveFeed(delta)
		return
	}
	vis := m.visible()
	if len(vis) == 0 {
		return
	}
	i := m.selIdx
	if i < 0 || i >= len(vis) || vis[i].SessionID != m.selected {
		m.ensureSelection()
		vis = m.visible()
		i = m.selIdx
	}
	i = min(max(i+delta, 0), len(vis)-1)
	m.selected, m.selIdx = vis[i].SessionID, i
	m.sessionCell = 0
	m.fixScroll()
}

func (m Model) feedItemAt(i int) *feedItem {
	if i < 0 || i >= len(m.feed.items) {
		return nil
	}
	return m.feed.items[i]
}

// ---- the list of sessions ----

// group is one urgency band of complete session trees.
type group struct {
	host   string
	agents []protocol.AgentView
	// hidden counts idle sessions folded out of the active view.
	hidden int
	total  int
	first  bool
	// marks says how a worker row is drawn, by session ID. A session with no
	// mark is a plain row.
	marks map[string]treeMark
}

// idleAfter is how long a session may sit idle before the screen calls it
// quiet: dim on every screen. Idle folding does not depend on its age.
const idleAfter = time.Hour

// idleLong tells whether a session is idle and has had no activity for more
// than idleAfter. LastSeen is the last time its machine reported it.
func (m Model) idleLong(a protocol.AgentView) bool {
	if !a.Online || a.Status != "idle" || a.LastSeen == 0 {
		return false
	}
	return m.now().Sub(time.Unix(a.LastSeen, 0)) > idleAfter
}

// treeMark is how a nested row hangs under the session it belongs to: a
// worker under the session that started it, or a project's merger under that
// project's main agent.
type treeMark struct {
	prefix  string // the tree glyphs before the name: "├─ ", "│  └─ ", or "↳ " for a lone worker
	root    string
	summary string
}

// matches tells whether an agent passes the filter: every word must appear in
// one of the fields, ignoring case.
func matches(a protocol.AgentView, filter string) bool {
	words := strings.Fields(strings.ToLower(filter))
	if len(words) == 0 {
		return true
	}
	hay := strings.ToLower(strings.Join([]string{a.Profile.Name, a.Harness, a.Hostname, a.Repo.Project, a.Profile.Project, a.ProjectDisplayName, a.AgentID, a.AgentName,
		a.Repo.Branch, a.Model, a.Mode, modeShort(a.Mode), a.Status, a.Profile.Function, a.Profile.Task, a.Cwd, a.SessionID,
		a.Repo.Remote}, "\n"))
	for _, w := range words {
		if !strings.Contains(hay, w) {
			return false
		}
	}
	return true
}

// groups builds complete trees before assigning their most urgent member's band.
func (m Model) groups() []group { return m.sessionGroups(true) }

func (m Model) sessionGroups(collapse bool) []group {
	shown := map[string]protocol.AgentView{}
	eligible := map[string]bool{}
	var bots []protocol.AgentView
	for _, a := range m.snap.Agents {
		if protocol.IsOwner(a.Harness) || m.hiddenSessions[a.SessionID] && !m.showHiddenSessions || isJunkSession(a) && !m.showJunk() {
			continue
		}
		if a.Harness == "bot" {
			// A bot has no turns, so it is never Working. It gets its own
			// small group at the bottom, and a filter still selects it.
			if m.filter == "" || matches(a, m.filter) {
				bots = append(bots, a)
			}
			continue
		}
		live := !sessionEnded(a)
		if a.Worker != nil && !live {
			continue
		}
		shown[a.SessionID] = a
		eligible[a.SessionID] = live && a.Status != protocol.StatusDormant || m.showOffline || strings.TrimSpace(m.filter) != "" || a.StalledSince > 0 || m.sessionNeedsOwner(a)
	}
	parents := map[string]string{}
	for id, a := range shown {
		parent := ""
		if isMerger(a) {
			parent = m.mergerParent(a, shown)
		} else if a.Worker != nil {
			parent = a.Worker.Owner
		}
		if _, ok := shown[parent]; ok && parent != id {
			parents[id] = parent
		}
	}
	// Invalid caller cycles stay at the top level.
	invalid := map[string]bool{}
	for id := range parents {
		seen := map[string]bool{id: true}
		for p := parents[id]; p != ""; p = parents[p] {
			if seen[p] {
				invalid[id] = true
				break
			}
			seen[p] = true
		}
	}
	for id := range invalid {
		delete(parents, id)
	}
	// Keep quiet ancestors when an eligible descendant needs their context.
	kept := map[string]bool{}
	for id, ok := range eligible {
		if ok {
			for p := id; p != "" && !kept[p]; p = parents[p] {
				kept[p] = true
			}
		}
	}
	for id := range shown {
		if !kept[id] {
			delete(shown, id)
		}
	}
	children := map[string][]protocol.AgentView{}
	var roots []protocol.AgentView
	for id, a := range shown {
		if p := parents[id]; p != "" {
			children[p] = append(children[p], a)
		} else {
			roots = append(roots, a)
		}
	}
	treeOrder := make(map[string]sessionOrder, len(roots))
	for _, root := range roots {
		treeOrder[root.SessionID] = m.treeOrder(root, children)
	}
	less := func(a, b protocol.AgentView) bool {
		if (a.PinnedAt > 0) != (b.PinnedAt > 0) {
			return a.PinnedAt > 0
		}
		if a.PinnedAt > 0 && a.PinnedAt != b.PinnedAt {
			return a.PinnedAt < b.PinnedAt
		}
		if m.now().Before(m.orderFrozenUntil) {
			ia, oka := m.frozenOrder[a.SessionID]
			ib, okb := m.frozenOrder[b.SessionID]
			if oka != okb {
				return oka
			}
			if oka && ia != ib {
				return ia < ib
			}
		}
		oa, oka := treeOrder[a.SessionID]
		ob, okb := treeOrder[b.SessionID]
		if oka && okb {
			if oa.band != ob.band {
				return oa.band < ob.band
			}
			if !oa.activity.Equal(ob.activity) {
				return oa.activity.After(ob.activity)
			}
			if oa.rank != ob.rank {
				return oa.rank < ob.rank
			}
		}
		return a.SessionID < b.SessionID
	}
	sort.SliceStable(roots, func(i, j int) bool { return less(roots[i], roots[j]) })
	for _, cs := range children {
		sort.Slice(cs, func(i, j int) bool {
			if isMerger(cs[i]) != isMerger(cs[j]) {
				return !isMerger(cs[i])
			}
			if cs[i].Worker != nil && cs[j].Worker != nil && cs[i].Worker.StartedAt != cs[j].Worker.StartedAt {
				return cs[i].Worker.StartedAt < cs[j].Worker.StartedAt
			}
			return cs[i].SessionID < cs[j].SessionID
		})
	}
	bands := []group{{host: "Needs you"}, {host: "Working"}, {host: "Idle"}}
	for i := range bands {
		bands[i].marks = map[string]treeMark{}
	}
	for _, root := range roots {
		var tree []protocol.AgentView
		marks := map[string]treeMark{}
		band, busy := 2, 0
		matched, pinned := false, false
		subagent := false
		var walk func(protocol.AgentView, string)
		walk = func(a protocol.AgentView, prefix string) {
			tree = append(tree, a)
			matched = matched || matches(a, m.filter)
			pinned = pinned || a.PinnedAt > 0
			subagent = subagent || a.Worker != nil || isMerger(a)
			live := !sessionEnded(a)
			if live && m.sessionNeedsOwner(a) {
				band = 0
			} else if live && (m.mergerWorking(a) || a.Status == "busy" || m.activeWithin(a, workingGrace)) {
				band = min(band, 1)
			}
			if live && a.Status == "busy" {
				busy++
			}
			cs := children[a.SessionID]
			for i, c := range cs {
				glyph, next := "├─ ", prefix+"│  "
				if i == len(cs)-1 {
					glyph, next = "└─ ", prefix+"   "
				}
				marks[c.SessionID] = treeMark{prefix: prefix + glyph, root: root.SessionID}
				walk(c, next)
			}
		}
		if root.Worker != nil && !isMerger(root) {
			marks[root.SessionID] = treeMark{prefix: "↳ "}
		}
		walk(root, "")
		if !matched {
			continue
		}
		g := &bands[band]
		g.total += len(tree)
		// An idle tree with a subagent stays whole: dropping it would hide the
		// merger or worker the owner needs to see. Only plain idle agents fold.
		if band == 2 && m.activeOnly && m.filter == "" && !pinned && !subagent {
			g.hidden += len(tree)
			continue
		}
		if collapse && m.collapsedTrees[root.SessionID] && len(tree) > 1 {
			childBusy := busy
			if root.Online && root.Status == "busy" {
				childBusy--
			}
			marks[root.SessionID] = treeMark{summary: fmt.Sprintf("+%d (%d busy)", len(tree)-1, childBusy)}
			tree = tree[:1]
		}
		g.agents = append(g.agents, tree...)
		for id, mk := range marks {
			g.marks[id] = mk
		}
	}
	var out []group
	for _, g := range bands {
		if g.total > 0 {
			g.first = len(out) == 0
			out = append(out, g)
		}
	}
	var botAgents []protocol.AgentView
	for _, a := range bots {
		if !a.Online && !m.showOffline {
			continue
		}
		botAgents = append(botAgents, a)
	}
	sort.SliceStable(botAgents, func(i, j int) bool {
		if botAgents[i].LastSeen != botAgents[j].LastSeen {
			return botAgents[i].LastSeen > botAgents[j].LastSeen
		}
		return botAgents[i].SessionID < botAgents[j].SessionID
	})
	if len(botAgents) > 0 {
		g := group{host: "Bots", agents: botAgents, total: len(botAgents), marks: map[string]treeMark{}}
		g.first = len(out) == 0
		out = append(out, g)
	}
	return out
}

// treeOrder picks the order key of a root tree's most urgent member. A worker
// or merger that is more recent than its caller lifts the whole tree, so the
// owner still sees a working child near the top of its band.
func (m Model) treeOrder(root protocol.AgentView, children map[string][]protocol.AgentView) sessionOrder {
	best, ok := m.sessionOrder[root.SessionID]
	if !ok {
		best = sessionOrder{band: 4}
	}
	var walk func(protocol.AgentView)
	walk = func(a protocol.AgentView) {
		if o, ok := m.sessionOrder[a.SessionID]; ok && sessionKeyBefore(o, best) {
			best = o
		}
		for _, c := range children[a.SessionID] {
			walk(c)
		}
	}
	walk(root)
	return best
}

func (m Model) sessionNeedsOwner(a protocol.AgentView) bool {
	return !sessionEnded(a) && m.sessionHasOwnerRequest(a)
}

func (m Model) sessionHasOwnerRequest(a protocol.AgentView) bool {
	if a.Status == "needs you" || a.Status == "needs_you" || a.Worker == nil && a.Waiting != nil {
		return true
	}
	for _, ask := range m.asks {
		if ask.FromSession == a.SessionID && ask.State != protocol.AskWithSomeone && (ask.Kind == protocol.AskKindQuestion || ask.Kind == protocol.AskKindApproval || ask.Kind == protocol.AskKindWaiting) {
			return true
		}
	}
	return false
}

func (m *Model) toggleSessionTrees(all bool) {
	if m.collapsedTrees == nil {
		m.collapsedTrees = map[string]bool{}
	}
	groups := m.sessionGroups(false)
	roots := map[string]bool{}
	selectedRoot := m.selected
	for _, g := range groups {
		for id, mk := range g.marks {
			if mk.root != "" {
				roots[mk.root] = true
				if id == m.selected {
					selectedRoot = mk.root
				}
			}
		}
	}
	if all {
		collapse := false
		for id := range roots {
			if !m.collapsedTrees[id] {
				collapse = true
			}
		}
		for id := range roots {
			m.collapsedTrees[id] = collapse
		}
		if collapse && roots[selectedRoot] {
			m.selected = selectedRoot
		}
	} else if roots[selectedRoot] {
		m.collapsedTrees[selectedRoot] = !m.collapsedTrees[selectedRoot]
		m.selected = selectedRoot
	}
	m.ensureSelection()
	m.fixScroll()
}

// isMerger tells whether a session is a project's merger.
func isMerger(a protocol.AgentView) bool {
	return a.Type == "merger"
}

// projectOf is the project a session belongs to, as the session list names it.
func projectOf(a protocol.AgentView) string {
	return firstNonEmpty(a.Repo.Project, a.Profile.Project)
}

// mergerParent is the session of the Agent the stored tree says the merger
// reports to (decision 0160). A merger whose lead has no shown session stays
// at the top level.
func (m Model) mergerParent(a protocol.AgentView, shown map[string]protocol.AgentView) string {
	return m.sessionOfAgent(a.ReportsTo, a.SessionID, shown)
}

// sessionOfAgent picks the shown session that stands for an Agent: an online
// one first, then the lowest ID, so the tree order is stable.
func (m Model) sessionOfAgent(agent, self string, shown map[string]protocol.AgentView) string {
	if agent == "" {
		return ""
	}
	best := ""
	for id, s := range shown {
		if id == self || s.AgentID != agent || s.Worker != nil || isMerger(s) {
			continue
		}
		b := shown[best]
		if best == "" || s.Online && !b.Online || s.Online == b.Online && id < best {
			best = id
		}
	}
	return best
}

// mergerWorking tells whether a merger holds a ready or running step of one of
// its project's open changes, even when its harness reports idle or shell.
func (m Model) mergerWorking(a protocol.AgentView) bool {
	if !isMerger(a) {
		return false
	}
	project := projectOf(a)
	for _, ch := range m.delivery.changes {
		if ch.State != "" && ch.State != protocol.ChangeOpen {
			continue
		}
		if project != "" && ch.Repo != "" && ch.Repo != project {
			continue
		}
		if ch.StepState != protocol.StepStateReady && ch.StepState != protocol.StepStateRunning {
			continue
		}
		if ch.OwnerKind == protocol.OwnerMerger || ch.ClaimedBy != "" && ch.ClaimedBy == a.SessionID {
			return true
		}
	}
	return false
}

// visible is the selection order: every table row.
func (m Model) visible() []protocol.AgentView {
	var out []protocol.AgentView
	for _, g := range m.groups() {
		for _, a := range g.agents {
			out = append(out, a)
		}
	}
	return out
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
	if m.selIdx >= 0 && m.selIdx < len(vis) && vis[m.selIdx].SessionID == m.selected {
		return
	}
	for i, a := range vis {
		if a.SessionID == m.selected {
			m.selIdx = i
			return
		}
	}
	i := min(m.selIdx, len(vis)-1)
	m.selected, m.selIdx = vis[i].SessionID, i
}

// selectSession moves the Sessions selection to a visible session, so the
// owner comes back to the row they opened. A hidden session leaves it alone.
func (m *Model) selectSession(id string) {
	for i, a := range m.visible() {
		if a.SessionID == id {
			m.selected, m.selIdx = id, i
			m.fixScroll()
			return
		}
	}
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

// tableRow is an urgency heading, a session or the folded idle count.
type tableRow struct {
	header bool
	g      group
	agent  protocol.AgentView
	mark   treeMark // how a worker hangs under its caller
	fold   bool
}

func (m Model) tableRows() []tableRow {
	var rows []tableRow
	for _, g := range m.groups() {
		rows = append(rows, tableRow{header: true, g: g})
		for _, a := range g.agents {
			rows = append(rows, tableRow{agent: a, mark: g.marks[a.SessionID], g: g})
		}
		if g.hidden > 0 {
			rows = append(rows, tableRow{fold: true, g: g})
		}
	}
	return rows
}

// fixScroll moves the table so the selected session is on screen.
func (m *Model) fixScroll() {
	rows := m.tableRows()
	n := m.layout().sessions
	if m.repoLimitsLine(max(m.width, 1)) != "" {
		n = max(n-1, 1)
	}
	sel := -1
	for i, r := range rows {
		if !r.header && !r.fold && r.agent.SessionID == m.selected {
			sel = i
		}
	}
	if sel < 0 || n <= 0 {
		m.tableTop = 0
		return
	}
	if sel < m.tableTop {
		m.tableTop = sel
		// Show the group heading above its first session.
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

// applyCTOUI applies a CTO request or a daemon notice from the local channel.
func (m Model) applyCTOUI(q protocol.UIRequest) (tea.Model, tea.Cmd) {
	if q.EnterSession != "" {
		if q.EnterCTO {
			return m.openCTO(q.EnterSession)
		}
		if agent, ok := m.agentByID(q.EnterSession); ok {
			if !agent.Online {
				return m.resumeOwnerSession(agent)
			}
			return m.startEnterID(q.EnterSession)
		}
		return m, m.enterCmd(q.EnterSession, q.EnterSession)
	}

	if q.Text != "" {
		if q.HerdrOpened || q.DaemonNotice {
			m.sessionNoticeSerial++
			m.say(q.Text)
		} else {
			m.say("CTO: " + q.Text)
		}
		return m, nil
	}
	m.help, m.mapOn = false, false
	m.open = nil
	m.mode = modeNormal
	m.filtering = false
	m.uiFilter = q.Filter
	m.filter = q.Filter
	m.input.SetValue(q.Filter)
	m.focus = focusSessions
	var cmd tea.Cmd
	switch q.Tab {
	case "sessions":
		m.tab = sessionsTab
	case "messages":
		m.tab = messagesTab
		m.focus = focusFeed
	case "quotas":
		m.tab = quotasTab
	case "tools":
		m.tab = toolsTab
		if !m.tl.loading {
			var model tea.Model
			model, cmd = m.loadTools()
			m = model.(Model)
		}
	case "graph", "map":
		m.tab = graphTab
		m.mapOn = false
		m.graphGeneration++
		cmd = graphTick(m.graphGeneration)
		if q.Select != "" {
			m.mp.selected = q.Select
			for _, a := range m.snap.Agents {
				if (strings.EqualFold(q.Select, a.SessionID) || strings.EqualFold(q.Select, a.Profile.Name)) && a.Repo.Project != "" {
					m.mp.selected = a.Repo.Project
					break
				}
			}
			for _, node := range m.mp.nodes {
				if strings.EqualFold(node.Name, m.mp.selected) {
					m.mp.selected = node.Name
					break
				}
			}
		}
		if !m.mp.loading {
			m.mp.loading = true
			cmd = tea.Batch(cmd, m.mapCmd())
		}
		if q.Select == "" && q.Filter != "" {
			if nodes := m.mapProjectNodes(); len(nodes) > 0 {
				m.mp.selected = nodes[0].Name
			}
		}
		m.mp.pendingSelection = m.mp.selected
		m.graphState, _ = graphview.Update(m.graphState, graphview.SelectNode{Name: m.mp.selected})
	}
	if q.Tab == "sessions" || q.Tab == "messages" {
		for _, a := range m.snap.Agents {
			if strings.EqualFold(q.Select, a.SessionID) || q.Select != "" && (strings.EqualFold(q.Select, a.Profile.Name) || strings.EqualFold(q.Select, a.Repo.Project)) {
				m.selected = a.SessionID
				for _, g := range m.sessionGroups(false) {
					if mk := g.marks[a.SessionID]; mk.root != "" {
						delete(m.collapsedTrees, mk.root)
					}
				}
				if a.Status != "busy" && !m.sessionNeedsOwner(a) || isJunkSession(a) || !a.Online {
					m.activeOnly, m.showOffline = false, true
				}
				if isJunkSession(a) {
					m.showHiddenSessions = true
				}
				break
			}
		}
		if q.Tab == "sessions" {
			m.ensureSelection()
		} else {
			m.feedCur = m.feedLastVisible()
			for i := len(m.feed.items) - 1; i >= 0; i-- {
				it := m.feed.items[i]
				if q.Select != "" && (it.From == m.selected || it.To == m.selected) && m.feedShows(it) {
					m.feedCur = i
					break
				}
			}
		}
	}
	if q.Why == "" {
		q.Why = "Showing " + q.Tab
	}
	m.say("CTO: " + q.Why)
	return m, cmd
}

func (m Model) graphMouseActive() bool {
	return m.tab == graphTab && m.focus != focusCTO && m.mode == modeNormal && !m.mapOn && !m.help
}

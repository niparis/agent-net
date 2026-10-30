package tui

import (
	"context"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/niparis/agent-net/internal/protocol"
)

// maxFeedItems is how many feed lines are kept.
const maxFeedItems = 300

// maxSnippetRunes caps the first line of a body that the feed keeps.
const maxSnippetRunes = 500

// feedEvents is how much of the quiet news the feed shows: by default the
// worker lines, then all session and worker lines, then none.
type feedEvents int

const (
	eventsDefault feedEvents = iota
	eventsAll
	eventsNone
)

// snippetState says whether the first line of a message body was fetched. A
// fetch happens once per message, in the background.
type snippetState int

const (
	snipNone snippetState = iota
	snipLoading
	snipDone
	snipFailed
)

// Message states in the order a message passes through them. A later event
// never moves a message back (a late "delivered" after "replied" keeps "replied").
var stateRank = map[string]int{
	protocol.StateStored:     0,
	protocol.StateDispatched: 1,
	protocol.StateDelivered:  2,
	protocol.StateReplied:    3,
	protocol.StateFailed:     4,
	protocol.StateExpired:    4,
}

// feedItem is one line of the message feed. A message is one item whose state
// follows its events. Other events (a machine connecting, a refused send) are
// items with no message.
type feedItem struct {
	EventID        int64
	At             time.Time
	Kind           string // kind of the first event
	MsgID          string // set for a message
	From           string // sender session ID
	To             string // recipient session ID
	Type           string // request, reply or notification
	ConversationID string // conversation containing the message
	Parent         string // the message this one answers
	State          string
	FromCTO        bool
	Detail         string
	Host           string // machine of a node event
	Session        string // session of a session.new event
	// Snippet is the first line of the body, when this machine has a session
	// in the conversation. Snip says whether it was fetched.
	Snippet string
	Snip    snippetState
}

func (it *feedItem) isMessage() bool { return it.MsgID != "" }

// feed keeps the recent events of the network, as the daemon serves them.
type feed struct {
	lastID int64
	items  []*feedItem // oldest first
	byMsg  map[string]*feedItem
	// unavailable says why there is no feed (a daemon or gateway that is too
	// old), or is empty.
	unavailable string
}

func newFeed() feed { return feed{byMsg: map[string]*feedItem{}} }

// apply adds events, oldest first. Events at or before the last seen ID are
// ignored, so a repeated poll changes nothing.
func (f *feed) apply(evs []protocol.FeedEvent) {
	for _, e := range evs {
		if e.ID <= f.lastID {
			continue
		}
		f.lastID = e.ID
		switch {
		case strings.HasPrefix(e.Kind, "msg.") && e.MessageID != "":
			f.applyMessage(e)
		case e.Kind == "msg.refused":
			f.add(&feedItem{EventID: e.ID, At: e.Time, Kind: e.Kind, From: e.SessionID, Detail: e.Detail, State: "refused"})
		case e.Kind == "node.connected" || e.Kind == "node.disconnected" || e.Kind == "node.enrolled":
			f.add(&feedItem{EventID: e.ID, At: e.Time, Kind: e.Kind, Host: e.Hostname, Detail: e.Detail})
		case strings.HasPrefix(e.Kind, "worker."):
			f.add(&feedItem{EventID: e.ID, At: e.Time, Kind: e.Kind, Session: e.SessionID, Host: e.Hostname, Detail: e.Detail})
		case e.Kind == "session.new":
			f.add(&feedItem{EventID: e.ID, At: e.Time, Kind: e.Kind, Session: e.SessionID, Host: e.Hostname, Detail: e.Detail})
		}
	}
}

func (f *feed) add(it *feedItem) {
	f.items = append(f.items, it)
	if len(f.items) > maxFeedItems {
		drop := len(f.items) - maxFeedItems
		for _, old := range f.items[:drop] {
			if old.MsgID != "" {
				delete(f.byMsg, old.MsgID)
			}
		}
		f.items = append([]*feedItem(nil), f.items[drop:]...)
	}
}

func (f *feed) applyMessage(e protocol.FeedEvent) {
	state := strings.TrimPrefix(e.Kind, "msg.")
	if _, ok := stateRank[state]; !ok {
		return
	}
	it := f.byMsg[e.MessageID]
	if it == nil {
		it = &feedItem{EventID: e.ID, At: e.Time, Kind: e.Kind, MsgID: e.MessageID, Parent: e.Parent, State: state}
		f.byMsg[e.MessageID] = it
		f.add(it)
	}
	if e.ConversationID != "" {
		it.ConversationID = e.ConversationID
	}
	it.FromCTO = it.FromCTO || e.FromCTO || (e.Kind == "msg.stored" && parseDetail(e.Detail)["by"] == "cto")
	if it.Parent == "" {
		it.Parent = e.Parent
	}
	// The ends and the type come with the event; a message whose row is gone
	// (old data) is read from the detail of the stored event.
	if it.From == "" {
		it.From = e.From
	}
	if it.To == "" {
		it.To = e.To
	}
	if it.Type == "" {
		it.Type = e.Type
	}
	if e.Kind == "msg.stored" {
		d := parseDetail(e.Detail)
		if it.From == "" {
			it.From = e.SessionID
		}
		if it.To == "" {
			it.To = d["to"]
		}
		if it.Type == "" {
			it.Type = d["type"]
		}
	}
	if stateRank[state] >= stateRank[it.State] {
		it.State = state
		if e.Detail != "" && e.Kind != "msg.stored" {
			it.Detail = e.Detail
		}
	}
	// A stored reply answers its parent: the parent is "replied", which has no
	// event of its own.
	if e.Kind == "msg.stored" && e.Parent != "" {
		if p := f.byMsg[e.Parent]; p != nil && stateRank[p.State] < stateRank[protocol.StateReplied] {
			p.State = protocol.StateReplied
		}
	}
}

// parseDetail reads "key=value key=value" as the gateway writes the detail of
// a stored message.
func parseDetail(s string) map[string]string {
	out := map[string]string{}
	for _, f := range strings.Fields(s) {
		if k, v, ok := strings.Cut(f, "="); ok {
			out[k] = v
		}
	}
	return out
}

// detailReason returns the value of reason= in an event detail. It is the
// last field and may be quoted and hold spaces, so it runs to the end.
func detailReason(detail string) string {
	_, rest, ok := strings.Cut(detail, "reason=")
	if !ok {
		return ""
	}
	rest = strings.TrimSpace(rest)
	if u, err := strconv.Unquote(rest); err == nil {
		return u
	}
	return strings.Trim(rest, `"`)
}

// messagesOf returns the message items that involve a session, newest first.
func (f *feed) messagesOf(sessionID string, limit int) []*feedItem {
	var out []*feedItem
	for i := len(f.items) - 1; i >= 0 && len(out) < limit; i-- {
		it := f.items[i]
		if it.isMessage() && (it.From == sessionID || it.To == sessionID) {
			out = append(out, it)
		}
	}
	return out
}

// ---- message bodies ----

// snippetMsg is the answer to a request for the first line of a body.
type snippetMsg struct {
	id  string
	msg protocol.Message
	err error
}

// snippetCmd asks the daemon for one message body, the same call the
// conversation view uses. The gateway only answers the machines with a session
// in the conversation.
func (m Model) snippetCmd(id string) tea.Cmd {
	src := m.src
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), Timeout)
		defer cancel()
		msg, err := src.Message(ctx, id)
		return snippetMsg{id: id, msg: msg, err: err}
	}
}

// wantSnippets starts one background fetch for the newest message whose body
// this machine may read and which has none yet. One at a time keeps the feed
// cheap, and the answer never blocks a redraw.
func (m *Model) wantSnippets() tea.Cmd {
	if m.feed.unavailable != "" || m.conv != nil || m.help {
		return nil
	}
	for _, it := range m.feed.items {
		if it.Snip == snipLoading {
			return nil
		}
	}
	for i := len(m.feed.items) - 1; i >= 0; i-- {
		it := m.feed.items[i]
		if !it.isMessage() || it.Snip != snipNone || !m.localEnd(it) {
			continue
		}
		it.Snip = snipLoading
		return m.snippetCmd(it.MsgID)
	}
	return nil
}

// applySnippet keeps the first line of a body, or remembers that there is none.
func (m *Model) applySnippet(r snippetMsg) {
	it := m.feed.byMsg[r.id]
	if it == nil {
		return // the line was dropped from the feed
	}
	if r.err != nil {
		it.Snip = snipFailed
		return
	}
	if r.msg.ConversationID != "" {
		it.ConversationID = r.msg.ConversationID
	}
	it.FromCTO = it.FromCTO || r.msg.FromCTO
	it.Snippet = firstLine(r.msg.Body)
	it.Snip = snipDone
}

// firstLine returns the first line of a body, cleaned for one terminal line.
func firstLine(body string) string {
	line, _, _ := strings.Cut(sanitizeBody(body), "\n")
	line = strings.TrimSpace(line)
	if r := []rune(line); len(r) > maxSnippetRunes {
		line = string(r[:maxSnippetRunes])
	}
	return line
}

// localSession tells whether a session runs on this machine.
func (m Model) localSession(id string) bool {
	if id == "" {
		return false
	}
	if m.snap.OwnerSession != "" && id == m.snap.OwnerSession {
		return true
	}
	a, ok := m.agentByID(id)
	return ok && a.NodeID == m.snap.NodeID
}

// localEnd tells whether this machine has a session at one end of a message,
// which is the only case where its body may be read here.
func (m Model) localEnd(it *feedItem) bool {
	return m.localSession(it.From) || m.localSession(it.To)
}

// feedConvID returns the session of this machine whose conversation a feed
// line opens. The recipient comes first: it is the one that answers.
func (m Model) feedConvID(it *feedItem) string {
	for _, id := range []string{it.To, it.From} {
		a, ok := m.agentByID(id)
		if ok && a.NodeID == m.snap.NodeID && !protocol.IsOwner(a.Harness) {
			return id
		}
	}
	return ""
}

// ---- what the feed shows ----

// feedShows tells whether a line passes the lifecycle filter. New sessions are
// quiet; worker lines, including a refused worker, show by default, and the l
// key shows every lifecycle line or hides all of them.
func (m Model) feedShows(it *feedItem) bool {
	if m.tab == 1 && m.uiFilter != "" {
		text := strings.ToLower(strings.Join([]string{it.Kind, it.From, it.To, it.Detail, it.Snippet, it.Host, m.feedName(it.From), m.feedName(it.To)}, " "))
		if !strings.Contains(text, strings.ToLower(m.uiFilter)) {
			return false
		}
	}
	switch it.Kind {
	case "session.new":
		return m.events == eventsAll
	case "worker.started", "worker.stopped", "worker.refused":
		return m.events != eventsNone
	}
	return true
}

// feedVisible returns the positions in feed.items that are drawn, oldest
// first. The cursor is an index in feed.items, so hidden lines keep their
// place in the data.
func (m Model) feedVisible() []int {
	out := make([]int, 0, len(m.feed.items))
	for i, it := range m.feed.items {
		if m.feedShows(it) {
			out = append(out, i)
		}
	}
	return out
}

// feedLastVisible is the item index of the newest drawn line, or -1.
func (m Model) feedLastVisible() int {
	vis := m.feedVisible()
	if len(vis) == 0 {
		return -1
	}
	return vis[len(vis)-1]
}

// feedClamp keeps the cursor on a drawn line.
func (m *Model) feedClamp() {
	vis := m.feedVisible()
	if len(vis) == 0 {
		m.feedCur = -1
		return
	}
	if m.feedCur < 0 {
		m.feedCur = vis[len(vis)-1] // nothing was drawn before: start at the newest
		return
	}
	best := vis[0]
	for _, i := range vis {
		if i == m.feedCur {
			return
		}
		if i < m.feedCur {
			best = i
		}
	}
	m.feedCur = best
}

// feedPos is the place of the cursor among the drawn lines, or -1.
func (m Model) feedPos() int {
	for p, i := range m.feedVisible() {
		if i == m.feedCur {
			return p
		}
	}
	return -1
}

// moveFeed moves the cursor by delta over the drawn lines only.
func (m *Model) moveFeed(delta int) {
	vis := m.feedVisible()
	if len(vis) == 0 {
		m.feedCur = -1
		return
	}
	pos := m.feedPos()
	if pos < 0 {
		pos = len(vis) - 1
	}
	pos = min(max(pos+delta, 0), len(vis)-1)
	m.feedCur = vis[pos]
}

// ---- names ----

// feedName is the name of a session in the feed: the session name alone, never
// the machine. The owner is "you".
func (m Model) feedName(id string) string {
	a, ok := m.agentByID(id)
	if !ok {
		return shortID(id)
	}
	if protocol.IsOwner(a.Harness) {
		return "you"
	}
	if n := strings.TrimSpace(a.Profile.Name); n != "" {
		return sanitize(n)
	}
	return shortID(id)
}

// duplicateFeedNames finds the names that two sessions in the feed share. Only
// those get a short host mark, so the common line stays short.
func (m Model) duplicateFeedNames() map[string]bool {
	sessions := map[string]map[string]bool{}
	for _, it := range m.feed.items {
		if !it.isMessage() || !m.feedShows(it) {
			continue
		}
		for _, id := range []string{it.From, it.To} {
			if id == "" {
				continue
			}
			n := m.feedName(id)
			if sessions[n] == nil {
				sessions[n] = map[string]bool{}
			}
			sessions[n][id] = true
		}
	}
	dup := map[string]bool{}
	for n, ids := range sessions {
		if len(ids) > 1 {
			dup[n] = true
		}
	}
	return dup
}

// feedDisplayName adds a short host mark to a name only when it is ambiguous.
func (m Model) feedDisplayName(id string, dup map[string]bool) string {
	name := m.feedName(id)
	if !dup[name] {
		return name
	}
	if a, ok := m.agentByID(id); ok {
		if host := shortHost(m.nodeName(a.NodeID, a.Hostname)); host != "" {
			return name + "@" + host
		}
	}
	return name
}

// shortHost cuts a machine name to a mark of at most 8 characters.
func shortHost(s string) string {
	s = sanitize(s)
	if i := strings.Index(s, "."); i > 0 {
		s = s[:i]
	}
	if r := []rune(s); len(r) > 8 {
		s = string(r[:8])
	}
	return s
}

package tui

import (
	"strconv"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// maxFeedItems is how many feed lines are kept.
const maxFeedItems = 300

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
	EventID int64
	At      time.Time
	Kind    string // kind of the first event
	MsgID   string // set for a message
	From    string // sender session ID
	To      string // recipient session ID
	Type    string // request, reply or notification
	State   string
	Detail  string
	Host    string // machine of a node event
	Session string // session of a session.new event
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
		it = &feedItem{EventID: e.ID, At: e.Time, Kind: e.Kind, MsgID: e.MessageID, State: state}
		f.byMsg[e.MessageID] = it
		f.add(it)
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

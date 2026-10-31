package tui

import (
	"reflect"
	"sort"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// activityBand keeps a session in its old band until the idle margin expires.
func activityBand(now, activity time.Time, previous int, online, urgent bool) int {
	if !online {
		return 4
	}
	if urgent {
		return 0
	}
	age := now.Sub(activity)
	limits := [...]time.Duration{0, 10 * time.Minute, time.Hour, 24 * time.Hour}
	margins := [...]time.Duration{0, 2 * time.Minute, 10 * time.Minute, time.Hour}
	band := 4
	for i := 1; i <= 3; i++ {
		if !activity.IsZero() && age <= limits[i] {
			band = i
			break
		}
	}
	if previous >= 1 && previous <= 3 && band > previous && !activity.IsZero() && age <= limits[previous]+margins[previous] {
		return previous
	}
	return band
}

type sessionOrder struct {
	band     int
	rank     int64
	activity time.Time
	status   string
	profile  protocol.Profile
	waiting  bool
	pending  bool
}

func (m *Model) updateSessionOrder(events []protocol.FeedEvent) {
	now := m.now()
	if m.sessionOrder == nil {
		m.sessionOrder = make(map[string]sessionOrder)
	}
	initialized := len(m.sessionOrder) > 0
	agents := append([]protocol.AgentView(nil), m.snap.Agents...)
	sort.SliceStable(agents, func(i, j int) bool {
		if a, b := sessionStart(agents[i]), sessionStart(agents[j]); a != b {
			return a < b
		}
		return agents[i].SessionID < agents[j].SessionID
	})
	for _, a := range agents {
		old, exists := m.sessionOrder[a.SessionID]
		seen := time.Time{}
		if a.LastSeen > 0 {
			seen = time.Unix(a.LastSeen, 0)
		}
		if seen.After(old.activity) {
			old.activity = seen
		}
		waiting := a.Waiting != nil
		if exists && ((a.Status != old.status && a.Status == "busy") || (waiting && !old.waiting) || !reflect.DeepEqual(a.Profile, old.profile)) {
			old.activity = now
		}
		old.status, old.profile, old.waiting = a.Status, a.Profile, waiting
		for _, e := range events {
			if (e.SessionID == a.SessionID || e.From == a.SessionID || e.To == a.SessionID) && e.Kind == "msg.stored" && e.Time.After(old.activity) {
				old.activity = e.Time
			}
		}
		prev := -1
		if exists {
			prev = old.band
		}
		band := activityBand(now, old.activity, prev, a.Online, a.Status == "busy" || waiting)
		if !exists {
			m.sessionNext++
			old.rank = m.sessionNext
			old.band = band
			if initialized {
				if now.Before(m.orderFrozenUntil) {
					old.pending = true
				} else {
					m.sessionFront--
					old.rank = m.sessionFront
				}
			}
		} else if (band != old.band || old.pending) && !now.Before(m.orderFrozenUntil) {
			m.sessionFront--
			old.rank = m.sessionFront
			old.band = band
			old.pending = false
		}
		m.sessionOrder[a.SessionID] = old
	}
}

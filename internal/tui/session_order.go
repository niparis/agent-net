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

// sessionActive is when a session last did something, from the gateway. An
// older gateway sends no last active time; fall back to when it last saw the
// session, which is what the screen used before.
func sessionActive(a protocol.AgentView) time.Time {
	if a.LastActive > 0 {
		return time.Unix(a.LastActive, 0)
	}
	if a.LastSeen > 0 {
		return time.Unix(a.LastSeen, 0)
	}
	return time.Time{}
}

func (m *Model) updateSessionOrder(events []protocol.FeedEvent) {
	now := m.now()
	if m.sessionOrder == nil {
		m.sessionOrder = make(map[string]sessionOrder)
	}
	initialized := len(m.sessionOrder) > 0
	agents := append([]protocol.AgentView(nil), m.snap.Agents...)
	// The first rank of a band follows real activity, newest first. The time
	// the daemon last reported a session says nothing about its use.
	sort.SliceStable(agents, func(i, j int) bool {
		a, b := sessionActive(agents[i]), sessionActive(agents[j])
		if !a.Equal(b) {
			return a.After(b)
		}
		return agents[i].SessionID < agents[j].SessionID
	})
	for _, a := range agents {
		old, exists := m.sessionOrder[a.SessionID]
		before := old.activity
		if active := sessionActive(a); active.After(old.activity) {
			old.activity = active
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
		} else if (band != old.band || old.pending || old.activity.After(before)) && !now.Before(m.orderFrozenUntil) {
			// A newer activity wins inside its band too: most recently active
			// first, never oldest start first.
			m.sessionFront--
			old.rank = m.sessionFront
			old.band = band
			old.pending = false
		}
		m.sessionOrder[a.SessionID] = old
	}
}

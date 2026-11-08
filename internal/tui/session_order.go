package tui

import (
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// workingGrace is how long after its last activity a session stays in the
// Working band. Once an hour passes with nothing done, it moves to Idle.
const workingGrace = time.Hour

// sessionEnded tells whether a session is over: its daemon no longer reports
// it, its harness process exited, or it ended. An ended session is never
// Working and never lifts its caller's tree into a more urgent band.
func sessionEnded(a protocol.AgentView) bool {
	if !a.Online {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(a.Status)) {
	case "ended", "exited", "offline":
		return true
	}
	return false
}

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

// sessionActive is when a session last did something, from the gateway.
// An unknown time stays unknown: a discovery report is not activity.
func sessionActive(a protocol.AgentView) time.Time {
	if a.LastActive > 0 {
		return time.Unix(a.LastActive, 0)
	}
	return time.Time{}
}

// activeWithin tells whether the session did something within d. It uses the
// same activity time as the order, never the last report from its daemon.
func (m Model) activeWithin(a protocol.AgentView, d time.Duration) bool {
	active := sessionActive(a)
	return !active.IsZero() && m.now().Sub(active) <= d
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
		band := activityBand(now, old.activity, prev, !sessionEnded(a), a.Status == "busy" || waiting)
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

// sessionKeyBefore orders two activity keys the way the Sessions list does:
// the more urgent band first, then the newer activity, then the newer rank.
func sessionKeyBefore(a, b sessionOrder) bool {
	if a.band != b.band {
		return a.band < b.band
	}
	if !a.activity.Equal(b.activity) {
		return a.activity.After(b.activity)
	}
	return a.rank < b.rank
}

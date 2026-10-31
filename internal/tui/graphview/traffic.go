package graphview

import "time"

const travelTime = 3 * time.Second
const flashTime = 700 * time.Millisecond

type Message struct {
	From, To, Kind string
	At             time.Time
	Voice          bool
	FromOwner      bool
}
type Comet struct {
	Message
	Position Point
	Progress float64
}

// Message records a real message, including replies as separate events.
func (g *Graph) Message(from, to, kind string, at time.Time, voice ...bool) {
	if _, ok := g.Positions[from]; !ok {
		return
	}
	if _, ok := g.Positions[to]; !ok {
		return
	}
	v := len(voice) > 0 && voice[0]
	owner := len(voice) > 1 && voice[1]
	g.messages = append(g.messages, Message{From: from, To: to, Kind: kind, At: at, Voice: v, FromOwner: owner})
	if len(g.messages) > 100 {
		g.messages = g.messages[len(g.messages)-100:]
	}
}
func (g *Graph) Recent() []Message {
	n := len(g.messages)
	return append([]Message(nil), g.messages[max(0, n-2):]...)
}

// Traffic samples the animation at a caller-supplied time, making it deterministic.
func (g *Graph) Traffic(now time.Time) ([]Comet, []string) {
	var comets []Comet
	var flashes []string
	for _, m := range g.messages {
		age := now.Sub(m.At)
		if age < 0 {
			continue
		}
		if age >= travelTime {
			if age < travelTime+flashTime {
				flashes = append(flashes, m.To)
			}
			continue
		}
		a, aok := g.Positions[m.From]
		b, bok := g.Positions[m.To]
		if !aok || !bok {
			continue
		}
		u := float64(age) / float64(travelTime)
		comets = append(comets, Comet{m, Point{a.X + (b.X-a.X)*u, a.Y + (b.Y-a.Y)*u}, u})
	}
	return comets, flashes
}

// ShiftTraffic preserves animation progress while traffic is paused.
func (g *Graph) ShiftTraffic(start, end time.Time) {
	if !end.After(start) {
		return
	}
	for i := range g.messages {
		if g.messages[i].At.Before(start) {
			g.messages[i].At = g.messages[i].At.Add(end.Sub(start))
		} else if g.messages[i].At.Before(end) {
			g.messages[i].At = end
		}
	}
}

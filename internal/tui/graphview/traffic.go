package graphview

import "time"

const travelTime = 3 * time.Second
const flashTime = 700 * time.Millisecond

type Message struct {
	From, To, Kind string
	At             time.Time
}
type Comet struct {
	Message
	Position Point
	Progress float64
}

// Message records a real message, including replies as separate events.
func (g *Graph) Message(from, to, kind string, at time.Time) {
	if _, ok := g.Positions[from]; !ok {
		return
	}
	if _, ok := g.Positions[to]; !ok {
		return
	}
	g.messages = append(g.messages, Message{from, to, kind, at})
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

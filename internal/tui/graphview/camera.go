package graphview

import (
	"math"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// cameraEase is how long a zoom, fit or selection move takes. It is short
// enough to feel immediate and long enough to read as motion.
const cameraEase = 300 * time.Millisecond

// CameraTween eases the camera between two states without changing the target
// held in State.Camera.
type CameraTween struct {
	From, To Camera
	Start    time.Time
	Duration time.Duration
}

func (t *CameraTween) End() time.Time { return t.Start.Add(t.Duration) }

// Done reports whether the tween has reached its target.
func (t *CameraTween) Done(now time.Time) bool {
	return t == nil || !now.Before(t.End())
}

// At samples the tween. The ease is a smoothstep, so it starts and ends slowly.
func (t *CameraTween) At(now time.Time) Camera {
	if t == nil {
		return Camera{}
	}
	if t.Duration <= 0 || !now.Before(t.End()) {
		return t.To
	}
	if now.Before(t.Start) {
		return t.From
	}
	u := smoothstep(float64(now.Sub(t.Start)) / float64(t.Duration))
	return Camera{
		Center:  Point{t.From.Center.X + (t.To.Center.X-t.From.Center.X)*u, t.From.Center.Y + (t.To.Center.Y-t.From.Center.Y)*u},
		Zoom:    t.From.Zoom + (t.To.Zoom-t.From.Zoom)*u,
		FitZoom: t.From.FitZoom + (t.To.FitZoom-t.From.FitZoom)*u,
		ScaleX:  t.From.ScaleX + (t.To.ScaleX-t.From.ScaleX)*u,
		ScaleY:  t.From.ScaleY + (t.To.ScaleY-t.From.ScaleY)*u,
	}
}

// ViewCamera is the camera the scene is drawn with: the eased one while a
// move is running, and the target otherwise.
func (s State) ViewCamera() Camera {
	if s.Tween == nil {
		return s.Camera
	}
	return s.Tween.At(s.Now)
}

// ease starts a camera move from the currently shown camera to to. Braille
// keeps jumping: Smooth is only set in pixel mode.
func (s State) ease(from, to Camera) State {
	if !s.Smooth {
		s.Tween = nil
		return s
	}
	if from == to {
		s.Tween = nil
		return s
	}
	s.Tween = &CameraTween{From: from, To: to, Start: s.Now, Duration: cameraEase}
	return s
}

// easeToSelection moves the camera centre to the selected node, keeping its
// zoom. It is a no-op without a graph or when pixel motion is off.
func (s State) easeToSelection() State {
	if !s.Smooth || s.Graph == nil {
		return s
	}
	selected := s.Graph.Selected()
	if selected == protocol.GraphVoiceOwner || selected == protocol.CTONodeName {
		return s
	}
	p, ok := s.Graph.Positions[selected]
	if !ok {
		return s
	}
	from := s.ViewCamera()
	target := s.Camera
	target.Center = p
	s.Camera = target
	return s.ease(from, target)
}

// Moving reports whether anything in the picture changes right now, so the
// host can render fast frames only while there is motion.
func (s State) Moving() bool {
	if s.Tween != nil && !s.Tween.Done(s.Now) {
		return true
	}
	// Space stops the traffic and the busy pulse while it is held.
	if s.Paused {
		return false
	}
	if s.Graph == nil {
		return false
	}
	for _, m := range s.Graph.messages {
		age := s.Now.Sub(m.At)
		if age >= 0 && age < travelTime+flashTime {
			return true
		}
	}
	if !s.ReducedMotion {
		for _, n := range s.Graph.Nodes {
			for _, session := range n.Sessions {
				if session.Status == "busy" {
					return true
				}
			}
		}
	}
	return false
}

// smoothstep exposes the ease curve to tests.
func smoothstep(u float64) float64 {
	u = math.Min(math.Max(u, 0), 1)
	return u * u * (3 - 2*u)
}

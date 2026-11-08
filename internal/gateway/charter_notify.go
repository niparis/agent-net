package gateway

import (
	"context"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// charterNotify runs only after a commit that changed charter text: a value at
// any level, a team, or an undo. The hook is installed by the gateway.
func (s *Store) charterNotify() {
	s.charterHookMu.RLock()
	hook := s.charterHook
	s.charterHookMu.RUnlock()
	if hook != nil {
		hook()
	}
}

// charterWrote passes a write's error through and, when the write succeeded,
// tells the connected daemons to drop the charter text they cached. A write that
// changed nothing also notifies; one small frame per owner write costs nothing.
func (s *Store) charterWrote(err error) error {
	if err == nil {
		s.charterNotify()
	}
	return err
}

// charterChanged sends FrameCharterChanged to every connected daemon. Each one
// drops its cached policy and graph answers, so the next turn of every session
// reads the new charter at once and a turn costs no extra call.
func (g *Gateway) charterChanged() {
	g.mu.Lock()
	conns := make([]*nodeConn, 0, len(g.conns))
	for _, c := range g.conns {
		conns = append(conns, c)
	}
	g.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, c := range conns {
		if frame, err := protocol.EncodeRequest(protocol.FrameCharterChanged, protocol.NewID("d"), nil); err == nil {
			c.write(ctx, frame)
		}
	}
}

package daemon

import (
	"context"

	"github.com/niparis/agent-net/internal/protocol"
)

// Every frame the daemon sends to the gateway carries a short origin label
// (protocol.Header.Via): who made the request. The gateway only counts by it.
// The label rides on the context of the local call, so a call made for the
// owner's TUI keeps it down to the frame. A call with no label on its context
// is the daemon's own loop.

type viaKey struct{}

func withVia(ctx context.Context, via string) context.Context {
	if via == "" {
		return ctx
	}
	return context.WithValue(ctx, viaKey{}, via)
}

// viaFrom is the origin of a call: the label on ctx, else the daemon itself.
func viaFrom(ctx context.Context) string {
	if v, ok := ctx.Value(viaKey{}).(string); ok {
		return v
	}
	return protocol.ViaDaemon
}

// via names the kind of caller behind this local connection. A connection
// that has not said hello yet is unknown.
func (lc *localConn) via() string {
	lc.mu.Lock()
	client, tui, ls := lc.client, lc.tui, lc.ls
	lc.mu.Unlock()
	switch client {
	case "":
		return protocol.ViaUnknown
	case "cli":
		if tui {
			return protocol.ViaTUI
		}
		return protocol.ViaCLI
	case "cto-mcp":
		return protocol.ViaCTO
	}
	if ls != nil {
		lc.d.mu.Lock()
		worker := ls.info.Worker != nil
		lc.d.mu.Unlock()
		if worker {
			return protocol.ViaWorker
		}
	}
	return protocol.ViaMCP
}

package daemon

import (
	"context"
	"encoding/json"

	"github.com/niparis/agent-net/internal/mcpserver"
	"github.com/niparis/agent-net/internal/protocol"
)

// dispatchCatalogRead serves the four read tools that filter, page or fetch:
// messages_find, message_get, workers_find and servers_find. The input is
// the tool input and the answer is the tool output, so the MCP server and the
// native plugins get one answer (principle 18).
func (lc *localConn) dispatchCatalogRead(ctx context.Context, method string, raw json.RawMessage) (res any, handled bool, err error) {
	switch method {
	case protocol.MethodMessagesFind, protocol.MethodMessageGet, protocol.MethodWorkersFind, protocol.MethodMCPServersFind:
	default:
		return nil, false, nil
	}
	res, err = lc.serveRead(ctx, method, raw)
	return res, true, err
}

// serveRead runs one shared read (mcpserver.ServeRead) for the session bound
// to this connection. The reads go through this connection's own methods, so
// the bound session decides what they may see.
func (lc *localConn) serveRead(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	sid, err := lc.boundID()
	if err != nil {
		return nil, err
	}
	who := mcpserver.ReadCaller{SessionID: sid, Sessions: lc.d.Sessions()}
	for _, s := range who.Sessions {
		if s.SessionID == sid {
			who.Name, who.Project = s.Name, s.Repo.Project
		}
	}
	call := func(ctx context.Context, method string, params any) (json.RawMessage, error) {
		in, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		out, err := lc.dispatch(ctx, method, in)
		if err != nil {
			return nil, err
		}
		return json.Marshal(out)
	}
	return mcpserver.ServeRead(ctx, method, raw, who, call)
}

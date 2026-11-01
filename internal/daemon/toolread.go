package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// The read tools of the tool redesign. The daemon verifies the caller and
// forwards the request to the gateway, which holds the data and does the
// filtering and paging. The caller is never taken from the request.

func (lc *localConn) toolReadCaller() string {
	if id, err := lc.boundID(); err == nil && id != "" {
		return id
	}
	return "local"
}

func (lc *localConn) forwardToolRead(ctx context.Context, typ string, p any) (any, bool, error) {
	res, err := lc.d.call(ctx, typ, p)
	return res, true, err
}

func (lc *localConn) dispatchToolRead(ctx context.Context, client, method string, raw json.RawMessage) (res any, handled bool, err error) {
	switch method {
	case "sessions_find":
		var p protocol.SessionsFindRequest
		if len(raw) > 0 && json.Unmarshal(raw, &p) != nil {
			return nil, true, errBadToolRead("sessions_find")
		}
		p.Caller = lc.toolReadCaller()
		return lc.forwardToolRead(ctx, "query.sessions_find", p)

	case "session_get":
		var p protocol.SessionGetRequest
		if json.Unmarshal(raw, &p) != nil || strings.TrimSpace(p.Session) == "" {
			return nil, true, errBadToolRead("session_get")
		}
		p.Caller = lc.toolReadCaller()
		return lc.forwardToolRead(ctx, "query.session_get", p)

	case "machines_find":
		var p protocol.MachinesFindRequest
		if len(raw) > 0 && json.Unmarshal(raw, &p) != nil {
			return nil, true, errBadToolRead("machines_find")
		}
		p.Caller = lc.toolReadCaller()
		return lc.forwardToolRead(ctx, "query.machines_find", p)

	case "machine_get":
		var p protocol.MachineGetRequest
		if json.Unmarshal(raw, &p) != nil || strings.TrimSpace(p.Machine) == "" {
			return nil, true, errBadToolRead("machine_get")
		}
		if len(p.Include) > 1 {
			return nil, true, errors.New("machine_get takes at most one include category, because the cursor pages that category. Example: {\"machine\":\"research\",\"include\":[\"models\"]}")
		}
		p.Caller = lc.toolReadCaller()
		return lc.forwardToolRead(ctx, "query.machine_get", p)

	case "projects_find":
		var p protocol.ProjectsFindRequest
		if len(raw) > 0 && json.Unmarshal(raw, &p) != nil {
			return nil, true, errBadToolRead("projects_find")
		}
		p.Caller = lc.toolReadCaller()
		return lc.forwardToolRead(ctx, "query.projects_find", p)
	}
	return nil, false, nil
}

func errBadToolRead(method string) error {
	return errors.New("bad " + method + " params; use the tool's input schema")
}

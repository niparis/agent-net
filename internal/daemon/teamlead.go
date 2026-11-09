package daemon

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/niparis/agent-net/internal/protocol"
)

// teamLeadCall serves MethodTeamLead. A session asks for the lead of its own
// team, so the Claude Code hint hook can send a denied step there. It is a
// read: it changes nothing. A plugin or MCP client is bound to its session; the
// command line names the harness and native ID of the session it runs for.
func (lc *localConn) teamLeadCall(ctx context.Context, client string, ls *localSession, raw json.RawMessage) (any, error) {
	var p protocol.TeamLeadRequest
	if json.Unmarshal(raw, &p) != nil {
		return nil, errors.New("bad team_lead params")
	}
	d := lc.d
	if client == "cli" {
		d.mu.Lock()
		ls = d.sessions[key(p.Harness, p.NativeID)]
		d.mu.Unlock()
		if ls == nil || p.NativeID == "" {
			return protocol.TeamLeadResult{}, nil // not a session of this daemon: silent
		}
	} else if ls == nil {
		return nil, errors.New("this method needs a bound session")
	}
	sid := d.currentSessionID(ls)
	if sid == "" {
		return protocol.TeamLeadResult{}, nil
	}
	data, err := d.call(ctx, "query.team_lead", protocol.QueryTeamLead{SessionID: sid})
	if err != nil {
		return protocol.TeamLeadResult{}, err
	}
	var res protocol.TeamLeadResult
	err = json.Unmarshal(data, &res)
	return res, err
}

package daemon

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/niparis/agent-net/internal/protocol"
)

// Charter text at session start. Every harness whose session has the agent-net
// MCP server reads the server's instructions once, when the session starts. The
// MCP server asks for the charter text here and puts it into them, so a rule
// the owner sets once at the global level reaches Codex and agy sessions too,
// which have no per-turn hook. The per-turn paths (the Claude Code hook and the
// Pi and OpenCode plugins) carry the same text through MethodPolicy.
//
// The effective charter of a node already holds the global level: `refuses` and
// `rules` join across global, team, repo and agent. Only a session that no node
// covers needs the global level read apart (globalCharterText).

// joinCharter puts the node's context and the agent's charter in one text.
func joinCharter(node, agent string) string {
	switch {
	case node != "" && agent != "":
		return node + "\n" + agent
	case node != "":
		return node
	}
	return agent
}

// charterTextCall serves MethodCharterText. It is for the tools of a session,
// not for the command line.
func (lc *localConn) charterTextCall(ctx context.Context, client string, ls *localSession) (any, error) {
	if client == "cli" {
		return nil, errors.New("charter text is for session tools")
	}
	return protocol.CharterTextResult{Text: lc.d.charterTextFor(ctx, ls)}, nil
}

// charterTextFor is the charter text for a session: its node and its agent. A
// connection with no session, or a session the gateway has not registered yet,
// gets the global level alone. It never asks for the one-time charter draft
// request, which belongs to a model turn.
func (d *Daemon) charterTextFor(ctx context.Context, ls *localSession) string {
	if ls != nil {
		if sid := d.currentSessionID(ls); sid != "" {
			cctx, cancel := context.WithTimeout(ctx, policyCallTimeout)
			raw, err := d.call(cctx, "policy.get", protocol.PolicyGet{SessionID: sid})
			cancel()
			var got protocol.PolicyGot
			if err == nil && json.Unmarshal(raw, &got) == nil {
				charter, notes := d.sessionContext(ctx, sid, got.Project)
				if text := joinCharter(joinCharter(charter, got.Charter), notes); text != "" {
					return text
				}
			}
		}
	}
	return d.globalCharterText(ctx)
}

// globalLevel asks the gateway for the global level of the charter. ok is
// false when the gateway cannot be reached.
func (d *Daemon) globalLevel(ctx context.Context) (global protocol.GlobalCharter, ok bool) {
	cctx, cancel := context.WithTimeout(ctx, policyCallTimeout)
	defer cancel()
	raw, err := d.call(cctx, "charter.global", struct{}{})
	var got protocol.GlobalCharterGot
	if err != nil || json.Unmarshal(raw, &got) != nil {
		return global, false
	}
	return got.Global, true
}

// globalCharterText is the global level alone as text. Empty when the owner set
// none, or the gateway cannot be reached.
func (d *Daemon) globalCharterText(ctx context.Context) string {
	g, _ := d.globalLevel(ctx)
	return protocol.GlobalCharterText(g)
}

// orGlobalCharter gives a charter text that was built from a node (a worker
// brief, a new Codex thread) the global level when no node covers the project.
// Text from a node already holds the global rules and refuses.
func (d *Daemon) orGlobalCharter(ctx context.Context, shown string) string {
	if shown != "" {
		return shown
	}
	return d.globalCharterText(ctx)
}

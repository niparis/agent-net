package daemon

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/niparis/agent-net/internal/authority"
	"github.com/niparis/agent-net/internal/protocol"
)

// tool_decide (decision 0166). A harness hook asks "may this tool call run
// without asking the owner". The daemon answers allow from the scopes of the
// role node the session speaks for, or gives no decision. It never answers
// block, and every failure is "no decision": the harness then behaves exactly
// as it did before.

// scopeEntry is the scopes the gateway last gave for a session, kept the way
// the turn policy is (policyTTL, one charter epoch, policyStale when the
// gateway cannot be reached). It is apart from the turn policy cache: that read
// can hand out a one-time charter draft request, and this one must not take it.
type scopeEntry struct {
	at    time.Time
	epoch uint64
	held  authority.Held
}

type scopeCache struct {
	mu    sync.Mutex
	cache map[string]scopeEntry
}

func (d *Daemon) scopesOf(ctx context.Context, sid string) (authority.Held, bool) {
	now := d.policy.clock()
	epoch := d.charterEpoch.Load()
	c := &d.scopes
	c.mu.Lock()
	e, have := c.cache[sid]
	c.mu.Unlock()
	if have && e.epoch == epoch && now.Sub(e.at) < policyTTL {
		return e.held, true
	}
	cctx, cancel := context.WithTimeout(ctx, policyCallTimeout)
	defer cancel()
	raw, err := d.call(cctx, "policy.get", protocol.PolicyGet{SessionID: sid})
	var got protocol.PolicyGot
	if err == nil {
		err = json.Unmarshal(raw, &got)
	}
	if err != nil {
		if have && now.Sub(e.at) < policyStale {
			return e.held, true
		}
		return authority.Held{}, false
	}
	held := authority.Held{Scopes: got.Scopes, Repos: got.ScopeRepos}
	c.mu.Lock()
	if c.cache == nil {
		c.cache = map[string]scopeEntry{}
	}
	c.cache[sid] = scopeEntry{at: now, epoch: epoch, held: held}
	c.mu.Unlock()
	return held, true
}

// toolDecideCall serves MethodToolDecide. Like team_lead, a plugin or MCP client
// is bound to its session and the command line names the harness and native
// ID. A call from a session the daemon does not know is silent.
func (lc *localConn) toolDecideCall(ctx context.Context, client string, ls *localSession, raw json.RawMessage) (any, error) {
	var p protocol.ToolDecideRequest
	if json.Unmarshal(raw, &p) != nil || p.Tool == "" {
		return protocol.ToolDecideResult{}, nil
	}
	d := lc.d
	if client == "cli" {
		d.mu.Lock()
		ls = d.sessions[key(p.Harness, p.NativeID)]
		d.mu.Unlock()
		if ls == nil || p.NativeID == "" {
			return protocol.ToolDecideResult{}, nil
		}
	} else if ls == nil {
		return protocol.ToolDecideResult{}, nil
	}
	sid := d.currentSessionID(ls)
	if sid == "" {
		return protocol.ToolDecideResult{}, nil
	}
	folder := folderOr(p.Folder, d.infoOf(ls).Cwd)
	if !d.toolAllowed(ctx, sid, p.Tool, p.Input, folder) {
		return protocol.ToolDecideResult{}, nil
	}
	if p.RequestID != "" {
		// OpenCode: the harness cannot be told "allow" in a hook, so answer the
		// request itself. A reply that fails leaves the request as it was.
		if err := d.replyOpenCodePermission(ctx, ls, p.RequestSession, p.RequestID, folder); err != nil {
			d.Logf("tool_decide: OpenCode permission %s not answered: %v", p.RequestID, err)
			return protocol.ToolDecideResult{}, nil
		}
	}
	return protocol.ToolDecideResult{Allow: true, Reason: "delegated by the owner through a scope of the session's role"}, nil
}

func folderOr(folder, fallback string) string {
	if folder != "" {
		return folder
	}
	return fallback
}

// toolAllowed is the one decision every harness path asks: the Claude hook
// through tool_decide, and the Codex and OpenCode adapters directly. It is true
// only when the scopes of the role node of session sid allow the call. Every
// other case, including a failure, is false: no decision.
func (d *Daemon) toolAllowed(ctx context.Context, sid, tool string, input json.RawMessage, folder string) bool {
	held, ok := d.scopesOf(ctx, sid)
	if !ok || len(held.Scopes) == 0 {
		return false
	}
	var repo string
	if folder != "" {
		repo = d.repo.Lookup(folder).Project
	}
	return authority.Allow(held, authority.Call{Tool: tool, Input: input, Repo: repo})
}

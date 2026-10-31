package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/niparis/agent-net/internal/claudeagents"
	"github.com/niparis/agent-net/internal/protocol"
)

// Delegation policy (decision 0014). A harness adapter asks for the effective
// policy of its session on every turn (MethodPolicy). The daemon asks the
// gateway (policy.get) and keeps the answer for policyTTL, so a turn costs one
// local call, and a change by the owner shows within a few seconds.

const (
	policyTTL = 2 * time.Second
	// policyStale is how long the last good answer is used when the gateway
	// cannot be reached, so a gateway restart does not drop the policy.
	policyStale = 10 * time.Minute
)

type policyEntry struct {
	at  time.Time
	got protocol.PolicyGot
}

type policyState struct {
	mu    sync.Mutex
	cache map[string]policyEntry
	// now is replaced in tests.
	now func() time.Time
}

func (p *policyState) clock() time.Time {
	if p.now != nil {
		return p.now()
	}
	return time.Now()
}

func (p *policyState) forget(sid string) {
	p.mu.Lock()
	delete(p.cache, sid)
	p.mu.Unlock()
}

// policyOf returns the gateway's answer for a session of this machine.
func (d *Daemon) policyOf(ctx context.Context, sid string) (protocol.PolicyGot, bool) {
	p := &d.policy
	now := p.clock()
	p.mu.Lock()
	e, have := p.cache[sid]
	p.mu.Unlock()
	if have && now.Sub(e.at) < policyTTL {
		return e.got, true
	}
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	raw, err := d.call(cctx, "policy.get", protocol.PolicyGet{SessionID: sid})
	var got protocol.PolicyGot
	if err == nil {
		err = json.Unmarshal(raw, &got)
	}
	if err == nil {
		// The node's charter travels with the policy answer, so a harness hook
		// asks once per turn (decision 0022).
		got.Charter = d.charterContext(ctx, got.Project)
	}
	if err != nil {
		if have && now.Sub(e.at) < policyStale {
			return e.got, true
		}
		return got, false
	}
	p.mu.Lock()
	if p.cache == nil {
		p.cache = map[string]policyEntry{}
	}
	p.cache[sid] = policyEntry{at: now, got: got}
	p.mu.Unlock()
	return got, true
}

// PolicyHookState is the file where setup records that the owner agreed to the
// Claude Code policy hook (internal/setup/policyhook.go writes it).
func PolicyHookStatePath() string { return filepath.Join(ConfigDir(), "claude-policy-hook.json") }

// policyHookInstalled tells whether setup added the Claude Code policy hook.
func policyHookInstalled() bool {
	b, err := os.ReadFile(PolicyHookStatePath())
	if err != nil {
		return false
	}
	var st struct {
		Installed bool `json:"installed"`
	}
	return json.Unmarshal(b, &st) == nil && st.Installed
}

// validName reports whether a model name from the gateway holds only name
// characters. A name that does not is not put into text for the model.
func validName(s string) bool {
	_, err := protocol.DelegationPolicy{CoderModel: s}.Clean()
	return err == nil
}

// policyResult turns the gateway's answer into what the adapter needs. The
// adapter of a harness without native subagents gets the workers text.
func (d *Daemon) policyResult(harness string, got protocol.PolicyGot) protocol.PolicyResult {
	res := protocol.PolicyResult{Charter: got.Charter}
	v := got.Delegation
	if v == nil || !protocol.SupportFor(harness).Applied {
		return res
	}
	if _, err := v.DelegationPolicy.Clean(); err != nil || (v.CoderResolved != "" && !validName(v.CoderResolved)) ||
		(v.MainResolved != "" && !validName(v.MainResolved)) {
		return res // bad data from the gateway: say nothing about the policy
	}
	view := *v
	if view.Delegate == "" {
		// Only a main model is set.
		res.Main = mainFor(harness, view)
		return res
	}
	mode := protocol.EffectiveMode(harness, view.Delegate)
	res.Mode, res.Main = mode, mainFor(harness, view)
	if mode == protocol.DelegateNative {
		res.Coder = view.Coder()
		res.AgentID = protocol.CoderAgentID(res.Coder)
		if harness == "claude" {
			// Claude Code takes subagents from files. The file is written
			// here, on this machine, because the owner set the policy and
			// installed the hook (decision 0014).
			if _, err := claudeagents.Ensure(res.AgentID, res.Coder); err != nil {
				d.Logf("policy: claude subagent file: %v", err)
				mode = protocol.DelegateWorkers // no subagent to name: use workers
				res.Mode, res.AgentID = mode, ""
				view.Delegate = protocol.DelegateWorkers
			}
		}
	}
	res.Text = protocol.PolicyText(view, harness, res.AgentID)
	return res
}

func mainFor(harness string, v protocol.DelegationView) string {
	if !protocol.SupportFor(harness).MainModel {
		return ""
	}
	return v.Main()
}

// policyCall serves MethodPolicy. A plugin or MCP client asks for its own
// session. The command line (the Claude Code hook) names the session it runs
// for; it can only read the policy of a session of this daemon.
func (lc *localConn) policyCall(ctx context.Context, client string, ls *localSession, raw json.RawMessage) (any, error) {
	var p protocol.PolicyRequest
	if json.Unmarshal(raw, &p) != nil {
		return nil, errors.New("bad policy params")
	}
	d := lc.d
	if client == "cli" {
		d.mu.Lock()
		ls = d.sessions[key(p.Harness, p.NativeID)]
		d.mu.Unlock()
		if ls == nil || p.NativeID == "" {
			return protocol.PolicyResult{}, nil // not a session of this daemon: silent
		}
	} else if ls == nil {
		return nil, errors.New("this method needs a bound session")
	}
	sid := d.currentSessionID(ls)
	if sid == "" {
		return protocol.PolicyResult{}, nil
	}
	if client == "cli" && p.Harness == "claude" {
		_, _ = d.call(ctx, "signal.prompt", map[string]string{"session_id": sid, "prompt": p.Prompt})
	}
	got, ok := d.policyOf(ctx, sid)
	if !ok {
		return protocol.PolicyResult{}, nil
	}
	return d.policyResult(d.infoOf(ls).Harness, got), nil
}

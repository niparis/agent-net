package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/redact"
)

// This file is the owner-only data: the recent conversation of a session, and
// the CPU and memory figures asked for on request. Decision 0007 and
// docs/security.md say who may ask:
//
//   - only the owner's CLI or TUI, which says "cli" in hello. A bound agent
//     connection (Claude Code MCP, Pi, OpenCode) is always refused;
//   - and not a CLI that runs inside an agent session. The daemon looks at the
//     parents of the calling process and refuses it when one of them is a
//     local session's process. An agent that runs `agent-net tui` from its
//     shell tool is stopped here.
//
// Another machine's owner asks through the gateway, which relays the request
// to the machine that holds the session. The text is masked and cut on the
// machine that holds it, it passes through the gateway without being stored or
// logged, and it never goes into the events table.

// operatorMethods are the methods that return owner-only data.
var operatorMethods = map[string]bool{
	"usage": true, "usage_top": true, "session_text": true, "counts": true, "session_enter": true,
	protocol.OpSessionTrust: true, protocol.OpSessionPolicy: true, protocol.OpNodeRename: true,
	protocol.OpIntegrationsGet: true, protocol.OpIntegrationsSet: true,
}

// The owner as a sender (owner_send, and the command line's worker calls) uses
// the same gate, operatorGate, but is served by dispatchActions (operator.go).

// TextProvider is implemented by an adapter that can read the conversation of
// its sessions itself (Claude Code reads the transcript file).
type TextProvider interface {
	Text(ctx context.Context, info protocol.SessionInfo, q protocol.TextQuery) (protocol.TextPage, error)
}

// textState holds the requests the daemon has sent to plugins and waits for.
type textState struct {
	mu   sync.Mutex
	wait map[string]chan textResult
}

type textResult struct {
	page protocol.TextPage
	err  string
}

func (t *textState) register(id string) chan textResult {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.wait == nil {
		t.wait = map[string]chan textResult{}
	}
	ch := make(chan textResult, 1)
	t.wait[id] = ch
	return ch
}

func (t *textState) drop(id string) {
	t.mu.Lock()
	delete(t.wait, id)
	t.mu.Unlock()
}

func (t *textState) deliver(id string, r textResult) {
	t.mu.Lock()
	ch := t.wait[id]
	t.mu.Unlock()
	if ch != nil {
		select {
		case ch <- r:
		default:
		}
	}
}

// operatorGate checks that a method that returns owner-only data may be
// called on this connection.
func (lc *localConn) operatorGate(method string) error {
	lc.mu.Lock()
	client, pid := lc.client, lc.peerPID
	lc.mu.Unlock()
	if client != "cli" {
		return fmt.Errorf("method %q is only available to the owner's command line or TUI, not to agent sessions", method)
	}
	if lc.d.inSession(pid) {
		return fmt.Errorf("method %q is not available from inside an agent session: run it in your own terminal. %s", method, insideSessionAdvice(method))
	}
	return nil
}

// insideSessionAdvice tells an agent that ran an owner-only command what to
// use instead. An agent that lacks the tool runs old agent-net tools.
func insideSessionAdvice(method string) string {
	const reconnect = "ask your user to reconnect agent-net (in Claude Code: /mcp)."
	if method == protocol.OpWorkerStart {
		return "agents start workers with the agent_start tool, not this command. If you do not have agent_start, your agent-net tools are out of date: " + reconnect
	}
	return "agents use the agent-net tools, not this command. If a tool you need is missing, your agent-net tools are out of date: " + reconnect
}

// insideSession tells whether pid is a local session's process or a descendant
// of one. It walks the parents of pid. A process that detaches itself from its
// parent (a double fork) is not caught; this is a barrier against an agent that
// simply runs the command line, not against a program that tries to hide.
func (d *Daemon) insideSession(pid int) bool {
	if pid <= 0 {
		return false
	}
	d.mu.Lock()
	sess := map[int]bool{}
	for _, ls := range d.sessions {
		if ls.info.PID > 1 {
			sess[ls.info.PID] = true
		}
	}
	d.mu.Unlock()
	// A worker is a session, but it is registered only when its plugin says
	// hello. Count its process from the moment it starts.
	d.wmu.Lock()
	for _, w := range d.workers {
		if w.pid > 1 {
			sess[w.pid] = true
		}
	}
	d.wmu.Unlock()
	if len(sess) == 0 {
		return false
	}
	for p, depth := pid, 0; p > 1 && depth < 64; depth++ {
		if sess[p] {
			return true
		}
		parent, err := parentPID(p)
		if err != nil {
			return false
		}
		p = parent
	}
	return false
}

// dispatchOperator serves the owner-only methods. ok is false for any other method.
func (lc *localConn) dispatchOperator(ctx context.Context, method string, raw json.RawMessage) (res any, ok bool, err error) {
	if !operatorMethods[method] {
		return nil, false, nil
	}
	if err := lc.operatorGate(method); err != nil {
		return nil, true, err
	}
	d := lc.d
	switch method {
	case "usage":
		res, err = d.localUsage(ctx)
	case "counts":
		var data json.RawMessage
		if data, err = d.call(ctx, "query.counts", struct{}{}); err == nil {
			res = data
		}
	case "usage_top":
		var p protocol.QueryUsageTop
		if json.Unmarshal(raw, &p) != nil || p.SessionID == "" {
			return nil, true, errors.New("usage_top needs session_id")
		}
		if d.sessionByID(p.SessionID) != nil {
			res, err = d.localUsageTop(ctx, p.SessionID)
		} else {
			var data json.RawMessage
			if data, err = d.call(ctx, "query.usage_top", p); err == nil {
				res = data
			}
		}
	case protocol.OpSessionTrust:
		// Only the owner at the keyboard may change what a session does on a
		// peer's request: the gate above refuses agent sessions, and no agent
		// tool exists for it (decision 0012).
		var q protocol.SessionTrustRequest
		if json.Unmarshal(raw, &q) != nil || q.SessionID == "" {
			return nil, true, errors.New("session_trust needs session_id")
		}
		if _, err = d.call(ctx, "session.trust", protocol.SessionTrust{SessionID: q.SessionID, On: q.On}); err == nil {
			res = protocol.SessionTrustResult{SessionID: q.SessionID, On: q.On}
			d.Logf("session.trust session=%s on=%t", q.SessionID, q.On)
		}
	case protocol.OpNodeRename:
		// The name appears in every machine's lists, hints and plans: only the
		// owner at the keyboard sets it (decision 0015). The gate above refuses
		// agent sessions, and no agent tool exists for it.
		var q protocol.NodeRenameRequest
		if json.Unmarshal(raw, &q) != nil || q.NodeID == "" {
			return nil, true, errors.New("node_rename needs node_id")
		}
		var data json.RawMessage
		if data, err = d.call(ctx, "node.rename", protocol.NodeRename{NodeID: q.NodeID, Name: q.Name}); err == nil {
			var out protocol.NodeRenameResult
			if err = json.Unmarshal(data, &out); err == nil {
				res = out
				if q.NodeID == d.cfg.NodeID && out.Hostname != "" {
					d.shownHost.Store(out.Hostname)
				}
				d.Logf("node.rename node=%s name=%q", q.NodeID, out.DisplayName)
			}
		}
	case protocol.OpSessionPolicy:
		// The policy is owner text that goes into the model's context every
		// turn: only the owner at the keyboard sets it (decision 0014). The
		// gate above refuses agent sessions, and no agent tool exists for it.
		var q protocol.SessionPolicyRequest
		if json.Unmarshal(raw, &q) != nil || q.SessionID == "" {
			return nil, true, errors.New("session_policy needs session_id")
		}
		var data json.RawMessage
		if data, err = d.call(ctx, "session.policy", q); err == nil {
			var out protocol.SessionPolicyResult
			if err = json.Unmarshal(data, &out); err == nil {
				res = out
				d.policy.forget(q.SessionID)
				if out.Saved {
					d.Logf("session.policy session=%s scope=%s delegate=%s", q.SessionID, q.Scope, q.Policy.Delegate)
				}
			}
		}
	case protocol.OpIntegrationsGet:
		// The answers decide which integrations run with the owner's consent:
		// only the owner at the keyboard reads or changes them (decision 0021).
		res, err = d.integrationsGet(ctx)
		if err == nil {
			d.Logf("integrations.get")
		}
	case protocol.OpIntegrationsSet:
		var in protocol.Integrations
		if json.Unmarshal(raw, &in) != nil {
			return nil, true, errors.New("integrations_set needs the six answers")
		}
		res, err = d.integrationsSet(ctx, in)
		if err == nil {
			d.Logf("integrations.set on=%d", in.CountOn())
		}
	case "session_enter":
		var q protocol.EnterRequest
		if json.Unmarshal(raw, &q) != nil || q.SessionID == "" {
			return nil, true, errors.New("session_enter needs session_id")
		}
		res, err = d.enterPlan(ctx, q)
	case "session_text":
		var q protocol.TextQuery
		if json.Unmarshal(raw, &q) != nil || q.SessionID == "" {
			return nil, true, errors.New("session_text needs session_id")
		}
		if d.sessionByID(q.SessionID) != nil {
			res, err = d.localText(ctx, q)
		} else {
			var data json.RawMessage
			if data, err = d.call(ctx, "query.session_text", q); err == nil {
				// The other machine masked and cut the text. Do it again here:
				// that machine may run an older or a broken agent-net.
				var page protocol.TextPage
				if err = json.Unmarshal(data, &page); err == nil {
					res = sanitizePage(page, q)
				}
			}
		}
	}
	if err != nil && strings.Contains(err.Error(), "unknown frame type") {
		err = errors.New("the gateway is too old for this (upgrade the gateway with agent-net upgrade)")
	}
	return res, true, err
}

// localText reads the conversation of a session on this machine.
func (d *Daemon) localText(ctx context.Context, q protocol.TextQuery) (protocol.TextPage, error) {
	ls := d.sessionByID(q.SessionID)
	if ls == nil {
		return protocol.TextPage{}, errors.New("no such session on this machine")
	}
	info := d.infoOf(ls)
	q.Limit = clampLimit(q.Limit)
	var page protocol.TextPage
	var err error
	d.mu.Lock()
	conn := ls.conn
	d.mu.Unlock()
	switch {
	case conn != nil:
		page, err = conn.askText(ctx, q, d.textTimeout())
	default:
		tp, ok := d.adapters[info.Harness].(TextProvider)
		if !ok {
			return protocol.TextPage{}, fmt.Errorf("reading the conversation is not supported for %s sessions", info.Harness)
		}
		page, err = tp.Text(ctx, info, q)
	}
	if err != nil {
		return protocol.TextPage{}, err
	}
	page = sanitizePage(page, q)
	page.SessionID, page.Harness = q.SessionID, info.Harness
	return page, nil
}

func (d *Daemon) textTimeout() time.Duration {
	if d.cfg.TextTimeout > 0 {
		return d.cfg.TextTimeout
	}
	return 8 * time.Second
}

func clampLimit(n int) int {
	switch {
	case n <= 0:
		return protocol.TextDefaultLimit
	case n > protocol.TextMaxLimit:
		return protocol.TextMaxLimit
	}
	return n
}

// sanitizePage enforces the rules on what a harness returned: only known
// roles, secrets masked, control characters removed, text cut, at most the
// asked number of entries (the newest ones). The harness cannot widen them.
func sanitizePage(p protocol.TextPage, q protocol.TextQuery) protocol.TextPage {
	limit := clampLimit(q.Limit)
	out := protocol.TextPage{Older: p.Older, Newer: p.Newer, Entries: make([]protocol.TextEntry, 0, min(len(p.Entries), limit))}
	for _, e := range p.Entries {
		if !protocol.ValidRole(e.Role) {
			continue
		}
		e.Text, e.Cut = cleanText(e.Text, e.Cut)
		e.Tool = redact.Cut(cleanLine(e.Tool), 80)
		e.Cursor = redact.Cut(e.Cursor, 200)
		out.Entries = append(out.Entries, e)
	}
	if len(out.Entries) > limit {
		out.Entries = out.Entries[len(out.Entries)-limit:]
		if q.After == "" {
			out.Older = out.Entries[0].Cursor
		}
	}
	out.Older, out.Newer = redact.Cut(out.Older, 200), redact.Cut(out.Newer, 200)
	return out
}

// cleanText masks secrets, removes control characters (keeping newlines and
// tabs) and cuts the text to protocol.TextMaxEntry characters.
func cleanText(s string, wasCut bool) (string, bool) {
	s = redact.Secrets(s)
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	if len([]rune(s)) > protocol.TextMaxEntry {
		return redact.Cut(s, protocol.TextMaxEntry), true
	}
	return s, wasCut
}

func cleanLine(s string) string {
	return strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)), " ")
}

// askText asks the plugin behind this connection for a page of the session and
// waits for the answer. A plugin from before this feature ignores the request,
// so the wait ends with a hint.
func (lc *localConn) askText(ctx context.Context, q protocol.TextQuery, wait time.Duration) (protocol.TextPage, error) {
	id := protocol.NewID("t")
	ch := lc.tx.register(id)
	defer lc.tx.drop(id)
	params := map[string]any{"req": id, "limit": q.Limit}
	if q.Before != "" {
		params["before"] = q.Before
	}
	if q.After != "" {
		params["after"] = q.After
	}
	if err := lc.writeJSON(map[string]any{"method": "session_text", "params": params}); err != nil {
		return protocol.TextPage{}, errors.New("the plugin connection is lost")
	}
	select {
	case r := <-ch:
		if r.err != "" {
			return protocol.TextPage{}, errors.New(r.err)
		}
		return r.page, nil
	case <-time.After(wait):
		return protocol.TextPage{}, errors.New("the plugin did not answer; it may be from an older agent-net (Pi: run /reload in the session, OpenCode: it reloads the plugin file by itself)")
	case <-lc.closed:
		return protocol.TextPage{}, errors.New("the plugin connection closed")
	case <-ctx.Done():
		return protocol.TextPage{}, ctx.Err()
	}
}

// textResult handles a plugin's answer to a session_text push.
func (lc *localConn) textResult(raw json.RawMessage) {
	var p struct {
		Req   string            `json:"req"`
		OK    bool              `json:"ok"`
		Error string            `json:"error"`
		Page  protocol.TextPage `json:"page"`
	}
	if json.Unmarshal(raw, &p) != nil || p.Req == "" {
		return
	}
	r := textResult{page: p.Page}
	if !p.OK {
		r.err = redact.Cut(cleanLine(p.Error), 200)
		if r.err == "" {
			r.err = "the plugin could not read the session"
		}
	}
	lc.tx.deliver(p.Req, r)
}

// ---- requests from the gateway ----

// onOperatorFrame answers a request the gateway relays from another machine's
// owner: node.usage_top and node.session_text.
func (d *Daemon) onOperatorFrame(ctx context.Context, l *link, typ, id string, raw []byte) {
	var data any
	var err error
	switch typ {
	case "node.usage_top":
		var p protocol.QueryUsageTop
		if json.Unmarshal(raw, &p) != nil || p.SessionID == "" {
			err = errors.New("bad node.usage_top")
		} else {
			data, err = d.localUsageTop(ctx, p.SessionID)
		}
	case "node.session_enter":
		var q protocol.EnterRequest
		if json.Unmarshal(raw, &q) != nil || q.SessionID == "" {
			err = errors.New("bad node.session_enter")
		} else {
			q.TmuxSocket, q.HerdrSocket = "", "" // the asker's terminal means nothing here
			data, err = d.localEnter(ctx, q, true)
		}
	case "node.session_text":
		var q protocol.TextQuery
		if json.Unmarshal(raw, &q) != nil || q.SessionID == "" {
			err = errors.New("bad node.session_text")
		} else {
			data, err = d.localText(ctx, q)
		}
	}
	if frame, e := protocol.EncodeResult(id, data, err); e == nil {
		l.write(ctx, frame)
	}
}

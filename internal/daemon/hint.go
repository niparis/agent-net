package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"time"

	"github.com/niparis/agent-net/internal/hint"
	"github.com/niparis/agent-net/internal/protocol"
)

// "Who can help" hints (decision 0012). A harness sends the error text of a
// failed tool call; the daemon matches it against the other online sessions
// (internal/hint) and answers with at most two suggestions. The limits keep it
// from nagging: the same suggestion for the same session at most once per
// hintRepeat, and any hint for a session at most once per hintGap.
const (
	hintRepeat   = 30 * time.Minute
	hintGap      = 45 * time.Second
	agentsMaxAge = 20 * time.Second
	// hintFetchWait is how long a hint waits for a fresh session list. The
	// Claude Code hook has a budget of a few hundred milliseconds.
	hintFetchWait = 250 * time.Millisecond
)

type hintState struct {
	mu       sync.Mutex
	last     map[string]time.Time // self|candidate -> when it was suggested
	lastSess map[string]time.Time // self -> when it got any hint
	agents   []protocol.AgentView
	agentsAt time.Time
	// now is replaced in tests.
	now func() time.Time
}

func (h *hintState) clock() time.Time {
	if h.now != nil {
		return h.now()
	}
	return time.Now()
}

// hintAgents returns the online sessions, from a cache up to agentsMaxAge old.
func (d *Daemon) hintAgents(ctx context.Context) []protocol.AgentView {
	h := &d.hints
	h.mu.Lock()
	fresh := h.agents != nil && h.clock().Sub(h.agentsAt) < agentsMaxAge
	cached := h.agents
	h.mu.Unlock()
	if fresh {
		return cached
	}
	cctx, cancel := context.WithTimeout(ctx, hintFetchWait)
	defer cancel()
	data, err := d.call(cctx, "query.agents", protocol.QueryAgents{OnlineOnly: true})
	if err != nil {
		return cached // stale is better than nothing
	}
	var r protocol.AgentsResult
	if json.Unmarshal(data, &r) != nil {
		return cached
	}
	h.mu.Lock()
	h.agents, h.agentsAt = r.Agents, h.clock()
	h.mu.Unlock()
	return r.Agents
}

func candidatesOf(agents []protocol.AgentView) []hint.Candidate {
	out := make([]hint.Candidate, 0, len(agents))
	for _, a := range agents {
		if a.Worker != nil { // a worker is a tool of its caller, not someone to ask
			continue
		}
		project := a.Repo.Project
		if project == "" {
			project = a.Profile.Project
		}
		out = append(out, hint.Candidate{SessionID: a.SessionID, Name: a.Profile.Name, Hostname: a.Hostname,
			Online: a.Online, Owner: protocol.IsOwner(a.Harness), Project: project, Remote: a.Repo.Remote,
			Folder: filepath.Base(a.Cwd), Function: a.Profile.Function, Task: a.Profile.Task,
			Expertise: a.Profile.Expertise})
	}
	return out
}

// hintFor works out the hint for a failure in the session self.
func (d *Daemon) hintFor(ctx context.Context, self *localSession, p protocol.HintRequest) protocol.HintResult {
	if !p.Failed && !hint.LooksLikeFailure(p.Text) {
		return protocol.HintResult{}
	}
	sid := d.currentSessionID(self)
	if sid == "" {
		return protocol.HintResult{}
	}
	info := d.infoOf(self)
	if info.Worker != nil { // a worker has its caller
		return protocol.HintResult{}
	}
	h := &d.hints
	now := h.clock()
	h.mu.Lock()
	if t, ok := h.lastSess[sid]; ok && now.Sub(t) < hintGap {
		h.mu.Unlock()
		return protocol.HintResult{}
	}
	h.mu.Unlock()
	agents := d.hintAgents(ctx)
	found := hint.Suggest(p.Text, hint.Self{SessionID: sid, Project: info.Repo.Project, Hostname: d.cfg.Hostname}, candidatesOf(agents))
	var keep []hint.Suggestion
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.last == nil {
		h.last, h.lastSess = map[string]time.Time{}, map[string]time.Time{}
	}
	for _, s := range found {
		k := sid + "|" + s.Candidate.SessionID
		if t, ok := h.last[k]; ok && now.Sub(t) < hintRepeat {
			continue
		}
		keep = append(keep, s)
	}
	if len(keep) == 0 {
		return protocol.HintResult{}
	}
	for _, s := range keep {
		h.last[sid+"|"+s.Candidate.SessionID] = now
	}
	h.lastSess[sid] = now
	sug := hint.ToProtocol(keep)
	return protocol.HintResult{Suggestions: sug, Text: protocol.HintText(sug)}
}

// hintCall serves MethodHint. A plugin or MCP client asks for its own session.
// The command line (the Claude Code hook) names the session it runs for; it
// can only ask for a hint, which changes nothing but the rate limit.
func (lc *localConn) hintCall(ctx context.Context, client string, ls *localSession, raw json.RawMessage) (any, error) {
	var p protocol.HintRequest
	if json.Unmarshal(raw, &p) != nil {
		return nil, errors.New("bad hint params")
	}
	d := lc.d
	if client == "cli" {
		d.mu.Lock()
		ls = d.sessions[key(p.Harness, p.NativeID)]
		d.mu.Unlock()
		if ls == nil || p.NativeID == "" {
			return protocol.HintResult{}, nil // not a session of this daemon: silent
		}
	} else if ls == nil {
		return nil, errors.New("this method needs a bound session")
	}
	return d.hintFor(ctx, ls, p), nil
}

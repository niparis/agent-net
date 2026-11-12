package daemon

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The tools loop runs after every wake (a catalog push, a project change, a
// new link) and every five minutes. Each run used to fetch the stored bundle
// of every copy target and report every result, though almost all of them had
// not changed: about two calls a second for a handful of skills.
//
// Now a run that is not a full check reuses the last result of a target whose
// revision and version are the same, and a result is reported only when the
// gateway has not got it yet on this link. A full check (the five-minute tick,
// a plan change, a project change, a new link, or toolsFullEvery without one)
// looks at every target as before, so a skill deleted by hand is still put
// back on time.
const toolsFullEvery = 5 * time.Minute

type toolsPassState struct {
	mu        sync.Mutex
	seen      map[string]toolsSeen
	fullAt    time.Time
	fullEpoch uint64
}

type toolsSeen struct {
	version  string
	result   protocol.ToolResult
	reported bool
	epoch    uint64
}

// toolsPass is one run of reconcileTools.
type toolsPass struct {
	d     *Daemon
	full  bool
	epoch uint64
}

func toolsKey(t protocol.ToolTarget) string {
	return t.Revision.Item + "\x00" + strconv.FormatInt(t.Revision.Revision, 10) + "\x00" + strconv.Itoa(t.Index)
}

// beginToolsPass decides whether this run checks every target.
func (d *Daemon) beginToolsPass(targets []protocol.ToolTarget) *toolsPass {
	d.mu.Lock()
	epoch := d.linkEpoch
	d.mu.Unlock()
	st := &d.toolsPass
	st.mu.Lock()
	defer st.mu.Unlock()
	requested := d.toolsFullNext.Swap(false)
	full := requested || st.seen == nil || st.fullEpoch != epoch || time.Since(st.fullAt) >= toolsFullEvery
	if full {
		st.fullAt, st.fullEpoch = time.Now(), epoch
		// Forget targets that are gone, so the map follows the plan.
		keep := map[string]toolsSeen{}
		for _, t := range targets {
			if v, ok := st.seen[toolsKey(t)]; ok {
				keep[toolsKey(t)] = v
			}
		}
		st.seen = keep
	}
	return &toolsPass{d: d, full: full, epoch: epoch}
}

// unchanged says whether a light run can skip the target: it was checked with
// the same version, and its result is already at the gateway. A result that
// did not get there is reported again.
func (p *toolsPass) unchanged(t protocol.ToolTarget) bool {
	if p.full {
		return false
	}
	st := &p.d.toolsPass
	st.mu.Lock()
	v, ok := st.seen[toolsKey(t)]
	st.mu.Unlock()
	if !ok || v.version != t.Revision.Version {
		return false
	}
	if v.reported && v.epoch == p.epoch {
		return true
	}
	p.send(context.Background(), t, v.result)
	return true
}

// report sends a checked result unless the gateway already has it.
func (p *toolsPass) report(ctx context.Context, t protocol.ToolTarget, result protocol.ToolResult) {
	st := &p.d.toolsPass
	st.mu.Lock()
	v, ok := st.seen[toolsKey(t)]
	st.mu.Unlock()
	if ok && v.reported && v.epoch == p.epoch && v.version == t.Revision.Version && v.result == result {
		return
	}
	p.send(ctx, t, result)
}

func (p *toolsPass) send(ctx context.Context, t protocol.ToolTarget, result protocol.ToolResult) {
	_, err := p.d.call(ctx, "tools.report", result)
	st := &p.d.toolsPass
	st.mu.Lock()
	if st.seen == nil {
		st.seen = map[string]toolsSeen{}
	}
	st.seen[toolsKey(t)] = toolsSeen{version: t.Revision.Version, result: result, reported: err == nil, epoch: p.epoch}
	st.mu.Unlock()
}

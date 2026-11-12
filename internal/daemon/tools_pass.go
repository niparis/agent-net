package daemon

import (
	"context"
	"encoding/json"
	"errors"
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
// revision and version are the same, and a result is reported only when it
// changed or its last report failed. A full check (the five-minute tick, a
// plan change, a project change, a new link, or toolsFullEvery without one)
// looks at every target as before, so a skill deleted by hand is still put
// back on time.
//
// A new link does not report the same results again: the gateway keeps them
// in its database (tool_results), not on the link, and drops them only with
// the item. Re-reporting every result on each reconnect sent thousands of
// tools.report calls an hour from a machine whose link dropped every few
// minutes.
const toolsFullEvery = 5 * time.Minute

type toolsPassState struct {
	mu        sync.Mutex
	seen      map[string]toolsSeen
	fullAt    time.Time
	fullEpoch uint64
	// bundles holds the stored bundle of each copy target by item and version.
	// A version is the hash of the content, so a bundle never changes: a full
	// check reads the destination against it and asks the gateway only for a
	// version it has not seen.
	bundles map[string][]byte
}

type toolsSeen struct {
	version  string
	result   protocol.ToolResult
	reported bool
}

// toolsPass is one run of reconcileTools.
type toolsPass struct {
	d    *Daemon
	full bool
}

func toolsKey(t protocol.ToolTarget) string {
	return t.Revision.Item + "\x00" + strconv.FormatInt(t.Revision.Revision, 10) + "\x00" + strconv.Itoa(t.Index)
}

func bundleKey(t protocol.ToolTarget) string {
	return t.Revision.Item + "\x00" + t.Revision.Version
}

// bundle returns the stored bundle of a copy target. It asks the gateway
// (tools.export) only when this version is not held yet.
func (p *toolsPass) bundle(ctx context.Context, t protocol.ToolTarget) ([]byte, error) {
	st := &p.d.toolsPass
	st.mu.Lock()
	b, ok := st.bundles[bundleKey(t)]
	st.mu.Unlock()
	if ok {
		return b, nil
	}
	raw, err := p.d.call(ctx, "tools.export", protocol.ToolVersionRequest{Item: t.Revision.Item, Version: t.Revision.Version})
	if err != nil {
		return nil, err
	}
	var sb protocol.SkillBundle
	if err := json.Unmarshal(raw, &sb); err != nil {
		return nil, errInvalidStoredBundle
	}
	st.mu.Lock()
	if st.bundles == nil {
		st.bundles = map[string][]byte{}
	}
	st.bundles[bundleKey(t)] = sb.Data
	st.mu.Unlock()
	return sb.Data, nil
}

// dropBundle forgets a bundle that did not install, so the next check asks
// the gateway again instead of repeating a bad copy.
func (p *toolsPass) dropBundle(t protocol.ToolTarget) {
	st := &p.d.toolsPass
	st.mu.Lock()
	delete(st.bundles, bundleKey(t))
	st.mu.Unlock()
}

var errInvalidStoredBundle = errors.New("invalid stored bundle")

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
		// Same for bundles: keep only those the plan still names.
		bundles := map[string][]byte{}
		for _, t := range targets {
			if b, ok := st.bundles[bundleKey(t)]; ok {
				bundles[bundleKey(t)] = b
			}
		}
		st.bundles = bundles
	}
	return &toolsPass{d: d, full: full}
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
	if v.reported {
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
	if ok && v.reported && v.version == t.Revision.Version && v.result == result {
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
	st.seen[toolsKey(t)] = toolsSeen{version: t.Revision.Version, result: result, reported: err == nil}
	st.mu.Unlock()
}

package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// panelIdle is how long the daemon keeps following an owner that nobody asked
// about. A Claude session's panel asks every 3 seconds, so an owner that is
// quiet this long has no panel any more.
const panelIdle = time.Minute

// workersPanelCache keeps the last gateway answer to the workers query of each
// Claude session (the query behind the workers panel) and serves it until the
// gateway says that session's rows changed.
//
// The gateway tells it with workers.changed pushes for the owners it was asked
// to watch (workers.watch). The agents topic would not do: it moves for every
// session write on the network, and the panel reads one owner's rows.
type workersPanelCache struct {
	mu     sync.Mutex
	link   *link
	noPush bool // the gateway does not know workers.watch: ask it every time
	owners map[string]*panelOwner
	pruned time.Time
}

type panelOwner struct {
	// rev is the newest revision the gateway told us for this owner.
	rev uint64
	// data is the last answer, and dataRev the revision it was read under:
	// the revision the gateway gave before the read, so a change that lands
	// during the read makes the answer out of date at once.
	data    json.RawMessage
	dataRev uint64
	hasData bool
	used    time.Time
}

// ownerAgents answers the workers query of one owner session: the gateway's
// agents of that owner, from the kept answer while the gateway has pushed no
// change of them. It asks the gateway, and keeps nothing, when the daemon has
// no way to be told of changes: no link, or a gateway that does not push them.
func (d *Daemon) ownerAgents(ctx context.Context, owner string) (json.RawMessage, error) {
	q := protocol.QueryAgents{IncludeOwners: true, WorkersOwnedBy: owner}
	d.mu.Lock()
	l := d.lk
	d.mu.Unlock()
	if l == nil {
		return d.call(ctx, "query.agents", q)
	}
	c := &d.panel
	now := time.Now()
	c.mu.Lock()
	if c.link != l {
		c.link, c.owners, c.noPush, c.pruned = l, nil, false, now
	}
	if c.noPush {
		c.mu.Unlock()
		return d.call(ctx, "query.agents", q)
	}
	o := c.owners[owner]
	if o != nil && o.hasData && o.dataRev == o.rev {
		o.used = now
		data := o.data
		resync := now.Sub(c.pruned) > panelIdle
		c.mu.Unlock()
		if resync {
			d.panelWatch(ctx, l, "")
		}
		return data, nil
	}
	c.mu.Unlock()

	// A miss: follow the owner first, so no change after the read is lost,
	// then read.
	rev, ok := d.panelWatch(ctx, l, owner)
	if !ok {
		return d.call(ctx, "query.agents", q)
	}
	data, err := d.call(ctx, "query.agents", q)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	if c.link == l {
		if o := c.owners[owner]; o != nil {
			o.data, o.dataRev, o.hasData, o.used = data, rev, true, time.Now()
		}
	}
	c.mu.Unlock()
	return data, nil
}

// panelWatch sends the gateway the owners to follow: the ones asked about in
// the last panelIdle, and add. It returns the revision of add (when it is not
// empty) and false when the gateway cannot push.
func (d *Daemon) panelWatch(ctx context.Context, l *link, add string) (uint64, bool) {
	c := &d.panel
	now := time.Now()
	c.mu.Lock()
	if c.link != l {
		c.mu.Unlock()
		return 0, false
	}
	if c.owners == nil {
		c.owners = map[string]*panelOwner{}
	}
	if add != "" && c.owners[add] == nil {
		c.owners[add] = &panelOwner{used: now}
	}
	if add != "" {
		c.owners[add].used = now
	}
	set := make([]string, 0, len(c.owners))
	for id, o := range c.owners {
		if id != add && now.Sub(o.used) > panelIdle {
			delete(c.owners, id)
			continue
		}
		set = append(set, id)
	}
	c.pruned = now
	c.mu.Unlock()

	raw, err := d.call(ctx, protocol.FrameWorkersWatch, protocol.WorkersWatch{Owners: set})
	if err != nil {
		if strings.Contains(err.Error(), "unknown frame type") {
			c.mu.Lock()
			if c.link == l {
				c.noPush = true
			}
			c.mu.Unlock()
		}
		return 0, false
	}
	var res protocol.WorkersWatchResult
	if json.Unmarshal(raw, &res) != nil {
		return 0, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.link != l {
		return 0, false
	}
	for id, rev := range res.Revs {
		if o := c.owners[id]; o != nil && rev > o.rev {
			o.rev = rev
		}
	}
	return res.Revs[add], add == "" || res.Revs[add] > 0
}

// workersChanged applies a workers.changed push of link l.
func (d *Daemon) workersChanged(l *link, ev protocol.WorkersChanged) {
	c := &d.panel
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.link != l {
		return
	}
	if o := c.owners[ev.Owner]; o != nil && ev.Rev > o.rev {
		o.rev = ev.Rev
	}
}

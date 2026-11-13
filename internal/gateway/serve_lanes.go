package gateway

import (
	"sync/atomic"

	"github.com/niparis/agent-net/internal/protocol"
)

// The read loop of a node connection only reads. A handler can wait for the
// database, for a lock or for another machine. While the loop waits, nothing
// reads the socket, so pings and pongs stop, the keepalive closes the link
// after its timeout, and the node connects again and syncs all its sessions.
// That adds load to a gateway that was already slow.
//
// So the loop hands each frame to one of three places:
//   - a goroutine of its own, for the frame types that are pure reads. They
//     share nothing with the order of other frames, and their queries run on
//     the store's read pool, not on the writer connection (decision 0110). A
//     read that starts after a write was answered sees it: the write has
//     committed, and a committed write is visible to every pool connection;
//   - the node's write lane, one goroutine that runs the other frames one at a
//     time in the order they arrived, so a node keeps the order of its writes;
//   - itself, for "hello" only. It sets what the next frames are checked
//     against, and the node has nothing else to ask before it is answered.

// laneDepth is how many frames may wait in a write lane. When the lane is
// full the read loop waits to add one, which stops reading this socket: the
// sender is slowed down by TCP, as it was when the loop ran every handler.
const laneDepth = 1024

// parallelFrames are the frame types that only read. Leave a type out when it
// writes or when a later frame may depend on it.
var parallelFrames = map[string]bool{
	"policy.get": true, "context.get": true,
	"query.agents": true, "query.agent": true, "query.projects": true, "query.nodes": true,
	"query.graph": true, "query.graph_talk": true, "graph.read": true, "graph.get": true,
	"query.team_lead": true,
	"query.events":    true, "query.worker_log": true, "query.outcome_inbox": true,
	"query.conversations": true, "query.conversation_messages": true,
	"query.messages": true, "query.message": true,
	"query.inventory": true, "query.mcp_import_candidates": true,
	"query.tools_items": true, "query.tools_item": true,
	"query.usage": true, "query.counts": true, "query.machine_get": true,
	"learned.list": true, "charter.drafts": true, "signal.list": true,
	"inbox.list": true, "asks.list": true, "sign_in.list": true, "query.sign_in": true, "desk.log": true, "held.list": true,
	"hidden.get": true, "settings.snapshot": true, "node.settings.get": true,
	"onboarding.get": true, "playbook.get": true,
	"cto.graph_latest": true, "cto.graph_history": true,
	"charter.history": true, "edge.history": true, "settings.history": true,
	"change.list": true, "change.get": true,
	"worker.recent_changes": true, "workers.watch": true,
	// The Desk reads these every refresh. They only read (the settings come
	// from the read cache), so one slow write in the lane must not hold them.
	"chief.get": true, "chief.sorts": true, "ask.history": true,
	// A failure report is a counter bump: it must not wait behind a write.
	protocol.FrameClientError: true,
}

// frameLane runs the write frames of one connection in arrival order.
type frameLane struct {
	tasks  chan func()
	closed atomic.Bool
}

func newFrameLane() *frameLane {
	l := &frameLane{tasks: make(chan func(), laneDepth)}
	go l.run()
	return l
}

func (l *frameLane) run() {
	for task := range l.tasks {
		// A frame that waited past the end of its connection was never
		// answered, so the node sends it again after it reconnects.
		if l.closed.Load() {
			continue
		}
		task()
	}
}

// submit adds a frame to the lane. It waits when the lane is full.
func (l *frameLane) submit(task func()) { l.tasks <- task }

// stop ends the lane. The frame that runs now finishes; the ones that wait are
// dropped.
func (l *frameLane) stop() {
	l.closed.Store(true)
	close(l.tasks)
}

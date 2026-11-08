package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// handleProcessStopRelay serves "session.stop_process": the owner's request to
// stop processes on another machine (decision 0094). Like a folder trust, the
// gateway checks that the caller is the owner session of its machine and asks
// the machine that runs the processes, which checks the owner flag again and
// applies its own refusals. The gateway keeps an event with the process IDs
// and the outcomes, not the command lines.
func (g *Gateway) handleProcessStopRelay(ctx context.Context, nc *nodeConn, raw []byte) (any, error) {
	var relay protocol.SessionProcessStopRelay
	if json.Unmarshal(raw, &relay) != nil {
		return nil, reject("bad %s", protocol.FrameSessionStopProcess)
	}
	if relay.Owner == "" {
		return nil, reject("a session relay needs the owner session")
	}
	owner, ok, err := g.store.Agent(relay.Owner)
	if err != nil {
		return nil, err
	}
	if !ok || owner.NodeID != nc.id || !protocol.IsOwner(owner.Harness) || owner.Worker != nil {
		return nil, reject("only the owner session of this machine may stop processes")
	}
	nodeID := strings.TrimSpace(relay.NodeID)
	if nodeID == "" {
		return nil, reject("stopping a process needs the machine that runs it")
	}
	if len(relay.PIDs) == 0 {
		return nil, reject("stopping a process needs at least one pid")
	}
	if nodeID, err = g.sessionRelayNode(nodeID); err != nil {
		return nil, err
	}
	host := g.store.hostname(nodeID)
	g.mu.Lock()
	target := g.conns[nodeID]
	g.mu.Unlock()
	if target == nil {
		return nil, reject("machine %s is offline", firstNonEmpty(host, nodeID))
	}
	version, err := g.store.NodeVersion(nodeID)
	if err != nil {
		version = ""
	}
	if !sessionRelaySupported(version) {
		return nil, reject("machine %s runs an older agent-net: upgrade it there", firstNonEmpty(host, nodeID))
	}
	frame := protocol.SessionOwnerProcessStop{Owner: relay.Owner, FromNode: nc.id, ProcessStopRequest: relay.ProcessStopRequest}
	frame.NodeID = nodeID
	cctx, cancel := context.WithTimeout(ctx, sessionRelayWait)
	defer cancel()
	data, err := target.call(cctx, protocol.FrameSessionOwnerStopProcess, frame)
	if err != nil {
		where := firstNonEmpty(host, nodeID)
		switch {
		case errors.Is(err, errNodeGone), errors.Is(err, context.DeadlineExceeded):
			return nil, reject("machine %s did not answer the process stop: %v", where, err)
		case strings.Contains(err.Error(), "unknown frame type"):
			return nil, reject("machine %s runs an older agent-net: upgrade it there", where)
		}
		return nil, reject("machine %s refused the process stop: %v", where, err)
	}
	var res protocol.ProcessStopResult
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, reject("machine %s answered without a result", firstNonEmpty(host, nodeID))
	}
	res.NodeID, res.Hostname = nodeID, firstNonEmpty(host, nodeID)
	var outcomes []string
	for _, p := range res.Processes {
		outcomes = append(outcomes, strconv.Itoa(p.PID)+"="+p.Outcome)
	}
	g.logLine("session.process_stop", "node", nodeID, "host", res.Hostname, "owner", relay.Owner, "from_node", nc.id, "processes", strings.Join(outcomes, ","))
	g.store.Event("session.process_stop", nodeID, "", "", strings.TrimPrefix(pairs("owner", relay.Owner, "from_node", nc.id, "processes", strings.Join(outcomes, ",")), " "))
	return res, nil
}

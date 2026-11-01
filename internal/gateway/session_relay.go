package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Owner sessions on another machine (docs/design/start-sessions.md, build plan
// part 8). The gateway never runs anything: it checks the caller, finds the
// machine that holds the project or session, and asks its daemon. The daemon
// checks the owner flag again against this gateway before it acts.

const (
	// sessionRelayWait is how long the gateway waits for the other machine to
	// answer a start, resume, stop or status question.
	sessionRelayWait = 60 * time.Second
	// sessionRelayMinVersion is the first agent-net that answers the
	// session.owner_* frames. "dev" builds are new enough.
	sessionRelayMinVersion = "0.4.46"
)

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func isSessionRelay(typ string) bool {
	switch typ {
	case protocol.FrameSessionStart, protocol.FrameSessionResume, protocol.FrameSessionStop, protocol.FrameSessionGet:
		return true
	}
	return false
}

// handleSessionRelay serves "session.start", "session.resume", "session.stop"
// and "session.get" from the owner's machine.
func (g *Gateway) handleSessionRelay(ctx context.Context, nc *nodeConn, typ string, raw []byte) (any, error) {
	var relay struct {
		protocol.SessionStartRequest
		Owner     string `json:"owner_session"`
		NodeID    string `json:"node_id,omitempty"`
		SessionID string `json:"session_id,omitempty"`
		StartID   string `json:"start_id,omitempty"`
	}
	if json.Unmarshal(raw, &relay) != nil {
		return nil, reject("bad %s", typ)
	}
	if relay.Owner == "" {
		return nil, reject("a session relay needs the owner session")
	}
	owner, ok, err := g.store.Agent(relay.Owner)
	if err != nil {
		return nil, err
	}
	if !ok || owner.NodeID != nc.id || !protocol.IsOwner(owner.Harness) || owner.Worker != nil {
		return nil, reject("only the owner session of this machine may start, resume or stop a session")
	}

	nodeID := strings.TrimSpace(relay.NodeID)
	if nodeID != "" {
		if nodeID, err = g.sessionRelayNode(nodeID); err != nil {
			return nil, err
		}
	}
	sessionID := strings.TrimSpace(relay.SessionID)
	switch typ {
	case protocol.FrameSessionStart:
		if nodeID == "" {
			if nodeID, err = g.sessionStartNode(relay.SessionStartRequest); err != nil {
				return nil, err
			}
		}
	case protocol.FrameSessionResume, protocol.FrameSessionStop:
		if sessionID == "" {
			return nil, reject("%s needs session_id", typ)
		}
		a, ok, err := g.store.Agent(sessionID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, reject("unknown session %q", sessionID)
		}
		nodeID = a.NodeID
	case protocol.FrameSessionGet:
		if relay.StartID == "" {
			return nil, reject("session.get needs id")
		}
		if nodeID == "" {
			return nil, reject("session.get needs the machine that holds the start")
		}
	}
	if nodeID == "" {
		return nil, reject("cannot tell which machine holds this session")
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

	frame := protocol.SessionOwnerRequest{Owner: relay.Owner, FromNode: nc.id,
		SessionStartRequest: relay.SessionStartRequest, SessionID: sessionID, StartID: relay.StartID}
	frameType := map[string]string{
		protocol.FrameSessionStart:  protocol.FrameSessionOwnerStart,
		protocol.FrameSessionResume: protocol.FrameSessionOwnerResume,
		protocol.FrameSessionStop:   protocol.FrameSessionOwnerStop,
		protocol.FrameSessionGet:    protocol.FrameSessionOwnerGet,
	}[typ]
	cctx, cancel := context.WithTimeout(ctx, sessionRelayWait)
	defer cancel()
	data, err := target.call(cctx, frameType, frame)
	if err != nil {
		where := firstNonEmpty(host, nodeID)
		switch {
		case errors.Is(err, errNodeGone), errors.Is(err, context.DeadlineExceeded):
			return nil, reject("machine %s did not answer the session request: %v", where, err)
		case strings.Contains(err.Error(), "unknown frame type"):
			return nil, reject("machine %s runs an older agent-net: upgrade it there", where)
		}
		return nil, reject("machine %s refused the session: %v", where, err)
	}
	switch typ {
	case protocol.FrameSessionStart, protocol.FrameSessionResume:
		var res protocol.StartedSession
		if err := json.Unmarshal(data, &res); err != nil {
			return nil, reject("machine %s answered without a start", firstNonEmpty(host, nodeID))
		}
		res.NodeID, res.Hostname = nodeID, firstNonEmpty(host, nodeID)
		g.logLine("session.started", "node", nodeID, "host", res.Hostname, "owner", relay.Owner,
			"from_node", nc.id, "harness", res.Harness, "project", res.Project)
		g.store.Event("session.started", nodeID, res.SessionID, "", strings.TrimPrefix(pairs(
			"owner", relay.Owner, "from_node", nc.id, "harness", res.Harness, "project", res.Project), " "))
		return res, nil
	case protocol.FrameSessionStop:
		var res protocol.SessionStopResult
		if err := json.Unmarshal(data, &res); err != nil {
			return nil, reject("machine %s answered without a stop", firstNonEmpty(host, nodeID))
		}
		g.logLine("session.stopped", "node", nodeID, "host", firstNonEmpty(host, nodeID),
			"owner", relay.Owner, "from_node", nc.id, "session", sessionID)
		g.store.Event("session.stopped", nodeID, sessionID, "", strings.TrimPrefix(pairs(
			"owner", relay.Owner, "from_node", nc.id), " "))
		return res, nil
	case protocol.FrameSessionGet:
		var res protocol.StartedSession
		if err := json.Unmarshal(data, &res); err != nil {
			return nil, reject("machine %s answered without a start", firstNonEmpty(host, nodeID))
		}
		res.NodeID, res.Hostname = nodeID, firstNonEmpty(host, nodeID)
		return res, nil
	}
	return nil, reject("unknown session relay %q", typ)
}

// sessionRelayNode accepts a node ID or a host name for a machine.
func (g *Gateway) sessionRelayNode(machine string) (string, error) {
	nodes, err := g.store.Nodes()
	if err != nil {
		return "", err
	}
	for _, n := range nodes {
		if n.NodeID == machine || strings.EqualFold(n.Hostname, machine) {
			return n.NodeID, nil
		}
	}
	return "", reject("no machine named %q", machine)
}

// sessionStartNode finds the one machine that holds a project.
func (g *Gateway) sessionStartNode(p protocol.SessionStartRequest) (string, error) {
	name := strings.TrimSpace(p.Project)
	if name == "" {
		return "", reject("session_start needs project")
	}
	projects, err := g.store.Projects("")
	if err != nil {
		return "", err
	}
	nodes := map[string]bool{}
	for _, v := range projects {
		hit := false
		if strings.HasPrefix(name, "/") || strings.HasPrefix(name, "~") {
			hit = v.Path == name
			for _, w := range v.Worktrees {
				hit = hit || w == name
			}
		} else {
			hit = strings.EqualFold(v.Name, name)
		}
		if hit {
			nodes[v.NodeID] = true
		}
	}
	switch len(nodes) {
	case 0:
		return "", reject("no project named %q is known on any machine", name)
	case 1:
		for node := range nodes {
			return node, nil
		}
	}
	return "", reject("project %q is on more than one machine: name the machine", name)
}

// sessionRelaySupported tells whether a machine's agent-net answers the
// session owner frames. A version older than the first release with them is
// refused, and so is a version this code cannot read.
func sessionRelaySupported(version string) bool {
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	if version == "dev" {
		return true
	}
	have, ok := parseReleaseVersion(version)
	if !ok {
		return false
	}
	want, _ := parseReleaseVersion(sessionRelayMinVersion)
	for i := range want {
		if have[i] != want[i] {
			return have[i] > want[i]
		}
	}
	return true
}

func parseReleaseVersion(v string) ([3]int, bool) {
	var out [3]int
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

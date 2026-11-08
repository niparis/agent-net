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
	case protocol.FrameSessionStart, protocol.FrameSessionProposalStart, protocol.FrameSessionProposalClone, protocol.FrameSessionResume, protocol.FrameSessionStop, protocol.FrameSessionGet:
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
	case protocol.FrameSessionStart, protocol.FrameSessionProposalStart, protocol.FrameSessionProposalClone:
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
	if relay.AgentID != "" && !target.sessionAgents.Load() {
		return nil, reject("target daemon does not support Agent selection; upgrade it before starting this session")
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
		protocol.FrameSessionStart:         protocol.FrameSessionOwnerStart,
		protocol.FrameSessionProposalStart: protocol.FrameSessionOwnerProposalStart,
		protocol.FrameSessionProposalClone: protocol.FrameSessionOwnerProposalClone,
		protocol.FrameSessionResume:        protocol.FrameSessionOwnerResume,
		protocol.FrameSessionStop:          protocol.FrameSessionOwnerStop,
		protocol.FrameSessionGet:           protocol.FrameSessionOwnerGet,
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
	case protocol.FrameSessionStart, protocol.FrameSessionProposalStart, protocol.FrameSessionProposalClone, protocol.FrameSessionResume:
		var res protocol.StartedSession
		if err := json.Unmarshal(data, &res); err != nil {
			return nil, reject("machine %s answered without a start", firstNonEmpty(host, nodeID))
		}
		res.NodeID, res.Hostname = nodeID, firstNonEmpty(host, nodeID)
		if res.Status == "failed" {
			return res, nil
		}
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

// folderRelayTarget runs the checks both folder relays share: the caller is
// the owner session of its machine, and the machine that holds the folder is
// online and new enough to serve relays. what names the action in refusals.
func (g *Gateway) folderRelayTarget(nc *nodeConn, ownerID, node, what string) (target *nodeConn, nodeID, host string, err error) {
	if ownerID == "" {
		return nil, "", "", reject("a session relay needs the owner session")
	}
	owner, ok, err := g.store.Agent(ownerID)
	if err != nil {
		return nil, "", "", err
	}
	if !ok || owner.NodeID != nc.id || !protocol.IsOwner(owner.Harness) || owner.Worker != nil {
		return nil, "", "", reject("only the owner session of this machine may %s", what)
	}
	nodeID = strings.TrimSpace(node)
	if nodeID == "" {
		return nil, "", "", reject("%s needs the machine that holds the folder", what)
	}
	if nodeID, err = g.sessionRelayNode(nodeID); err != nil {
		return nil, "", "", err
	}
	host = g.store.hostname(nodeID)
	g.mu.Lock()
	target = g.conns[nodeID]
	g.mu.Unlock()
	if target == nil {
		return nil, "", "", reject("machine %s is offline", firstNonEmpty(host, nodeID))
	}
	version, err := g.store.NodeVersion(nodeID)
	if err != nil {
		version = ""
	}
	if !sessionRelaySupported(version) {
		return nil, "", "", reject("machine %s runs an older agent-net: upgrade it there", firstNonEmpty(host, nodeID))
	}
	return target, nodeID, host, nil
}

// folderRelayCall asks the machine that holds the folder and turns its failures
// into refusals the owner can read. An older machine does not know the frame.
func folderRelayCall(ctx context.Context, target *nodeConn, frameType string, frame any, host, nodeID, what string) (json.RawMessage, error) {
	cctx, cancel := context.WithTimeout(ctx, sessionRelayWait)
	defer cancel()
	data, err := target.call(cctx, frameType, frame)
	if err != nil {
		where := firstNonEmpty(host, nodeID)
		switch {
		case errors.Is(err, errNodeGone), errors.Is(err, context.DeadlineExceeded):
			return nil, reject("machine %s did not answer the %s: %v", where, what, err)
		case strings.Contains(err.Error(), "unknown frame type"):
			return nil, reject("machine %s runs an older agent-net: upgrade it there", where)
		}
		return nil, reject("machine %s refused the %s: %v", where, what, err)
	}
	return data, nil
}

// handleFolderTrustRelay serves "session.trust_folder": the owner's request to
// trust a folder for Claude Code on another machine. Like a session start, the
// gateway checks that the caller is the owner session of its machine and asks
// the machine that holds the folder, which checks the owner flag again.
func (g *Gateway) handleFolderTrustRelay(ctx context.Context, nc *nodeConn, raw []byte) (any, error) {
	var relay protocol.SessionFolderTrustRelay
	if json.Unmarshal(raw, &relay) != nil {
		return nil, reject("bad %s", protocol.FrameSessionTrustFolder)
	}
	target, nodeID, host, err := g.folderRelayTarget(nc, relay.Owner, relay.NodeID, "trust a folder")
	if err != nil {
		return nil, err
	}
	frame := protocol.SessionOwnerFolderTrust{Owner: relay.Owner, FromNode: nc.id, FolderTrustRequest: relay.FolderTrustRequest}
	frame.NodeID = nodeID
	data, err := folderRelayCall(ctx, target, protocol.FrameSessionOwnerTrustFolder, frame, host, nodeID, "folder trust")
	if err != nil {
		return nil, err
	}
	var res protocol.FolderTrustResult
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, reject("machine %s answered without a result", firstNonEmpty(host, nodeID))
	}
	res.NodeID, res.Hostname = nodeID, firstNonEmpty(host, nodeID)
	g.logLine("session.folder_trust", "node", nodeID, "host", res.Hostname, "owner", relay.Owner, "from_node", nc.id, "remove", relay.Remove)
	g.store.Event("session.folder_trust", nodeID, "", "", strings.TrimPrefix(pairs("owner", relay.Owner, "from_node", nc.id), " "))
	return res, nil
}

// handleFolderMCPRelay serves "session.mcp_servers": the owner's choice for the
// .mcp.json servers of a folder on another machine. It has its own frames, so a
// gateway or machine that does not know them refuses and never runs a plain
// folder trust instead.
func (g *Gateway) handleFolderMCPRelay(ctx context.Context, nc *nodeConn, raw []byte) (any, error) {
	var relay protocol.SessionFolderMCPRelay
	if json.Unmarshal(raw, &relay) != nil {
		return nil, reject("bad %s", protocol.FrameSessionMCPServers)
	}
	target, nodeID, host, err := g.folderRelayTarget(nc, relay.Owner, relay.NodeID, "choose MCP servers")
	if err != nil {
		return nil, err
	}
	frame := protocol.SessionOwnerFolderMCP{Owner: relay.Owner, FromNode: nc.id, FolderMCPRequest: relay.FolderMCPRequest}
	frame.NodeID = nodeID
	data, err := folderRelayCall(ctx, target, protocol.FrameSessionOwnerMCPServers, frame, host, nodeID, "MCP server choice")
	if err != nil {
		return nil, err
	}
	var res protocol.FolderMCPResult
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, reject("machine %s answered without a result", firstNonEmpty(host, nodeID))
	}
	res.NodeID, res.Hostname = nodeID, firstNonEmpty(host, nodeID)
	g.logLine("session.folder_mcp", "node", nodeID, "host", res.Hostname, "owner", relay.Owner, "from_node", nc.id, "choice", res.Choice, "undo", relay.Undo != nil)
	g.store.Event("session.folder_mcp", nodeID, "", "", strings.TrimPrefix(pairs("owner", relay.Owner, "from_node", nc.id, "choice", res.Choice), " "))
	return res, nil
}

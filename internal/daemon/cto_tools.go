package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// ctoTool is the only entry to the private CTO channel. Check the process
// tree on every call, including after a server restart.
func (lc *localConn) ctoTool(ctx context.Context, raw json.RawMessage) (any, error) {
	lc.mu.Lock()
	pid := lc.peerPID
	client := lc.client
	lc.mu.Unlock()
	if client != "cto-mcp" || !lc.d.isCTOPeer(pid) {
		return nil, errors.New("CTO tool requires the CTO server's process tree")
	}
	var q struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if json.Unmarshal(raw, &q) != nil {
		return nil, errors.New("bad CTO tool request")
	}
	return lc.d.runCTOTool(ctx, q.Name, q.Arguments)
}

func (d *Daemon) runCTOToolRaw(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	var args map[string]any
	if len(raw) > 0 && string(raw) != "null" && json.Unmarshal(raw, &args) != nil {
		return nil, errors.New("bad CTO tool arguments")
	}
	if args == nil {
		args = map[string]any{}
	}
	switch name {
	case "graph_get", "charter_set", "charter_history", "node_add", "node_remove", "edge_set", "edge_remove", "edge_history", "graph_undo":
		delete(args, "by")
		if name == "graph_undo" {
			args["history_id"] = args["id"]
			delete(args, "id")
		}
		args["by"] = "cto"
		if name != "graph_get" && name != "charter_history" && name != "edge_history" {
			return d.ctoAttributedCall(ctx, strings.ReplaceAll(name, "_", "."), args)
		}
		return d.call(ctx, strings.ReplaceAll(name, "_", "."), args)
	case "agents":
		return d.call(ctx, "query.agents", args)
	case "feed":
		return d.call(ctx, "query.events", args)
	case "workers":
		return d.workersView(ctx, protocol.WorkersQuery{Recent: true})
	case "needs_you":
		data, err := d.call(ctx, "query.agents", protocol.QueryAgents{OnlineOnly: true})
		if err != nil {
			return nil, err
		}
		var a protocol.AgentsResult
		if err = json.Unmarshal(data, &a); err != nil {
			return nil, err
		}
		out := []protocol.AgentView{}
		for _, v := range a.Agents {
			if v.Waiting != nil {
				out = append(out, v)
			}
		}
		return protocol.AgentsResult{Agents: out}, nil
	case "send":
		var p struct {
			To   string `json:"to"`
			Body string `json:"body"`
		}
		if json.Unmarshal(raw, &p) != nil || p.To == "" || p.Body == "" {
			return nil, errors.New("send needs to and body")
		}
		sid := d.ownerSessionID()
		if sid == "" {
			return nil, errNotConnected
		}
		to, err := d.resolveTo(ctx, p.To)
		if err != nil {
			return nil, err
		}
		return d.send(ctx, protocol.MsgSend{Sender: sid, Recipient: to, Type: protocol.TypeRequest, Body: p.Body, FromCTO: true})
	case "worker_providers_set":
		var p protocol.WorkerProviders
		if json.Unmarshal(raw, &p) != nil {
			return nil, errors.New("bad worker providers")
		}
		return d.workerProviders(ctx, &p)
	case "score_set":
		var p protocol.ScoreSetRequest
		if json.Unmarshal(raw, &p) != nil {
			return nil, errors.New("bad score request")
		}
		return d.scores(ctx, &p)
	case "worker_start":
		var p protocol.WorkerStartRequest
		if json.Unmarshal(raw, &p) != nil {
			return nil, errors.New("bad worker request")
		}
		sid := d.ownerSessionID()
		if sid == "" {
			return nil, errNotConnected
		}
		return d.workerStartCall(ctx, sid, p, false, "", true)
	}
	return nil, errors.New("unknown CTO tool")
}

// Every tool call is recorded, including reads, no-ops and failures. Graph
// history supplies the separate before-and-after diff for a successful change.
func (d *Daemon) runCTOTool(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	result, err := d.runCTOToolRaw(ctx, name, raw)
	var a struct {
		To      string `json:"to"`
		Body    string `json:"body"`
		Project string `json:"project"`
		Task    string `json:"task"`
		Node    string `json:"node"`
		Attr    string `json:"attr"`
	}
	_ = json.Unmarshal(raw, &a)
	text := name
	if err != nil {
		text = fmt.Sprintf("failed %s: %v", name, err)
	} else {
		switch name {
		case "graph_get":
			text = "read graph"
		case "charter_history":
			text = "read charter history: " + a.Node
		case "edge_history":
			text = "read edge history: " + a.Node
		case "agents":
			text = "read agents"
		case "workers":
			text = "read workers"
		case "needs_you":
			text = "read needs you"
		case "feed":
			text = "read feed"
		case "charter_set":
			text = fmt.Sprintf("charter_set %s.%s", a.Node, a.Attr)
		case "send":
			text = fmt.Sprintf("sent to %s: %s", a.To, a.Body)
		case "worker_start":
			verb := "started"
			if r, ok := result.(*protocol.WorkerStartResult); ok && r.Status == "queued" {
				verb = "queued"
			}
			text = fmt.Sprintf("%s worker %s: %s", verb, a.Project, a.Task)
		}
	}
	logErr := d.recordCTOAction(text)
	if err != nil {
		return result, err
	}
	if logErr != nil {
		return result, logErr
	}
	return result, nil
}

// Send the capability check and the mutation on the same gateway link, so a
// reconnect to an older gateway cannot strip the attribution in between.
func (d *Daemon) ctoAttributedCall(ctx context.Context, typ string, params any) (json.RawMessage, error) {
	d.mu.Lock()
	l := d.lk
	d.mu.Unlock()
	if l == nil {
		return nil, errNotConnected
	}
	b, err := l.call(ctx, "cto.capabilities", nil)
	if err != nil {
		return nil, errors.New("gateway cannot verify CTO attribution; upgrade the gateway")
	}
	var caps struct {
		Attribution bool `json:"attribution"`
	}
	if json.Unmarshal(b, &caps) != nil || !caps.Attribution {
		return nil, errors.New("gateway cannot verify CTO attribution; upgrade the gateway")
	}
	return l.call(ctx, typ, params)
}

func (d *Daemon) recordCTOAction(text string) error {
	// Empty session IDs are the not-yet-started conversation. The selection and
	// insert are one statement so a concurrent first send cannot lose an action.
	_, err := d.st.db.Exec(`INSERT INTO cto_actions(session_id,text,at,harness) VALUES(COALESCE((SELECT CASE active_harness WHEN 'claude' THEN claude_session_id ELSE session_id END FROM cto WHERE id=1),''),?,?,COALESCE((SELECT active_harness FROM cto WHERE id=1),'opencode'))`, text, time.Now().UnixNano())
	return err
}

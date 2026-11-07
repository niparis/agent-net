package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// projectChange follows session creation: explicit machine selectors go through
// the gateway, while an omitted selector keeps the call on this daemon.
func (d *Daemon) projectChange(ctx context.Context, method string, raw json.RawMessage, preview bool) (any, error) {
	var selector struct {
		NodeID string `json:"node_id"`
		On     string `json:"on"`
	}
	if err := json.Unmarshal(raw, &selector); err != nil {
		return nil, err
	}
	node := strings.TrimSpace(firstNonEmpty(selector.NodeID, selector.On))
	if node != "" && node != d.cfg.NodeID && !strings.EqualFold(node, d.cfg.Hostname) {
		return d.call(ctx, protocol.FrameProjectChange, protocol.ProjectChangeRelay{
			Owner: d.ownerSessionID(), NodeID: node, Method: method, Args: raw, Preview: preview,
		})
	}
	return d.projectChangeLocal(ctx, method, raw, preview)
}

func (d *Daemon) projectChangeLocal(ctx context.Context, method string, raw json.RawMessage, preview bool) (any, error) {
	switch method {
	case "project_add":
		var p protocol.ProjectAddRequest
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		if preview {
			p.DryRun = true
		}
		raw, _ = json.Marshal(p)
	case "project_remove":
		p, err := proposalProjectRemove(raw)
		if err != nil {
			return nil, err
		}
		p.On = ""
		raw, _ = json.Marshal(p)
		if preview {
			return d.call(ctx, "cto.proposal.validate", map[string]any{"tool": method, "args": raw, "owner": d.ownerSessionID()})
		}
	default:
		return nil, errors.New("unknown project change")
	}
	lc := &localConn{d: d, client: "cli"}
	out, _, err := lc.dispatchProject(ctx, "cli", method, raw)
	return out, err
}

func (d *Daemon) onProjectOwnerFrame(ctx context.Context, l *link, h protocol.Header, raw []byte) {
	var p protocol.ProjectChangeRelay
	var out any
	err := json.Unmarshal(raw, &p)
	if err == nil {
		err = d.checkOwnerRelay(ctx, p.Owner, p.FromNode)
	}
	if err == nil {
		out, err = d.projectChangeLocal(ctx, p.Method, p.Args, p.Preview)
	}
	if res, encErr := protocol.EncodeResult(h.ID, out, err); encErr == nil {
		l.write(ctx, res)
	}
}

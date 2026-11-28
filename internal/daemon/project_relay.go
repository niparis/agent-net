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
func (d *Daemon) projectChange(ctx context.Context, method string, raw json.RawMessage) (any, error) {
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
			Owner: d.ownerSessionID(), NodeID: node, Method: method, Args: raw,
		})
	}
	return d.projectChangeLocal(ctx, method, raw, false)
}

// projectChangeLocal runs a project change on this machine. preview comes only
// from an older machine's relay, which checked a stored CTO proposal with it.
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
		p, err := ctoProjectRemove(raw)
		if err != nil {
			return nil, err
		}
		p.On = ""
		raw, _ = json.Marshal(p)
		if preview {
			// Never remove on a preview. The CTO now runs changes at once, so
			// there is no stored proposal to check.
			return nil, errors.New("project remove has no preview: the CTO runs project changes at once")
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

package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// folderMCP records the choice for the .mcp.json servers Claude Code would
// load in a folder, or takes back what an earlier choice added. It runs on the
// machine that holds the folder and travels there like a folder trust (decision
// 0094). It uses its own frames, so an older machine refuses it instead of
// treating it as a plain trust.
func (d *Daemon) folderMCP(ctx context.Context, q protocol.FolderMCPRequest) (protocol.FolderMCPResult, error) {
	q.Project, q.Path, q.NodeID = strings.TrimSpace(q.Project), strings.TrimSpace(q.Path), strings.TrimSpace(q.NodeID)
	if (q.Project == "") == (q.Path == "") {
		return protocol.FolderMCPResult{}, errors.New("mcp_servers needs a registered project or a path, not both")
	}
	if q.Undo == nil && q.Choice != protocol.MCPApprove && q.Choice != protocol.MCPDisable {
		return protocol.FolderMCPResult{}, fmt.Errorf("mcp_servers must be %q or %q", protocol.MCPApprove, protocol.MCPDisable)
	}
	target := ""
	if q.NodeID != "" && q.NodeID != d.cfg.NodeID && !strings.EqualFold(q.NodeID, d.cfg.Hostname) {
		target = q.NodeID
	} else if q.NodeID == "" && q.Project != "" {
		var err error
		if target, err = d.sessionStartMachine(ctx, protocol.SessionStartRequest{Project: q.Project}); err != nil {
			return protocol.FolderMCPResult{}, err
		}
	}
	if target == "" {
		return d.folderMCPLocal(ctx, q)
	}
	owner := d.ownerSessionID()
	if owner == "" {
		return protocol.FolderMCPResult{}, errors.New("the owner session is not registered yet (is the gateway reachable?)")
	}
	data, err := d.call(ctx, protocol.FrameSessionMCPServers, protocol.SessionFolderMCPRelay{Owner: owner, NodeID: target, FolderMCPRequest: q})
	if err != nil {
		return protocol.FolderMCPResult{}, tooOldGateway(err)
	}
	var res protocol.FolderMCPResult
	if err := json.Unmarshal(data, &res); err != nil {
		return protocol.FolderMCPResult{}, err
	}
	return res, nil
}

// folderMCPLocal does the work on this machine. A project must be registered
// here and a typed path must be an existing folder under the owner's home. An
// undo only needs the absolute path, so it works for a project folder outside
// home too.
func (d *Daemon) folderMCPLocal(ctx context.Context, q protocol.FolderMCPRequest) (protocol.FolderMCPResult, error) {
	res := protocol.FolderMCPResult{NodeID: d.cfg.NodeID, Hostname: d.hostName()}
	var err error
	switch {
	case q.Project != "":
		found, e := d.registeredLocalProject(ctx, q.Project)
		if e != nil {
			return res, e
		}
		res.Path, res.Project = found.Path, found.Name
	case q.Undo != nil:
		res.Path, err = expandPath(q.Path)
	default:
		res.Path, err = cleanOwnerPath(q.Path)
	}
	if err != nil {
		return res, err
	}
	if q.Undo != nil {
		if err := claudeMCPUndo(res.Path, *q.Undo); err != nil {
			return res, fmt.Errorf("cannot undo the MCP server choice for %s: %w", res.Path, err)
		}
		res.Choice = q.Undo.Choice
		d.Logf("folder.mcp path=%s undo=true choice=%s", res.Path, q.Undo.Choice)
		return res, nil
	}
	info, err := os.Stat(res.Path)
	if err != nil {
		return res, fmt.Errorf("cannot set the MCP server choice for %s: %w", res.Path, err)
	}
	if !info.IsDir() {
		return res, fmt.Errorf("cannot set the MCP server choice for %s: it is not a folder", res.Path)
	}
	choice, err := claudeMCPChoose(res.Path, q.Choice)
	if err != nil {
		return res, err
	}
	res.MCPChoiceResult = choice
	d.Logf("folder.mcp path=%s choice=%s added=%v restored=%v", res.Path, q.Choice, choice.Added, choice.Restored)
	return res, nil
}

// onFolderMCPFrame serves the owner's MCP server choice from another machine.
func (d *Daemon) onFolderMCPFrame(ctx context.Context, l *link, h protocol.Header, raw []byte) {
	var p protocol.SessionOwnerFolderMCP
	var res protocol.FolderMCPResult
	err := json.Unmarshal(raw, &p)
	if err != nil {
		err = errors.New("bad MCP server choice relay")
	} else if err = d.checkOwnerRelay(ctx, p.Owner, p.FromNode); err == nil {
		p.NodeID = ""
		res, err = d.folderMCPLocal(ctx, p.FolderMCPRequest)
	}
	if out, eerr := protocol.EncodeResult(h.ID, res, err); eerr == nil {
		l.write(ctx, out)
	}
}

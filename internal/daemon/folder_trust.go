package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/niparis/agent-net/internal/claudetrust"
	"github.com/niparis/agent-net/internal/protocol"
)

// folderTrust trusts a folder for Claude Code on the machine that holds it,
// or removes the trust agent-net added. The owner at the keyboard (the TUI's
// undo) and the CTO on the owner's instruction call it (decision 0095). A
// folder on another machine travels as an owner frame, like a session start,
// and that machine checks the owner flag again.
func (d *Daemon) folderTrust(ctx context.Context, q protocol.FolderTrustRequest) (protocol.FolderTrustResult, error) {
	q.Project, q.Path, q.NodeID = strings.TrimSpace(q.Project), strings.TrimSpace(q.Path), strings.TrimSpace(q.NodeID)
	if (q.Project == "") == (q.Path == "") {
		return protocol.FolderTrustResult{}, errors.New("folder trust needs a registered project or a path, not both")
	}
	target := ""
	if q.NodeID != "" && q.NodeID != d.cfg.NodeID && !strings.EqualFold(q.NodeID, d.cfg.Hostname) {
		target = q.NodeID
	} else if q.NodeID == "" && q.Project != "" {
		var err error
		if target, err = d.sessionStartMachine(ctx, protocol.SessionStartRequest{Project: q.Project}); err != nil {
			return protocol.FolderTrustResult{}, err
		}
	}
	if target == "" {
		return d.folderTrustLocal(ctx, q)
	}
	owner := d.ownerSessionID()
	if owner == "" {
		return protocol.FolderTrustResult{}, errors.New("the owner session is not registered yet (is the gateway reachable?)")
	}
	data, err := d.call(ctx, protocol.FrameSessionTrustFolder, protocol.SessionFolderTrustRelay{Owner: owner, NodeID: target, FolderTrustRequest: q})
	if err != nil {
		return protocol.FolderTrustResult{}, tooOldGateway(err)
	}
	var res protocol.FolderTrustResult
	if err := json.Unmarshal(data, &res); err != nil {
		return protocol.FolderTrustResult{}, err
	}
	return res, nil
}

// folderTrustLocal trusts (or untrusts) a folder on this machine. A project
// must be registered here and a typed path must be an existing folder under the
// owner's home, the rule a typed path in the New session form already has.
// Removing a trust only needs the absolute path, so an undo works for a project
// folder outside home too.
func (d *Daemon) folderTrustLocal(ctx context.Context, q protocol.FolderTrustRequest) (protocol.FolderTrustResult, error) {
	res := protocol.FolderTrustResult{NodeID: d.cfg.NodeID, Hostname: d.hostName()}
	var err error
	switch {
	case q.Project != "":
		found, e := d.registeredLocalProject(ctx, q.Project)
		if e != nil {
			return res, e
		}
		res.Path, res.Project = found.Path, found.Name
	case q.Remove:
		res.Path, err = expandPath(q.Path)
	default:
		res.Path, err = cleanOwnerPath(q.Path)
	}
	if err != nil {
		return res, err
	}
	if q.Remove {
		if err := claudetrust.Remove(res.Path); err != nil {
			return res, fmt.Errorf("cannot remove Claude Code trust for %s: %w", res.Path, err)
		}
		d.Logf("folder.trust path=%s remove=true", res.Path)
		return res, nil
	}
	info, err := os.Stat(res.Path)
	if err != nil {
		return res, fmt.Errorf("cannot trust %s: %w", res.Path, err)
	}
	if !info.IsDir() {
		return res, fmt.Errorf("cannot trust %s: it is not a folder", res.Path)
	}
	was, err := claudetrust.Trusted(res.Path)
	if err != nil {
		return res, fmt.Errorf("cannot read Claude Code trust for %s: %w", res.Path, err)
	}
	if err := claudetrust.Set(res.Path); err != nil {
		return res, fmt.Errorf("cannot trust %s for Claude Code: %w", res.Path, err)
	}
	res.Trusted, res.Added = true, !was
	d.Logf("folder.trust path=%s added=%t", res.Path, res.Added)
	return res, nil
}

// onFolderTrustFrame serves the owner's folder trust from another machine.
func (d *Daemon) onFolderTrustFrame(ctx context.Context, l *link, h protocol.Header, raw []byte) {
	var p protocol.SessionOwnerFolderTrust
	var res protocol.FolderTrustResult
	err := json.Unmarshal(raw, &p)
	if err != nil {
		err = errors.New("bad folder trust relay")
	} else if err = d.checkOwnerRelay(ctx, p.Owner, p.FromNode); err == nil {
		p.NodeID = ""
		res, err = d.folderTrustLocal(ctx, p.FolderTrustRequest)
	}
	if out, eerr := protocol.EncodeResult(h.ID, res, err); eerr == nil {
		l.write(ctx, out)
	}
}

// ctoTrustBeforeStart runs the folder trust of session_start with trust:true as
// its own audited CTO action, so it has an Inbox notice and an undo. A path
// that does not exist yet is skipped: starting it creates and trusts it.
func (d *Daemon) ctoTrustBeforeStart(ctx context.Context, fields map[string]json.RawMessage) error {
	var q struct {
		Project string `json:"project"`
		Path    string `json:"path"`
		NodeID  string `json:"node_id"`
		Harness string `json:"harness"`
	}
	b, _ := json.Marshal(fields)
	_ = json.Unmarshal(b, &q)
	if q.Harness != "" && q.Harness != "claude" {
		return nil
	}
	args := map[string]any{"action": "trust", "node_id": q.NodeID}
	switch {
	case strings.TrimSpace(q.Path) != "":
		if q.NodeID == "" {
			if p, err := cleanOwnerPath(q.Path); err == nil {
				if _, statErr := os.Stat(p); errors.Is(statErr, os.ErrNotExist) {
					return nil
				}
			}
		}
		args["path"] = q.Path
	case strings.TrimSpace(q.Project) != "":
		args["project"] = q.Project
	default:
		return nil
	}
	raw, _ := json.Marshal(args)
	_, err := d.executeImmediateCTOTool(ctx, "session_change", raw)
	return err
}

// registeredLocalProject finds a project registered on this machine by name or
// by path.
func (d *Daemon) registeredLocalProject(ctx context.Context, ref string) (*protocol.ProjectView, error) {
	raw, err := d.call(ctx, "query.projects", protocol.QueryProjects{})
	if err != nil {
		return nil, err
	}
	var projects protocol.ProjectsResult
	if err = json.Unmarshal(raw, &projects); err != nil {
		return nil, err
	}
	var found *protocol.ProjectView
	for _, p := range projects.Projects {
		if p.NodeID == d.cfg.NodeID && (p.Name == ref || p.Path == ref) {
			if found != nil {
				return nil, errors.New("project is ambiguous: use its local path")
			}
			cp := p
			found = &cp
		}
	}
	if found == nil {
		return nil, fmt.Errorf("project %q is not registered on this machine", ref)
	}
	return found, nil
}

package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/niparis/agent-net/internal/claudetrust"
	"github.com/niparis/agent-net/internal/protocol"
)

// Claude Code permission rules (decision 0127). The gateway sends these frames
// only after the owner answered a permission ask, or after the CTO applied the
// owner's direct chat instruction (decision 0159). They are handled on the
// gateway link and nowhere else: a local session has no method that can write
// these files.
//
// The rules go into <repo>/.claude/settings.local.json, under
// permissions.allow. Every other key and entry stays as it is. The write is
// atomic, and the file is kept out of git through .git/info/exclude when no
// ignore rule already covers it.

// claudePermissionParts reads the permissions object and its allow list,
// keeping every other key inside permissions untouched.
func claudePermissionParts(doc map[string]json.RawMessage) (map[string]json.RawMessage, []string, error) {
	perms := map[string]json.RawMessage{}
	if raw, ok := doc["permissions"]; ok {
		if err := json.Unmarshal(raw, &perms); err != nil {
			return nil, nil, fmt.Errorf("permissions in settings.local.json is not an object: %w", err)
		}
	}
	var allow []string
	if raw, ok := perms["allow"]; ok {
		if err := json.Unmarshal(raw, &allow); err != nil {
			return nil, nil, fmt.Errorf("permissions.allow in settings.local.json is not a list of rules: %w", err)
		}
	}
	return perms, allow, nil
}

// setClaudePermissionAllow writes the list back, keeping the file minimal when
// an undo removed everything.
func setClaudePermissionAllow(doc, perms map[string]json.RawMessage, allow []string) {
	if len(allow) == 0 {
		delete(perms, "allow")
	} else {
		perms["allow"], _ = json.Marshal(allow)
	}
	if len(perms) == 0 {
		delete(doc, "permissions")
		return
	}
	doc["permissions"], _ = json.Marshal(perms)
}

// claudePermissionAdd merges rules into permissions.allow. It reports exactly
// the rules it added, so an undo never removes a rule that was there before.
func claudePermissionAdd(dir string, rules []string) ([]string, error) {
	file := claudeLocalSettingsFile(dir)
	doc, err := readClaudeLocalSettings(file)
	if err != nil {
		return nil, err
	}
	perms, allow, err := claudePermissionParts(doc)
	if err != nil {
		return nil, err
	}
	var added []string
	for _, rule := range rules {
		if !containsName(allow, rule) {
			allow = append(allow, rule)
			added = append(added, rule)
		}
	}
	if len(added) > 0 {
		setClaudePermissionAllow(doc, perms, allow)
		if err := writeClaudeLocalSettings(file, doc); err != nil {
			return nil, err
		}
	}
	if err := ensureClaudeSettingsIgnored(dir); err != nil {
		return added, err
	}
	return added, nil
}

// claudePermissionRemove takes back exactly the rules one approval added.
func claudePermissionRemove(dir string, rules []string) ([]string, error) {
	file := claudeLocalSettingsFile(dir)
	if _, err := os.Stat(file); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	doc, err := readClaudeLocalSettings(file)
	if err != nil {
		return nil, err
	}
	perms, allow, err := claudePermissionParts(doc)
	if err != nil {
		return nil, err
	}
	var removed []string
	kept := allow[:0]
	for _, rule := range allow {
		if containsName(rules, rule) {
			removed = append(removed, rule)
			continue
		}
		kept = append(kept, rule)
	}
	if len(removed) == 0 {
		return nil, nil
	}
	setClaudePermissionAllow(doc, perms, kept)
	if err := writeClaudeLocalSettings(file, doc); err != nil {
		return nil, err
	}
	return removed, nil
}

// reportTrustNeed tells the gateway that a local start answered needs_trust,
// so the gateway raises the owner's one-click trust ask. It writes nothing.
func (d *Daemon) reportTrustNeed(ctx context.Context, path, project, session string) {
	if !filepath.IsAbs(path) {
		return
	}
	if session == "" {
		session = d.ownerSessionID()
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := d.call(cctx, protocol.FramePermissionTrustAsk, protocol.PermissionTrustAsk{
		Session: session, Owner: d.ownerSessionID(), Path: path, Project: project,
	}); err != nil {
		d.Logf("permission.trust_ask.report path=%s error=%v", path, err)
	}
}

// proposePermission sends one proposal to the gateway. It writes nothing.
func (d *Daemon) proposePermission(ctx context.Context, session string, req protocol.PermissionProposeRequest) (protocol.PermissionProposeResult, error) {
	var out protocol.PermissionProposeResult
	data, err := d.call(ctx, "permission.propose", protocol.PermissionProposeFrame{PermissionProposeRequest: req, SessionID: session})
	if err != nil {
		return out, tooOldGateway(err)
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return out, err
	}
	return out, nil
}

// permissionApply writes one proposal's rules into one repository, or trusts
// one folder through the owner's existing trust writer.
func (d *Daemon) permissionApply(ctx context.Context, p protocol.PermissionApplyFrame) protocol.PermissionApplyResult {
	res := protocol.PermissionApplyResult{Target: p.Target, Kind: p.Kind, Status: "failed"}
	if !filepath.IsAbs(p.Target.Path) {
		res.Error = "the target path is not absolute"
		return res
	}
	if p.Kind == protocol.PermissionKindTrust {
		out, err := d.folderTrustLocal(ctx, protocol.FolderTrustRequest{Path: p.Target.Path})
		if err != nil {
			res.Error = err.Error()
			return res
		}
		res.TrustAdded = out.Added
		switch {
		case out.Added:
			res.Status = "applied"
		case out.Trusted:
			res.Status = "already_present"
		default:
			res.Error = "the folder was not trusted"
			return res
		}
		d.Logf("permission.apply proposal=%s folder=%s trusted=%t added=%t", p.ProposalID, p.Target.Path, out.Trusted, out.Added)
		return res
	}
	info, err := os.Stat(p.Target.Path)
	if err != nil {
		res.Error = "the repository folder is not reachable: " + err.Error()
		return res
	}
	if !info.IsDir() {
		res.Error = "the target is not a folder"
		return res
	}
	added, err := claudePermissionAdd(p.Target.Path, p.Rules)
	res.Added = added
	res.File = claudeLocalSettingsFile(p.Target.Path)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	if len(added) == 0 {
		res.Status = "already_present"
	} else {
		res.Status = "applied"
	}
	d.Logf("permission.apply proposal=%s repo=%s path=%s added=%d", p.ProposalID, p.Target.Name, p.Target.Path, len(added))
	return res
}

// permissionRevoke removes only the listed rules, or the trust, one approval
// added.
func (d *Daemon) permissionRevoke(ctx context.Context, p protocol.PermissionRevokeFrame) protocol.PermissionApplyResult {
	res := protocol.PermissionApplyResult{Target: protocol.PermissionTarget{Path: p.Path}, Kind: p.Kind, Status: "failed"}
	if !filepath.IsAbs(p.Path) {
		res.Error = "the target path is not absolute"
		return res
	}
	if p.Kind == protocol.PermissionKindTrust {
		was, err := claudetrust.Trusted(p.Path)
		if err != nil {
			res.Error = err.Error()
			return res
		}
		if !was {
			res.Status = "absent"
			return res
		}
		if _, err := d.folderTrustLocal(ctx, protocol.FolderTrustRequest{Path: p.Path, Remove: true}); err != nil {
			res.Error = err.Error()
			return res
		}
		res.Status = "removed"
		d.Logf("permission.revoke proposal=%s folder=%s removed=true", p.ProposalID, p.Path)
		return res
	}
	removed, err := claudePermissionRemove(p.Path, p.Rules)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	res.Removed = removed
	if len(removed) == 0 {
		res.Status = "absent"
	} else {
		res.Status = "removed"
	}
	d.Logf("permission.revoke proposal=%s path=%s removed=%d", p.ProposalID, p.Path, len(removed))
	return res
}

// onPermissionFrame serves the gateway's apply and revoke frames.
func (d *Daemon) onPermissionFrame(ctx context.Context, l *link, h protocol.Header, raw []byte) {
	var res protocol.PermissionApplyResult
	var err error
	switch h.Type {
	case protocol.FramePermissionApply:
		var p protocol.PermissionApplyFrame
		if json.Unmarshal(raw, &p) != nil || p.Target.Path == "" ||
			(p.Kind != protocol.PermissionKindTrust && len(p.Rules) == 0) {
			err = errors.New("bad permission apply")
		} else {
			res = d.permissionApply(ctx, p)
		}
	case protocol.FramePermissionRevoke:
		var p protocol.PermissionRevokeFrame
		if json.Unmarshal(raw, &p) != nil || p.Path == "" ||
			(p.Kind != protocol.PermissionKindTrust && len(p.Rules) == 0) {
			err = errors.New("bad permission revoke")
		} else {
			res = d.permissionRevoke(ctx, p)
		}
	default:
		err = errors.New("unknown permission frame")
	}
	if out, eerr := protocol.EncodeResult(h.ID, res, err); eerr == nil {
		l.write(ctx, out)
	}
}

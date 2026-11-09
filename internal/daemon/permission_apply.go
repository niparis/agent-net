package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/niparis/agent-net/internal/protocol"
)

// Claude Code permission rules (decision 0126). The gateway sends these frames
// only after the owner answered a permission ask. They are handled on the
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

// permissionApply writes one proposal's rules into one repository.
func (d *Daemon) permissionApply(p protocol.PermissionApplyFrame) protocol.PermissionApplyResult {
	res := protocol.PermissionApplyResult{Target: p.Target, Status: "failed"}
	if !filepath.IsAbs(p.Target.Path) {
		res.Error = "the target path is not absolute"
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

// permissionRevoke removes only the listed rules from one repository.
func (d *Daemon) permissionRevoke(p protocol.PermissionRevokeFrame) protocol.PermissionApplyResult {
	res := protocol.PermissionApplyResult{Target: protocol.PermissionTarget{Path: p.Path}, Status: "failed"}
	if !filepath.IsAbs(p.Path) {
		res.Error = "the target path is not absolute"
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
		if json.Unmarshal(raw, &p) != nil || p.Target.Path == "" || len(p.Rules) == 0 {
			err = errors.New("bad permission apply")
		} else {
			res = d.permissionApply(p)
		}
	case protocol.FramePermissionRevoke:
		var p protocol.PermissionRevokeFrame
		if json.Unmarshal(raw, &p) != nil || p.Path == "" || len(p.Rules) == 0 {
			err = errors.New("bad permission revoke")
		} else {
			res = d.permissionRevoke(p)
		}
	default:
		err = errors.New("unknown permission frame")
	}
	if out, eerr := protocol.EncodeResult(h.ID, res, err); eerr == nil {
		l.write(ctx, out)
	}
}

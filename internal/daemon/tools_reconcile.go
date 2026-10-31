package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
)

type skillManifestEntry struct {
	Item     string `json:"item"`
	Path     string `json:"path"`
	Hash     string `json:"hash"`
	Revision int64  `json:"revision"`
	Owned    bool   `json:"owned"`
}

func (d *Daemon) recordSkill(t protocol.ToolTarget, dest string, owned bool) error {
	path := filepath.Join(d.cfg.StateDir, "skill-manifest.json")
	var entries []skillManifestEntry
	b, err := os.ReadFile(path)
	if err == nil {
		if err = json.Unmarshal(b, &entries); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	found := false
	for i := range entries {
		if entries[i].Path == dest {
			if entries[i].Item == t.Revision.Item && entries[i].Owned {
				owned = true
			}
			entries[i] = skillManifestEntry{t.Revision.Item, dest, t.Revision.Version, t.Revision.Revision, owned}
			found = true
			break
		}
	}
	if !found {
		entries = append(entries, skillManifestEntry{t.Revision.Item, dest, t.Revision.Version, t.Revision.Revision, owned})
	}
	b, err = json.Marshal(entries)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(d.cfg.StateDir, ".skill-manifest-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}

func (d *Daemon) copyGlobalSkill(t protocol.ToolTarget, bundle []byte) protocol.ToolResult {
	r := protocol.ToolResult{Item: t.Revision.Item, Revision: t.Revision.Revision, Index: t.Index}
	fail := func(state, reason string) protocol.ToolResult { r.State = state; r.Reason = reason; return r }
	if t.Revision.Assignment.Scope != "global" || t.Target.Checkout != "" || t.Target.Action != "copy" {
		return fail("pending", "operation is not supported yet")
	}
	if t.Target.Conflict != "" && t.Target.Conflict != "destination already exists and is not owned" && t.Target.Conflict != "machine offline; destination cannot be inspected" {
		return fail("conflict", "plan target requires inspection")
	}
	if t.Name == "" || t.Name == "." || t.Name == ".." || filepath.Base(t.Name) != t.Name || strings.ContainsAny(t.Name, "/\\\x00") {
		return fail("conflict", "invalid skill name")
	}
	root := d.cfg.InventoryHome
	if root == "" || !filepath.IsAbs(root) || d.cfg.StateDir == "" {
		return fail("failed", "local directories unavailable")
	}
	var rel string
	switch t.Target.Harness {
	case "claude":
		rel = ".claude/skills"
	case "codex":
		rel = ".agents/skills"
	case "opencode":
		rel = ".config/opencode/skills"
	case "pi":
		rel = ".pi/agent/skills"
	default:
		return fail("manual", "unsupported harness")
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() {
		return fail("conflict", "home is not a directory")
	}
	cur := root
	for _, part := range strings.Split(rel, "/") {
		cur = filepath.Join(cur, part)
		info, err = os.Lstat(cur)
		if os.IsNotExist(err) {
			if err = os.Mkdir(cur, 0700); err != nil && !os.IsExist(err) {
				return fail("failed", "cannot create skill root")
			}
			info, err = os.Lstat(cur)
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fail("conflict", "skill root is not a directory")
		}
	}
	dest := filepath.Join(cur, t.Name)
	if _, err = os.Lstat(dest); err == nil {
		hash, e := inventory.SkillHash(dest)
		if e != nil || hash != t.Revision.Version {
			return fail("conflict", "destination differs")
		}
		if e = d.recordSkill(t, dest, false); e != nil {
			return fail("failed", "cannot record manifest")
		}
		return fail("applied", "")
	} else if !os.IsNotExist(err) {
		return fail("failed", "cannot inspect destination")
	}
	stage, err := os.MkdirTemp(cur, ".agent-net-stage-")
	if err != nil {
		return fail("failed", "cannot create staging directory")
	}
	defer os.RemoveAll(stage)
	if err = inventory.UnpackSkill(bundle, stage); err != nil {
		return fail("failed", "invalid skill bundle")
	}
	hash, err := inventory.SkillHash(stage)
	if err != nil || hash != t.Revision.Version {
		return fail("failed", "skill hash mismatch")
	}
	if err = renameSkillNoReplace(stage, dest); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fail("conflict", "destination appeared during copy")
		}
		return fail("failed", "cannot install skill")
	}
	if err = d.recordSkill(t, dest, true); err != nil {
		return fail("failed", "cannot record manifest")
	}
	return fail("applied", "")
}

func (d *Daemon) reconcileTools(ctx context.Context) {
	raw, err := d.call(ctx, "tools.targets", struct{}{})
	if err != nil {
		return
	}
	var targets []protocol.ToolTarget
	if json.Unmarshal(raw, &targets) != nil {
		return
	}
	blocked := map[string]bool{}
	for _, t := range targets {
		if t.Target.Action == "inspect" && t.Target.Conflict != "" {
			blocked[t.Revision.Item] = true
		}
	}
	for _, t := range targets {
		result := protocol.ToolResult{Item: t.Revision.Item, Revision: t.Revision.Revision, Index: t.Index, State: "pending", Reason: "operation is not supported yet"}
		if blocked[t.Revision.Item] {
			result.State = "conflict"
			result.Reason = "plan requires inspection"
		} else if t.Revision.Assignment.Scope == "global" && t.Target.Action == "copy" && t.Target.Checkout == "" {
			raw, err = d.call(ctx, "tools.export", protocol.ToolVersionRequest{Item: t.Revision.Item, Version: t.Revision.Version})
			if err != nil {
				result.State = "failed"
				result.Reason = "stored skill unavailable"
			} else {
				var b protocol.SkillBundle
				if json.Unmarshal(raw, &b) != nil {
					result.State = "failed"
					result.Reason = "invalid stored bundle"
				} else {
					result = d.copyGlobalSkill(t, b.Data)
				}
			}
		}
		_, _ = d.call(ctx, "tools.report", result)
	}
}

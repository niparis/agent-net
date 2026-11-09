package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
)

// onSkillUpdateCopy replaces one older copy with a stored version. The gateway
// sends it only after the owner confirmed a plan.
func (d *Daemon) onSkillUpdateCopy(ctx context.Context, l *link, h protocol.Header, raw []byte) {
	var p protocol.ToolUpdateCopyRequest
	var result protocol.ToolResult
	err := json.Unmarshal(raw, &p)
	if err == nil {
		result = d.updateSkillCopy(p)
		if result.State == "applied" {
			d.scanTools(ctx)
		}
	}
	_ = writeSkillScanResult(l, ctx, h.ID, result, err)
}

// skillUpdateRoots are the places agent-net writes skills, relative to the
// home folder (user copies) or the checkout (project copies).
var skillUpdateRoots = map[string]bool{".claude/skills": true, ".agents/skills": true}

// updateSkillCopy replaces the copy at req.Location with req.Data. It never
// touches a copy that was edited after it was deployed, and it never touches a
// copy whose content is not exactly req.FromHash. The old copy is backed up by
// the guarded removal first. If the new copy cannot be installed, the backup is
// moved back.
func (d *Daemon) updateSkillCopy(req protocol.ToolUpdateCopyRequest) protocol.ToolResult {
	r := protocol.ToolResult{Item: req.ToItem}
	fail := func(state, reason string) protocol.ToolResult { r.State, r.Reason = state, reason; return r }
	loc := filepath.Clean(req.Location)
	if req.Name == "" || !filepath.IsAbs(loc) || filepath.Base(loc) != req.Name || req.FromHash == "" || req.ToHash == "" || req.FromHash == req.ToHash {
		return fail("conflict", "invalid update request")
	}
	// Refuse a bad bundle before anything is moved.
	stage, err := os.MkdirTemp(d.cfg.StateDir, ".update-verify-")
	if err != nil {
		return fail("failed", "cannot verify skill bundle")
	}
	defer os.RemoveAll(stage)
	if inventory.UnpackSkill(req.Data, stage) != nil {
		return fail("failed", "invalid skill bundle")
	}
	if got, e := inventory.SkillHash(stage); e != nil || got != req.ToHash {
		return fail("failed", "skill hash mismatch")
	}
	d.toolsMu.Lock()
	var observed *protocol.InventoryItem
	for _, v := range d.toolsReport.Items {
		if v.Kind == protocol.InventorySkill && v.Name == req.Name && filepath.Clean(v.Location) == loc {
			v := v
			observed = &v
		}
	}
	d.toolsMu.Unlock()
	if observed == nil || observed.State != protocol.InventoryOK {
		return fail("conflict", "copy is no longer observed")
	}
	if observed.ChangedHere {
		return fail("conflict", "changed here: save it as a version first")
	}
	if observed.Source != protocol.InventoryNative || observed.Tracked {
		return fail("manual", "copy is managed externally or tracked by Git")
	}
	if observed.Hash != req.FromHash {
		return fail("conflict", "changed since plan")
	}
	// The manifest is the second witness: an owned copy whose recorded hash
	// differs from its content was edited, even if the scan is older.
	previous, owned := d.skillManifestEntryAt(loc)
	if owned && previous.Hash != req.FromHash {
		return fail("conflict", "changed here: save it as a version first")
	}
	checkout := ""
	base := d.cfg.InventoryHome
	if observed.Scope == protocol.InventoryProject {
		checkout = filepath.Clean(observed.ProjectRoot)
		base = checkout
	}
	rel, err := filepath.Rel(base, filepath.Dir(loc))
	if err != nil || !skillUpdateRoots[filepath.ToSlash(rel)] || base == "" {
		return fail("manual", "copy is outside the places agent-net writes")
	}
	removal := d.removeSkill(protocol.ToolTarget{Name: req.Name, Revision: protocol.ToolRevision{Item: req.FromItem, Version: req.FromHash}, Target: protocol.ToolPlanTarget{Source: loc, Checkout: checkout, Action: "remove"}})
	if removal.State != "applied" {
		return fail(removal.State, removal.Reason)
	}
	revision := protocol.ToolRevision{Item: req.ToItem, Version: req.ToHash}
	target := protocol.ToolPlanTarget{Harness: observed.Harness, Root: filepath.ToSlash(rel), Destination: loc, Action: "copy"}
	var result protocol.ToolResult
	if checkout != "" {
		revision.Assignment = protocol.ToolAssignment{Scope: "projects", Projects: []string{observed.Project}}
		target.Checkout, target.Project = checkout, observed.Project
		result = d.copyProjectSkillIn(protocol.ToolTarget{Name: req.Name, Revision: revision, Target: target}, req.Data, nil, false)
	} else {
		revision.Assignment = protocol.ToolAssignment{Scope: "global"}
		result = d.copyGlobalSkill(protocol.ToolTarget{Name: req.Name, Revision: revision, Target: target}, req.Data)
	}
	if result.State == "applied" {
		r.State = "applied"
		return r
	}
	// The install failed after the old copy was moved away. Put it back.
	if e := d.restoreSkillFromBackup(req.FromItem, loc); e != nil {
		return fail("failed", "update failed ("+result.Reason+") and the old copy could not be restored: "+e.Error())
	}
	if owned {
		_ = d.recordSkill(protocol.ToolTarget{Name: req.Name, Revision: protocol.ToolRevision{Item: previous.Item, Version: previous.Hash, Revision: previous.Revision}}, loc, true)
	}
	return fail(result.State, "update failed, the old copy was restored: "+result.Reason)
}

// skillManifestEntryAt returns the manifest entry of a skill path made by the
// daemon.
func (d *Daemon) skillManifestEntryAt(path string) (skillManifestEntry, bool) {
	b, err := os.ReadFile(filepath.Join(d.cfg.StateDir, "skill-manifest.json"))
	if err != nil {
		return skillManifestEntry{}, false
	}
	var entries []skillManifestEntry
	if json.Unmarshal(b, &entries) != nil {
		return skillManifestEntry{}, false
	}
	for _, e := range entries {
		if filepath.Clean(e.Path) == filepath.Clean(path) && e.Owned {
			return e, true
		}
	}
	return skillManifestEntry{}, false
}

// restoreSkillFromBackup moves the newest backup of source back into place.
func (d *Daemon) restoreSkillFromBackup(item, source string) error {
	sum := sha256.Sum256([]byte(item))
	dir := filepath.Join(d.cfg.StateDir, "skill-backups", hex.EncodeToString(sum[:]))
	stamps, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var names []string
	for _, s := range stamps {
		names = append(names, s.Name())
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	for _, n := range names {
		backup := filepath.Join(dir, n, strings.TrimPrefix(source, string(os.PathSeparator)))
		if _, e := os.Lstat(backup); e == nil {
			return restoreSkillBackup(backup, source)
		}
	}
	return os.ErrNotExist
}

// destinationDiffersReason explains why a planned copy cannot be written over
// what is there. When the daemon itself put a newer version of the skill in
// that place (an update), the older assignment is simply out of date.
func (d *Daemon) destinationDiffersReason(dest, currentHash, item string) string {
	if e, ok := d.skillManifestEntryAt(dest); ok && e.Item != item && e.Hash == currentHash {
		return "replaced by a newer version"
	}
	return "destination differs"
}

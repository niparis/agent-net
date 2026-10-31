package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
)

// undoCopyDestination returns the root and destination path of a copy that an
// undo reverses, or a conflict reason.
func (d *Daemon) undoCopyDestination(t protocol.ToolTarget, targets []protocol.ToolTarget) (root, dest, reason string) {
	if t.Name == "" || t.Name == "." || t.Name == ".." || filepath.Base(t.Name) != t.Name || strings.ContainsAny(t.Name, "/\\\x00\r\n") {
		return "", "", "invalid skill name"
	}
	if t.Target.Checkout == "" {
		rel := globalSkillRel(t.Target.Harness)
		if rel == "" || !filepath.IsAbs(d.cfg.InventoryHome) {
			return "", "", "unsupported harness"
		}
		dest = filepath.Join(d.cfg.InventoryHome, rel, t.Name)
		if t.Target.Destination != "" && filepath.Clean(t.Target.Destination) != dest {
			return "", "", "planned destination differs"
		}
		return d.cfg.InventoryHome, dest, ""
	}
	checkout := filepath.Clean(t.Target.Checkout)
	if !filepath.IsAbs(checkout) {
		return "", "", "invalid checkout"
	}
	dest = filepath.Clean(t.Target.Destination)
	if t.Target.Destination == "" {
		var names []string
		for _, v := range targets {
			if v.Revision.Item == t.Revision.Item && v.Target.Checkout == t.Target.Checkout && v.Target.Action == t.Target.Action {
				names = append(names, v.Target.Harness)
			}
		}
		skillRoot, _ := inventory.ProjectSkillRoot(t.Target.Harness, names)
		dest = filepath.Join(checkout, skillRoot, t.Name)
	}
	rel, err := filepath.Rel(checkout, filepath.Dir(dest))
	allowed := map[string]bool{".claude/skills": true, ".agents/skills": true, ".opencode/skills": true, ".pi/skills": true}
	if err != nil || filepath.Base(dest) != t.Name || !allowed[filepath.ToSlash(rel)] {
		return "", "", "planned destination differs"
	}
	return checkout, dest, ""
}

func skillTreeHasSymlink(path string) bool {
	return filepath.WalkDir(path, func(_ string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.Type()&os.ModeSymlink != 0 {
			return fs.ErrInvalid
		}
		return nil
	}) != nil
}

// undoCopySkill reverses a copy: it removes the installed directory only if it
// is still the copy this daemon made, keeping a local backup, and releases the
// owned Git exclude line. A directory that changed since apply is left alone.
func (d *Daemon) undoCopySkill(t protocol.ToolTarget, targets []protocol.ToolTarget) protocol.ToolResult {
	r := protocol.ToolResult{Item: t.Revision.Item, Revision: t.Revision.Revision, Index: t.Index}
	fail := func(state, reason string) protocol.ToolResult { r.State, r.Reason = state, reason; return r }
	root, dest, reason := d.undoCopyDestination(t, targets)
	if reason != "" {
		return fail("conflict", reason)
	}
	if reason = d.skillConflict(dest); reason != "" {
		return fail("conflict", reason)
	}
	rel, err := filepath.Rel(root, dest)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return fail("conflict", "destination outside authorized root")
	}
	if _, err = os.Lstat(filepath.Dir(dest)); os.IsNotExist(err) {
		if err = d.forgetSkill(dest); err != nil {
			return fail("failed", "cannot update skill manifest or Git exclude")
		}
		return fail("applied", "")
	}
	parent, closeParent, err := openSkillRoot(root, filepath.Dir(rel))
	if err != nil {
		return fail("conflict", "destination path is unsafe")
	}
	defer closeParent()
	anchored := filepath.Join(parent, t.Name)
	info, err := os.Lstat(anchored)
	if os.IsNotExist(err) {
		if err = d.forgetSkill(dest); err != nil {
			return fail("failed", "cannot update skill manifest or Git exclude")
		}
		return fail("applied", "")
	}
	if err != nil || !info.IsDir() {
		return fail("conflict", "changed since apply")
	}
	owned, err := d.projectSkillOwned(dest, t.Revision.Item)
	if err != nil {
		return fail("failed", "cannot read manifest")
	}
	if !owned {
		return fail("conflict", "not installed by agent-net")
	}
	if skillTreeHasSymlink(anchored) {
		return fail("conflict", "changed since apply")
	}
	if top, e := projectGit(filepath.Dir(dest), "rev-parse", "--show-toplevel"); e == nil {
		gitRoot := strings.TrimSpace(string(top))
		if gitRel, e := filepath.Rel(gitRoot, dest); e == nil {
			if tracked, e := projectGit(gitRoot, "ls-files", "-z", "--", gitRel); e != nil || len(tracked) != 0 {
				return fail("manual", "destination contains tracked files")
			}
		}
	}
	if h, e := inventory.SkillHash(anchored); e != nil || h != t.Revision.Version {
		return fail("conflict", "changed since apply")
	}
	j, err := newSkillJournal(d.cfg.StateDir, skillJournalRecord{Operation: "remove", Target: dest, BeforeHash: t.Revision.Version, Item: t.Revision.Item, Revision: t.Revision.Revision, Step: "prepared"})
	if err != nil {
		return fail("failed", "cannot write skill journal")
	}
	state, why, keep := d.backupAndForget(j, anchored, dest, t.Revision.Item, t.Revision.Version)
	if !keep {
		_ = j.finish()
	}
	return fail(state, why)
}

// undoRemoveSkill reverses a removal by copying the local backup back to the
// original path. It acts only when that path is still absent.
func (d *Daemon) undoRemoveSkill(t protocol.ToolTarget) protocol.ToolResult {
	r := protocol.ToolResult{Item: t.Revision.Item, Revision: t.Revision.Revision, Index: t.Index}
	fail := func(state, reason string) protocol.ToolResult { r.State, r.Reason = state, reason; return r }
	source := filepath.Clean(t.Target.Source)
	if !filepath.IsAbs(source) || t.Name == "" || filepath.Base(source) != t.Name || t.Name == "." || t.Name == ".." {
		return fail("conflict", "invalid source")
	}
	if reason := d.skillConflict(source); reason != "" {
		return fail("conflict", reason)
	}
	root := d.skillRootFor(t, source)
	rel, err := filepath.Rel(root, source)
	if err != nil || !filepath.IsAbs(root) || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fail("conflict", "source outside authorized root")
	}
	if _, err = os.Lstat(source); err == nil {
		if skillHashIs(source, t.Revision.Version) {
			return fail("applied", "")
		}
		return fail("conflict", "changed since apply")
	} else if !os.IsNotExist(err) {
		return fail("failed", "cannot inspect source")
	}
	backup := d.findSkillBackup(t.Revision.Item, source, t.Revision.Version)
	if backup == "" {
		return fail("failed", "no local backup matches the removed content")
	}
	bundle, err := inventory.PackSkill(backup)
	if err != nil {
		return fail("failed", "cannot read local backup")
	}
	j, err := newSkillJournal(d.cfg.StateDir, skillJournalRecord{Operation: "restore", Target: source, Backup: backup, AfterHash: t.Revision.Version, Item: t.Revision.Item, Revision: t.Revision.Revision, Step: "prepared"})
	if err != nil {
		return fail("failed", "cannot write skill journal")
	}
	retain := false
	defer func() {
		if !retain {
			_ = j.finish()
		}
	}()
	parent, closeParent, err := openSkillRoot(root, filepath.Dir(rel))
	if err != nil {
		return fail("conflict", "source path is unsafe")
	}
	defer closeParent()
	stageName := skillStagePrefix + filepath.Base(j.path)
	stage := filepath.Join(parent, stageName)
	j.record.Stage = filepath.Join(filepath.Dir(source), stageName)
	if err = j.advance("staging"); err != nil {
		retain = true
		return fail("failed", "skill operation interrupted")
	}
	if err = os.Mkdir(stage, 0700); err != nil {
		return fail("failed", "cannot create staging directory")
	}
	defer func() {
		if !retain {
			_ = os.RemoveAll(stage)
		}
	}()
	if err = inventory.UnpackSkill(bundle, stage); err != nil {
		return fail("failed", "invalid local backup")
	}
	if h, e := inventory.SkillHash(stage); e != nil || h != t.Revision.Version {
		return fail("failed", "local backup does not match")
	}
	if err = j.advance("staged"); err != nil {
		retain = true
		return fail("failed", "skill operation interrupted")
	}
	if err = renameSkillNoReplace(stage, filepath.Join(parent, t.Name)); err != nil {
		if os.IsExist(err) {
			return fail("conflict", "changed since apply")
		}
		return fail("failed", "cannot restore skill")
	}
	if err = j.advance("installed"); err != nil {
		retain = true
		return fail("failed", "skill operation interrupted")
	}
	return fail("applied", "")
}

// skillBackupDir is the private folder that holds an item's backups.
func (d *Daemon) skillBackupDir(item string) string {
	sum := sha256.Sum256([]byte(item))
	return filepath.Join(d.cfg.StateDir, "skill-backups", hex.EncodeToString(sum[:]))
}

// findSkillBackup returns the newest backup of source whose content has the hash.
func (d *Daemon) findSkillBackup(item, source, hash string) string {
	dir := d.skillBackupDir(item)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() > entries[j].Name() })
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		candidate := filepath.Join(dir, e.Name(), strings.TrimPrefix(source, string(os.PathSeparator)))
		if skillHashIs(candidate, hash) {
			return candidate
		}
	}
	return ""
}

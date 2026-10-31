package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
)

func (d *Daemon) verifySkillAbsence(t protocol.ToolTarget) protocol.ToolResult {
	r := protocol.ToolResult{Item: t.Revision.Item, Revision: t.Revision.Revision, Index: t.Index, State: "pending"}
	var identity []string
	if json.Unmarshal([]byte(t.Revision.Item), &identity) != nil || len(identity) != 3 || identity[0] != protocol.InventorySkill {
		r.Reason = "absence verification is not supported"
		return r
	}
	d.toolsMu.Lock()
	report := d.toolsReport
	report.Items = append([]protocol.InventoryItem(nil), report.Items...)
	d.toolsMu.Unlock()
	if report.State == protocol.InventoryError || report.State == protocol.InventoryStale {
		r.Reason = "inventory scan incomplete"
		return r
	}
	for _, obs := range report.Items {
		if obs.Kind != protocol.InventorySkill || obs.Name != t.Name || obs.Hash != t.Revision.Version {
			continue
		}
		_, err := os.Lstat(obs.Location)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			r.Reason = "cannot inspect source"
			return r
		}
		if obs.Source != protocol.InventoryNative {
			r.State = "manual"
			r.Reason = "source is managed externally"
		} else {
			r.Reason = "skill is still present"
		}
		return r
	}
	r.State = "applied"
	return r
}

// removeSkill moves a verified observed directory into local private storage.
func (d *Daemon) removeSkill(t protocol.ToolTarget) protocol.ToolResult {
	r := protocol.ToolResult{Item: t.Revision.Item, Revision: t.Revision.Revision, Index: t.Index}
	fail := func(state, reason string) protocol.ToolResult { r.State, r.Reason = state, reason; return r }
	if t.Target.Conflict != "" && t.Target.Conflict != "unmanaged source requires guarded removal" && t.Target.Conflict != "machine offline; source cannot be inspected" {
		return fail("conflict", t.Target.Conflict)
	}
	source := filepath.Clean(t.Target.Source)
	if reason := d.skillConflict(source); reason != "" {
		return fail("conflict", reason)
	}
	if !filepath.IsAbs(source) || t.Name == "" || filepath.Base(source) != t.Name || t.Name == "." || t.Name == ".." {
		return fail("conflict", "invalid source")
	}
	d.toolsMu.Lock()
	var observed *protocol.InventoryItem
	managed := false
	for _, v := range d.toolsReport.Items {
		if v.Kind == protocol.InventorySkill && v.Name == t.Name && v.Location == source {
			if v.Source != protocol.InventoryNative {
				managed = true
			}
			v := v
			observed = &v
		}
	}
	d.toolsMu.Unlock()
	root := d.cfg.InventoryHome
	if t.Target.Checkout != "" {
		root = filepath.Clean(t.Target.Checkout)
	} else if filepath.IsAbs(d.cfg.CodexHome) {
		codexRel, e := filepath.Rel(filepath.Join(d.cfg.CodexHome, "skills"), source)
		if e == nil && codexRel != ".." && !strings.HasPrefix(codexRel, ".."+string(os.PathSeparator)) {
			root = d.cfg.CodexHome
		}
	}
	rel, err := filepath.Rel(root, source)
	if err != nil || !filepath.IsAbs(root) || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fail("conflict", "source outside authorized root")
	}
	parent, closeParent, err := openSkillRoot(root, filepath.Dir(rel))
	if err != nil {
		return fail("conflict", "source path is unsafe")
	}
	defer closeParent()
	anchored := filepath.Join(parent, t.Name)
	info, err := os.Lstat(anchored)
	if os.IsNotExist(err) {
		if err = d.forgetSkill(source); err != nil {
			return fail("failed", "cannot update skill manifest or Git exclude")
		}
		return fail("applied", "")
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fail("conflict", "source is not a safe directory")
	}
	if observed == nil {
		return fail("conflict", "source is no longer observed")
	}
	if managed {
		return fail("manual", "source is managed externally")
	}
	if observed.Hash != t.Revision.Version {
		return fail("conflict", "changed since plan")
	}
	if t.Target.Checkout != "" && (observed.Scope != protocol.InventoryProject || filepath.Clean(observed.ProjectRoot) != filepath.Clean(t.Target.Checkout)) {
		return fail("conflict", "source project differs from plan")
	}
	if t.Target.Checkout == "" && observed.Scope == protocol.InventoryProject {
		return fail("conflict", "source project differs from plan")
	}
	if err = filepath.WalkDir(anchored, func(_ string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.Type()&os.ModeSymlink != 0 {
			return fs.ErrInvalid
		}
		return nil
	}); err != nil {
		return fail("conflict", "source contains a symlink or cannot be inspected")
	}
	j, err := newSkillJournal(d.cfg.StateDir, skillJournalRecord{Operation: "remove", Target: source, BeforeHash: t.Revision.Version, Item: t.Revision.Item, Revision: t.Revision.Revision, Step: "prepared"})
	if err != nil {
		return fail("failed", "cannot write skill journal")
	}
	retain := false
	defer func() {
		if !retain {
			_ = j.finish()
		}
	}()
	top, gitErr := projectGit(filepath.Dir(source), "rev-parse", "--show-toplevel")
	if gitErr == nil {
		gitRoot := strings.TrimSpace(string(top))
		gitRel, e := filepath.Rel(gitRoot, source)
		if e != nil || gitRel == ".." || strings.HasPrefix(gitRel, ".."+string(os.PathSeparator)) {
			return fail("conflict", "source outside Git worktree")
		}
		gitDir, e := projectGit(gitRoot, "rev-parse", "--absolute-git-dir")
		if e != nil {
			return fail("failed", "cannot inspect Git worktree")
		}
		gitPath, closeGit, e := openSkillRoot(strings.TrimSpace(string(gitDir)), ".")
		if e != nil {
			return fail("conflict", "Git worktree directory is unsafe")
		}
		defer closeGit()
		release, busy, e := j.acquireIndexLock(filepath.Join(strings.TrimSpace(string(gitDir)), "index.lock"), filepath.Join(gitPath, "index.lock"))
		if busy {
			return fail("pending", "Git index is locked")
		}
		if e != nil {
			retain = true
			return fail("failed", "cannot lock Git index")
		}
		defer release()
		tracked, e := projectGit(gitRoot, "ls-files", "-z", "--", gitRel)
		if e != nil {
			return fail("failed", "cannot inspect tracked paths")
		}
		if len(tracked) != 0 {
			return fail("manual", "source contains tracked files")
		}
	} else if t.Target.Checkout != "" {
		return fail("failed", "cannot inspect tracked paths")
	}
	hash, err := inventory.SkillHash(anchored)
	if err != nil || hash != t.Revision.Version {
		return fail("conflict", "changed since plan")
	}
	sum := sha256.Sum256([]byte(t.Revision.Item))
	itemDir := hex.EncodeToString(sum[:])
	backups, closeBackups, err := openSkillRoot(d.cfg.StateDir, filepath.Join("skill-backups", itemDir))
	if err != nil {
		return fail("failed", "cannot open backup folder")
	}
	defer closeBackups()
	stamp, err := os.MkdirTemp(backups, time.Now().UTC().Format("20060102T150405.000000000Z")+"-")
	if err != nil {
		return fail("failed", "cannot create backup")
	}
	// Keep the full source path beneath the unique backup directory.
	dest := filepath.Join(stamp, strings.TrimPrefix(source, string(os.PathSeparator)))
	if err = os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
		os.RemoveAll(stamp)
		return fail("failed", "cannot create backup")
	}
	// The opened backup directory can be a descriptor path. Store the stable
	// path in the journal so a later daemon can find the preimage.
	j.record.Backup = filepath.Join(d.cfg.StateDir, "skill-backups", itemDir, filepath.Base(stamp), strings.TrimPrefix(source, string(os.PathSeparator)))
	if err = j.advance("backing_up"); err != nil {
		retain = true
		return fail("failed", "skill operation interrupted")
	}
	if err = os.Rename(anchored, dest); err != nil {
		os.RemoveAll(stamp)
		return fail("failed", "cannot move source to backup")
	}
	retain = true
	if err = j.advance("backed_up"); err != nil {
		return fail("failed", "skill operation interrupted")
	}
	hash, err = inventory.SkillHash(dest)
	if err != nil || hash != t.Revision.Version {
		if e := restoreSkillBackup(dest, anchored); e == nil {
			os.RemoveAll(stamp)
			retain = false
		}
		return fail("conflict", "changed since plan")
	}
	entries, err := os.ReadDir(backups)
	if err == nil {
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() > entries[j].Name() })
		for i, e := range entries {
			if i >= 20 && e.IsDir() {
				_ = os.RemoveAll(filepath.Join(backups, e.Name()))
			}
		}
	}
	if err = d.forgetSkill(source); err != nil {
		return fail("failed", "cannot update skill manifest or Git exclude")
	}
	if err = j.advance("forgotten"); err != nil {
		return fail("failed", "skill operation interrupted")
	}
	retain = false
	return fail("applied", "")
}

func restoreSkillBackup(backup, source string) error {
	return renameSkillNoReplace(backup, source)
}

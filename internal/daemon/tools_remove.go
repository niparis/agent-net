package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

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
	root := d.skillRootFor(t, source)
	rel, err := filepath.Rel(root, source)
	if err != nil || !filepath.IsAbs(root) || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fail("conflict", "source outside authorized root")
	}
	parent, err := makeSkillRoot(root, filepath.Dir(rel))
	if err != nil {
		return fail("conflict", "source path is unsafe")
	}
	defer parent.Close()
	anchored := skillPath{parent, t.Name}
	info, err := anchored.lstat()
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
	if t.Target.OwnedOnly {
		owned, err := d.projectSkillOwned(source, t.Revision.Item)
		if err != nil {
			return fail("failed", "cannot read manifest")
		}
		if !owned {
			return fail("conflict", "not installed by agent-net")
		}
	}
	if err = walkSkillAt(anchored, func(_ string, e fs.DirEntry, err error) error {
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
		gitPath, e := makeSkillRoot(strings.TrimSpace(string(gitDir)), ".")
		if e != nil {
			return fail("conflict", "Git worktree directory is unsafe")
		}
		defer gitPath.Close()
		release, busy, e := j.acquireIndexLock(filepath.Join(strings.TrimSpace(string(gitDir)), "index.lock"), skillPath{gitPath, "index.lock"})
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
	hash, err := skillHashAt(anchored)
	if err != nil || hash != t.Revision.Version {
		return fail("conflict", "changed since plan")
	}
	state, reason, keep := d.backupAndForget(j, anchored, source, t.Revision.Item, t.Revision.Version)
	retain = keep
	return fail(state, reason)
}

// backupAndForget moves a verified source directory into the private backup
// folder and drops its manifest entry. The caller has already written the
// journal record and must keep it when keep is true.
func (d *Daemon) backupAndForget(j *skillJournal, anchored skillPath, source, item, version string) (state, reason string, keep bool) {
	fail := func(state, reason string) (string, string, bool) { return state, reason, keep }
	sum := sha256.Sum256([]byte(item))
	itemDir := hex.EncodeToString(sum[:])
	backups, err := makeSkillRoot(d.cfg.StateDir, filepath.Join("skill-backups", itemDir))
	if err != nil {
		return fail("failed", "cannot open backup folder")
	}
	defer backups.Close()
	stamp, err := skillTempDir(backups, time.Now().UTC().Format("20060102T150405.000000000Z")+"-")
	if err != nil {
		return fail("failed", "cannot create backup")
	}
	// Keep the full source path beneath the unique backup directory.
	dest := stamp.join(strings.TrimPrefix(source, string(os.PathSeparator)))
	if err = (skillPath{dest.root, filepath.Dir(dest.name)}).mkdirAll(); err != nil {
		stamp.removeAll()
		return fail("failed", "cannot create backup")
	}
	// Store the stable path so a later daemon can find the preimage.
	j.record.Backup = filepath.Join(d.cfg.StateDir, "skill-backups", itemDir, filepath.Base(stamp.name), strings.TrimPrefix(source, string(os.PathSeparator)))
	if err = j.advance("backing_up"); err != nil {
		keep = true
		return fail("failed", "skill operation interrupted")
	}
	stableStamp := filepath.Join(d.cfg.StateDir, "skill-backups", itemDir, filepath.Base(stamp.name))
	if err = skillRename(anchored, dest); err != nil {
		if !errors.Is(err, syscall.EXDEV) {
			stamp.removeAll()
			return fail("failed", "cannot move source to backup")
		}
		switch st, rs := d.moveAcrossFilesystems(j, anchored, dest, stamp, stableStamp, version); st {
		case "":
		case "interrupted":
			keep = true
			return fail("failed", "skill operation interrupted")
		default:
			if st == "conflict" {
				stamp.removeAll()
			}
			return fail(st, rs)
		}
	}
	keep = true
	if err = j.advance("backed_up"); err != nil {
		return fail("failed", "skill operation interrupted")
	}
	hash, err := skillHashAt(dest)
	if err != nil || hash != version {
		if e := renameSkillAt(dest, anchored, true); e == nil {
			stamp.removeAll()
			keep = false
		}
		return fail("conflict", "changed since plan")
	}
	lockdownSkillBackupAt(stamp)
	d.pruneSkillBackups()
	if err = d.forgetSkill(source); err != nil {
		return fail("failed", "cannot update skill manifest or Git exclude")
	}
	if err = j.advance("forgotten"); err != nil {
		return fail("failed", "skill operation interrupted")
	}
	keep = false
	return fail("applied", "")
}

func restoreSkillBackup(backup, source string) error {
	return renameSkillNoReplace(backup, source)
}

// skillRename moves a source directory into the backup folder. Tests replace
// it to simulate a backup folder on another filesystem.
var skillRename = func(from, to skillPath) error { return renameSkillAt(from, to, false) }

// moveAcrossFilesystems backs up a source that cannot be renamed into the
// backup folder: copy to a staging folder beside the destination, verify the
// hash, rename into place, then remove the source only if it is unchanged.
// Every step is journaled. It returns an empty state on success.
func (d *Daemon) moveAcrossFilesystems(j *skillJournal, source, dest, stamp skillPath, stableStamp, hash string) (state, reason string) {
	bundle, err := packSkillAt(source)
	if err != nil {
		stamp.removeAll()
		return "failed", "cannot copy source to backup"
	}
	stage := stamp.join(skillStagePrefix + filepath.Base(j.path))
	j.record.Stage = filepath.Join(stableStamp, skillStagePrefix+filepath.Base(j.path))
	if err = j.advance("copying"); err != nil {
		return "interrupted", ""
	}
	if err = stage.mkdir(); err == nil {
		err = unpackSkillAt(bundle, stage)
	}
	if err != nil {
		stamp.removeAll()
		return "failed", "cannot copy source to backup"
	}
	if got, e := skillHashAt(stage); e != nil || got != hash {
		stamp.removeAll()
		return "conflict", "changed since plan"
	}
	if err = j.advance("copied"); err != nil {
		return "interrupted", ""
	}
	if err = renameSkillAt(stage, dest, false); err != nil {
		stamp.removeAll()
		return "failed", "cannot move source to backup"
	}
	if err = j.advance("backup_copied"); err != nil {
		return "interrupted", ""
	}
	// Guarded removal: the source must still be what the backup holds.
	if got, e := skillHashAt(source); e != nil || got != hash {
		stamp.removeAll()
		return "conflict", "changed since plan"
	}
	if err = source.removeAll(); err != nil {
		return "interrupted", ""
	}
	return "", ""
}

// skillRootFor returns the authorized root that must contain a source path.
func (d *Daemon) skillRootFor(t protocol.ToolTarget, source string) string {
	root := d.cfg.InventoryHome
	if t.Target.Checkout != "" {
		root = filepath.Clean(t.Target.Checkout)
	} else if filepath.IsAbs(d.cfg.CodexHome) {
		codexRel, e := filepath.Rel(filepath.Join(d.cfg.CodexHome, "skills"), source)
		if e == nil && codexRel != ".." && !strings.HasPrefix(codexRel, ".."+string(os.PathSeparator)) {
			root = d.cfg.CodexHome
		}
	}
	return root
}

// lockdownSkillBackup makes a backup readable by the owner only: folders 0700
// and files 0600 (0700 when the file was executable). Backups can hold secrets.
func lockdownSkillBackup(root string) {
	_ = filepath.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
		if err != nil || e.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if e.IsDir() {
			_ = os.Chmod(p, 0700)
		} else if info, err := e.Info(); err == nil {
			if info.Mode().Perm()&0100 != 0 {
				_ = os.Chmod(p, 0700)
			} else {
				_ = os.Chmod(p, 0600)
			}
		}
		return nil
	})
}

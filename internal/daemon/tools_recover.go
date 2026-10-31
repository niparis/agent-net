package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
)

const skillStagePrefix = ".agent-net-stage-"

func (d *Daemon) logSkill(format string, args ...any) {
	if d.log != nil {
		d.log.Printf(format, args...)
	}
}

// skillConflict returns the reason an interrupted operation left this path
// in a state the daemon will not touch, or "".
func (d *Daemon) skillConflict(path string) string {
	d.toolsMu.Lock()
	defer d.toolsMu.Unlock()
	return d.skillConflicts[filepath.Clean(path)]
}

func (d *Daemon) setSkillConflict(path, reason string) {
	d.toolsMu.Lock()
	defer d.toolsMu.Unlock()
	if d.skillConflicts == nil {
		d.skillConflicts = map[string]string{}
	}
	d.skillConflicts[filepath.Clean(path)] = reason
}

// recoverSkillJournal finishes or undoes skill operations that a previous
// daemon did not complete. Anything whose content no longer matches the
// journal is left alone and reported as a conflict. It returns the conflict
// lines it logged.
func (d *Daemon) recoverSkillJournal() []string {
	dir := filepath.Join(d.cfg.StateDir, "skill-journal")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	d.toolsMu.Lock()
	d.skillConflicts = map[string]string{}
	d.toolsMu.Unlock()
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	var conflicts []string
	for _, name := range names {
		path := filepath.Join(dir, name)
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var rec skillJournalRecord
		if json.Unmarshal(b, &rec) != nil {
			d.logSkill("skill journal %s is unreadable and was left alone", name)
			continue
		}
		if removeOwnedIndexLock(rec.Lock, rec.LockToken) {
			d.logSkill("skill journal: removed Git index lock left by an interrupted %s of %s", rec.Operation, rec.Target)
		}
		reason := d.recoverSkillRecord(rec)
		if reason != "" {
			line := fmt.Sprintf("skill journal conflict: %s of %s stopped at %s: %s", rec.Operation, rec.Target, rec.Step, reason)
			d.logSkill("%s", line)
			conflicts = append(conflicts, line)
			d.setSkillConflict(rec.Target, "interrupted operation: "+reason)
			continue
		}
		j := &skillJournal{path: path, record: rec}
		_ = j.finish()
	}
	return conflicts
}

func skillHashIs(path, want string) bool {
	h, err := inventory.SkillHash(path)
	return err == nil && h == want
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// recoverSkillRecord returns "" when the record is resolved, or a conflict reason.
func (d *Daemon) recoverSkillRecord(rec skillJournalRecord) string {
	switch rec.Operation {
	case "copy":
		return d.recoverCopy(rec)
	case "remove":
		return d.recoverRemove(rec)
	case "restore":
		if rec.Stage != "" && strings.HasPrefix(filepath.Base(rec.Stage), skillStagePrefix) && exists(rec.Stage) {
			if err := os.RemoveAll(rec.Stage); err != nil {
				return "cannot delete staged copy"
			}
		}
		return ""
	case "project-copy":
		if rec.Exclude != "" && rec.ExcludeLine != "" {
			_ = d.recordProjectExclude(rec.Target, rec.Exclude, rec.ExcludeLine, true)
		}
		return ""
	}
	return ""
}

func (d *Daemon) recoverCopy(rec skillJournalRecord) string {
	if rec.Stage != "" && strings.HasPrefix(filepath.Base(rec.Stage), skillStagePrefix) && exists(rec.Stage) {
		// The staged copy was never renamed into place.
		if err := os.RemoveAll(rec.Stage); err != nil {
			return "cannot delete staged copy"
		}
		return ""
	}
	switch rec.Step {
	case "staged", "installed":
		if !exists(rec.Target) {
			return ""
		}
		if !skillHashIs(rec.Target, rec.AfterHash) {
			return "installed content does not match the journal"
		}
		t := protocol.ToolTarget{Revision: protocol.ToolRevision{Item: rec.Item, Revision: rec.Revision, Version: rec.AfterHash}}
		if err := d.recordSkill(t, rec.Target, true); err != nil {
			return "cannot record manifest"
		}
	}
	return ""
}

func (d *Daemon) recoverRemove(rec skillJournalRecord) string {
	switch rec.Step {
	case "prepared", "locking", "locked":
		return ""
	case "forgotten":
		return ""
	case "backing_up", "copying", "copied":
		// The source stays authoritative until the backup is complete.
		if rec.Stage != "" && strings.HasPrefix(filepath.Base(rec.Stage), skillStagePrefix) && exists(rec.Stage) {
			_ = os.RemoveAll(rec.Stage)
		}
		if exists(rec.Backup) {
			if !skillHashIs(rec.Backup, rec.BeforeHash) {
				return "backup does not match the journal"
			}
			if exists(rec.Target) {
				if !skillHashIs(rec.Target, rec.BeforeHash) {
					return "source changed after backup"
				}
				return d.removeAfterBackup(rec)
			}
			return d.finishForget(rec)
		}
		if exists(rec.Target) {
			// Nothing was moved. Drop the empty backup folder, if any.
			if rec.Backup != "" {
				removeEmptyBackupParents(rec)
			}
			return ""
		}
		return "source and backup are both missing"
	case "backup_copied":
		if !exists(rec.Backup) || !skillHashIs(rec.Backup, rec.BeforeHash) {
			return "backup does not match the journal"
		}
		if exists(rec.Target) {
			if !skillHashIs(rec.Target, rec.BeforeHash) {
				return "source changed after backup"
			}
			return d.removeAfterBackup(rec)
		}
		return d.finishForget(rec)
	case "backed_up":
		if exists(rec.Backup) {
			if !skillHashIs(rec.Backup, rec.BeforeHash) {
				return "backup does not match the journal"
			}
			if exists(rec.Target) && !skillHashIs(rec.Target, rec.BeforeHash) {
				// Someone created new content at the source. Keep it.
				return "source was replaced after backup"
			}
			if exists(rec.Target) {
				return "source reappeared after backup"
			}
			return d.finishForget(rec)
		}
		// The backup was restored to the source.
		if exists(rec.Target) && skillHashIs(rec.Target, rec.BeforeHash) {
			return ""
		}
		return "source and backup are both missing"
	}
	return ""
}

func removeEmptyBackupParents(rec skillJournalRecord) {
	// The backup is <stamp>/<full source path>. Remove only empty folders.
	p := filepath.Dir(rec.Backup)
	rel := strings.TrimPrefix(rec.Target, string(os.PathSeparator))
	stamp := strings.TrimSuffix(rec.Backup, string(os.PathSeparator)+rel)
	for p != stamp && strings.HasPrefix(p, stamp) {
		if os.Remove(p) != nil {
			return
		}
		p = filepath.Dir(p)
	}
	_ = os.Remove(stamp)
}

func (d *Daemon) finishForget(rec skillJournalRecord) string {
	if err := d.forgetSkill(rec.Target); err != nil {
		return "cannot update skill manifest or Git exclude"
	}
	return ""
}

// removeAfterBackup is the guarded source removal: the source goes only when
// it still matches the hash that the backup holds.
func (d *Daemon) removeAfterBackup(rec skillJournalRecord) string {
	if !skillHashIs(rec.Target, rec.BeforeHash) {
		return "source changed after backup"
	}
	if err := os.RemoveAll(rec.Target); err != nil {
		return "cannot remove source"
	}
	return d.finishForget(rec)
}

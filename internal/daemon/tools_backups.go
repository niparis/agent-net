package daemon

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

const (
	skillBackupKeep   = 20
	skillBackupMaxAge = 30 * 24 * time.Hour
)

// skillBackupNow is the clock for backup retention. Tests replace it.
var skillBackupNow = time.Now

// skillBackupTime reads the time from a backup folder name such as
// 20060102T150405.000000000Z-1234.
func skillBackupTime(name string) (time.Time, bool) {
	stamp, _, ok := strings.Cut(name, "Z-")
	if !ok {
		return time.Time{}, false
	}
	t, err := time.Parse("20060102T150405.000000000", stamp)
	return t, err == nil
}

// pruneSkillBackups keeps the newest 20 backups of each item and none older
// than 30 days. A backup that an unfinished journal record names stays.
func (d *Daemon) pruneSkillBackups() {
	root := filepath.Join(d.cfg.StateDir, "skill-backups")
	items, err := os.ReadDir(root)
	if err != nil {
		return
	}
	var pinned []string
	if recs, e := os.ReadDir(filepath.Join(d.cfg.StateDir, "skill-journal")); e == nil {
		for _, r := range recs {
			b, e := os.ReadFile(filepath.Join(d.cfg.StateDir, "skill-journal", r.Name()))
			var rec skillJournalRecord
			if e == nil && json.Unmarshal(b, &rec) == nil && rec.Backup != "" {
				pinned = append(pinned, rec.Backup)
			}
		}
	}
	now := skillBackupNow()
	for _, item := range items {
		if !item.IsDir() {
			continue
		}
		dir := filepath.Join(root, item.Name())
		stamps, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		sort.Slice(stamps, func(i, j int) bool { return stamps[i].Name() > stamps[j].Name() })
		kept := 0
		for _, s := range stamps {
			if !s.IsDir() {
				continue
			}
			path := filepath.Join(dir, s.Name())
			held := false
			for _, p := range pinned {
				if strings.HasPrefix(p, path+string(os.PathSeparator)) {
					held = true
				}
			}
			when, ok := skillBackupTime(s.Name())
			if held || !ok {
				kept++
				continue
			}
			if kept >= skillBackupKeep || now.Sub(when) > skillBackupMaxAge {
				_ = os.RemoveAll(path)
				continue
			}
			kept++
		}
	}
}

// skillBackups lists this machine's backups of an item: time, size and the
// original path. It never reads or returns file content.
func (d *Daemon) skillBackups(item string) []protocol.ToolBackup {
	dir := d.skillBackupDir(item)
	stamps, err := os.ReadDir(dir)
	out := []protocol.ToolBackup{}
	if err != nil {
		return out
	}
	sort.Slice(stamps, func(i, j int) bool { return stamps[i].Name() > stamps[j].Name() })
	for _, s := range stamps {
		when, ok := skillBackupTime(s.Name())
		if !s.IsDir() || !ok {
			continue
		}
		b := protocol.ToolBackup{Time: when.UnixNano()}
		_ = filepath.WalkDir(filepath.Join(dir, s.Name()), func(p string, e fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if e.IsDir() && b.Source == "" && p != filepath.Join(dir, s.Name()) {
				// The first directory that holds a SKILL.md is the source.
				if _, e := os.Lstat(filepath.Join(p, "SKILL.md")); e == nil {
					b.Source = string(os.PathSeparator) + strings.TrimPrefix(strings.TrimPrefix(p, filepath.Join(dir, s.Name())), string(os.PathSeparator))
				}
			}
			if !e.IsDir() {
				if info, e := e.Info(); e == nil {
					b.Size += info.Size()
				}
			}
			return nil
		})
		out = append(out, b)
	}
	return out
}

// withLocalBackups adds this machine's backups to a history answer.
func (d *Daemon) withLocalBackups(res any, item string) any {
	raw, err := json.Marshal(res)
	if err != nil {
		return res
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return res
	}
	b, err := json.Marshal(d.skillBackups(item))
	if err != nil {
		return res
	}
	m["backups"] = b
	return m
}

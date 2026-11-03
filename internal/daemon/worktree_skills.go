package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// copyWorktreeSkills copies only managed or Git-ignored project skills.
func (d *Daemon) copyWorktreeSkills(ctx context.Context, source, dest string) error {
	owned := map[string]bool{}
	b, err := os.ReadFile(filepath.Join(d.cfg.StateDir, "skill-manifest.json"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil {
		var entries []skillManifestEntry
		if err := json.Unmarshal(b, &entries); err != nil {
			return err
		}
		for _, e := range entries {
			if e.Owned {
				owned[filepath.Clean(e.Path)] = true
			}
		}
	}
	count := 0
	for _, root := range []string{".claude/skills", ".agents/skills"} {
		if !safeSkillRoot(source, root) || !safeSkillRoot(dest, root) {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(source, root))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			rel := filepath.Join(root, entry.Name())
			src := filepath.Join(source, rel)
			dst := filepath.Join(dest, rel)
			if _, err := os.Lstat(dst); err == nil {
				continue
			} else if !os.IsNotExist(err) {
				return err
			}
			if !owned[filepath.Clean(src)] {
				if _, err := gitOut(ctx, source, "check-ignore", "--", rel+"/"); err != nil {
					continue
				}
			}
			if err := copySkillTree(src, dst); err != nil {
				return fmt.Errorf("copy skill %s: %w", rel, err)
			}
			count++
		}
	}
	d.log.Printf("worktree %s: copied %d project skills", dest, count)
	return nil
}

func copySkillTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
		if err != nil {
			return err
		}
		_, err = io.Copy(out, in)
		closeErr := out.Close()
		if err != nil {
			return err
		}
		return closeErr
	})
}

// Existing root components must be directories, never symlinks.
func safeSkillRoot(base, root string) bool {
	for _, part := range []string{filepath.Dir(root), root} {
		info, err := os.Lstat(filepath.Join(base, part))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil || !info.IsDir() {
			return false
		}
	}
	return true
}

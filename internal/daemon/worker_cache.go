package daemon

import (
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// trimGoCache deletes old regular files inside the cache. Root confines all
// operations even if a cache entry is replaced by a symlink while walking.
func trimGoCache(path string, before time.Time) (int64, error) {
	root, err := os.OpenRoot(path)
	if err != nil {
		return 0, err
	}
	defer root.Close()
	var freed int64
	err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.ModTime().Before(before) {
			return nil
		}
		if err := root.Chmod(name, info.Mode().Perm()|0200); err != nil {
			return err
		}
		if err := root.Remove(name); err != nil {
			return err
		}
		freed += info.Size()
		return nil
	})
	return freed, err
}

func trimGoTemp(path string, before time.Time, inUse func(string) (bool, error)) (int64, error) {
	root, err := os.OpenRoot(path)
	if err != nil {
		return 0, err
	}
	defer root.Close()
	entries, err := os.ReadDir(path)
	if err != nil {
		return 0, err
	}
	var freed int64
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return freed, err
		}
		if !info.ModTime().Before(before) {
			continue
		}
		used, err := inUse(filepath.Join(path, entry.Name()))
		if err != nil {
			return freed, err
		}
		if used {
			continue
		}
		var size int64
		err = fs.WalkDir(root.FS(), entry.Name(), func(name string, e fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			i, err := e.Info()
			if err != nil {
				return err
			}
			if e.Type().IsRegular() {
				size += i.Size()
			}
			if e.IsDir() {
				return root.Chmod(name, i.Mode().Perm()|0700)
			}
			if e.Type().IsRegular() {
				return root.Chmod(name, i.Mode().Perm()|0200)
			}
			return nil
		})
		if err != nil {
			return freed, err
		}
		if err := root.RemoveAll(entry.Name()); err != nil {
			return freed, err
		}
		freed += size
	}
	return freed, nil
}

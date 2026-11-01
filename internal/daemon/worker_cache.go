package daemon

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// openCacheRoot rejects symlink components and verifies the opened directory
// against the path, so a replaced cache root cannot redirect removal.
func openCacheRoot(path string) (*os.Root, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) == string(filepath.Separator) {
		return nil, fmt.Errorf("invalid cache root: %s", path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	volume := filepath.VolumeName(abs)
	base := volume + string(filepath.Separator)
	root, err := os.OpenRoot(base)
	if err != nil {
		return nil, err
	}
	for _, part := range strings.Split(strings.TrimPrefix(abs, base), string(filepath.Separator)) {
		if part == "" {
			continue
		}
		info, err := root.Lstat(part)
		if err != nil {
			root.Close()
			return nil, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			root.Close()
			return nil, fmt.Errorf("cache path has a non-directory or symlink: %s", path)
		}
		next, err := root.OpenRoot(part)
		if err != nil {
			root.Close()
			return nil, err
		}
		opened, err := next.Stat(".")
		if err != nil || !os.SameFile(info, opened) {
			next.Close()
			root.Close()
			return nil, fmt.Errorf("cache path changed while opening: %s", path)
		}
		root.Close()
		root = next
	}
	return root, nil
}

func trimFolder(path string, before time.Time, inUse func(string) (bool, error)) (int64, error) {
	root, err := openCacheRoot(path)
	if err != nil {
		return 0, err
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return 0, err
	}
	entries, err := dir.ReadDir(-1)
	dir.Close()
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

		// A build can update a nested file without changing the top-level
		// directory mtime. Check every descendant before removing the entry.
		recent := errors.New("entry is still active")
		var size int64
		err = fs.WalkDir(root.FS(), entry.Name(), func(name string, e fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			i, err := e.Info()
			if err != nil {
				return err
			}
			if !i.ModTime().Before(before) {
				return recent
			}
			if e.Type().IsRegular() {
				if diskHardlinked(i) {
					return fmt.Errorf("temporary entry contains a hard link: %s", name)
				}
				size += i.Size()
			}
			return nil
		})
		if errors.Is(err, recent) {
			continue
		}
		if err != nil {
			return freed, err
		}
		// A positive result protects the entry. Permission errors and other
		// inconclusive process checks do not prevent age-based cleanup.
		if used, _ := inUse(filepath.Join(path, entry.Name())); used {
			continue
		}
		err = fs.WalkDir(root.FS(), entry.Name(), func(name string, e fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			i, err := e.Info()
			if err != nil {
				return err
			}
			// Recheck the age before changing permissions or deleting the entry.
			if !i.ModTime().Before(before) {
				return recent
			}
			if e.IsDir() {
				return root.Chmod(name, i.Mode().Perm()|0700)
			}
			if e.Type().IsRegular() {
				if diskHardlinked(i) {
					return fmt.Errorf("temporary entry contains a hard link: %s", name)
				}
				return root.Chmod(name, i.Mode().Perm()|0200)
			}
			return nil
		})
		if errors.Is(err, recent) {
			continue
		}
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

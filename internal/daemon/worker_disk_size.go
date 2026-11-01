package daemon

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

type diskFolder struct {
	path string
	size int64
}

// largestDiskFolders counts files below each top-level folder, without
// following symlinks or crossing into another filesystem.
func largestDiskFolders(root string) []diskFolder {
	return largestDiskFoldersContext(context.Background(), root)
}

func largestDiskFoldersContext(ctx context.Context, root string) []diskFolder {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	dev, ok := diskDevice(root)
	if !ok {
		return nil
	}
	var folders []diskFolder
	for _, e := range entries {
		if ctx.Err() != nil {
			break
		}
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(root, e.Name())
		var total int64
		_ = filepath.WalkDir(path, func(p string, ent fs.DirEntry, err error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				return nil
			}
			if ent.IsDir() {
				d, ok := diskDevice(p)
				if !ok || d != dev {
					return fs.SkipDir
				}
				return nil
			}
			if ent.Type().IsRegular() {
				if info, err := ent.Info(); err == nil {
					total += info.Size()
				}
			}
			return nil
		})
		folders = append(folders, diskFolder{path, total})
	}
	sort.Slice(folders, func(i, j int) bool {
		if folders[i].size == folders[j].size {
			return folders[i].path < folders[j].path
		}
		return folders[i].size > folders[j].size
	})
	if len(folders) > 3 {
		folders = folders[:3]
	}
	return folders
}

func filesystemRoot(path string) string {
	path, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(path); err == nil {
			break
		}
		next := filepath.Dir(path)
		if next == path {
			return ""
		}
		path = next
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	dev, ok := diskDevice(path)
	if !ok {
		return ""
	}
	for {
		parent := filepath.Dir(path)
		if parent == path {
			return path
		}
		d, ok := diskDevice(parent)
		if !ok || d != dev {
			return path
		}
		path = parent
	}
}

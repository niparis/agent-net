// Package inventory discovers local harness resources without executing them.
package inventory

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const maxFiles = 10000
const maxBytes = 64 << 20

// SkillHash hashes a sorted manifest of paths, file contents, executable bits,
// and in-tree symlink targets. It refuses special files and oversized bundles.
func SkillHash(root string) (string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("skill root is not a directory")
	}
	dir, err := os.OpenRoot(root)
	if err != nil {
		return "", err
	}
	defer dir.Close()
	var paths []string
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		if len(paths) >= maxFiles {
			return fmt.Errorf("skill exceeds file count limit")
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(paths)
	h := sha256.New()
	var total int64
	write := func(b []byte) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(b)))
		h.Write(n[:])
		h.Write(b)
	}
	for _, path := range paths {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return "", err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return "", err
		}
		write([]byte(filepath.ToSlash(rel)))
		switch {
		case info.Mode().IsRegular():
			total += info.Size()
			if total > maxBytes || info.Size() < 0 {
				return "", fmt.Errorf("skill exceeds size limit")
			}
			f, err := dir.Open(rel)
			if err != nil {
				return "", err
			}
			opened, err := f.Stat()
			if err != nil || !os.SameFile(info, opened) {
				f.Close()
				return "", fmt.Errorf("skill changed during scan")
			}
			data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
			closeErr := f.Close()
			if err != nil {
				return "", err
			}
			if closeErr != nil {
				return "", closeErr
			}
			if int64(len(data)) != info.Size() {
				return "", fmt.Errorf("skill changed during scan")
			}
			write([]byte("file"))
			write([]byte{byte(info.Mode() & 0111)})
			write(data)
		case info.IsDir():
			write([]byte("directory"))
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return "", err
			}
			if filepath.IsAbs(target) {
				return "", fmt.Errorf("absolute symlink")
			}
			resolved, err := filepath.EvalSymlinks(path)
			if err != nil {
				return "", fmt.Errorf("invalid symlink")
			}
			relative, err := filepath.Rel(root, resolved)
			if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				return "", fmt.Errorf("escaping symlink")
			}
			write([]byte("symlink"))
			write([]byte(filepath.ToSlash(target)))
		default:
			return "", fmt.Errorf("special file in skill")
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

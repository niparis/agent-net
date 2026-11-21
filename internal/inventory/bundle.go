package inventory

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// MaxArchiveBytes is the packed size one skill may have in the skill archive
// (decision 0169). It is the scan's 64 MiB of file data, with the tar headers
// counted too. The archive moves a skill in parts, so this can be more than a
// frame holds.
const MaxArchiveBytes = 64 << 20

// MaxArchiveEntries is the scan's file count (maxFiles in hash.go).
const MaxArchiveEntries = 10000

// ErrOverLimit says a bundle is over its limit. The callers show its text.
var ErrOverLimit = errors.New("skill exceeds bundle limit")

// BundleLimit bounds one bundle: its packed bytes and its entries.
type BundleLimit struct {
	Bytes   int64
	Entries int
}

// ArchiveLimit is what one skill in the skill archive allows (decision 0169).
var ArchiveLimit = BundleLimit{Bytes: MaxArchiveBytes, Entries: MaxArchiveEntries}

// PackSkillLimit is PackSkill with the given limit.
func PackSkillLimit(root string, lim BundleLimit) ([]byte, error) {
	info, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("skill root is not a directory")
	}
	dir, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	return PackSkillRootLimit(dir, lim)
}

// PackSkillRootLimit is PackSkillRoot with the given limit.
func PackSkillRootLimit(dir *os.Root, lim BundleLimit) ([]byte, error) {
	var out bytes.Buffer
	tw := tar.NewWriter(&out)
	count := 0
	var total int64
	err := fs.WalkDir(dir.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == "." {
			return nil
		}
		count++
		if count > lim.Entries {
			return errors.New("skill exceeds entry limit")
		}
		info, e := dir.Lstat(name)
		if e != nil {
			return e
		}
		h := &tar.Header{Name: name, Mode: 0600}
		switch {
		case info.IsDir():
			h.Typeflag = tar.TypeDir
			h.Mode = 0700
		case info.Mode().IsRegular():
			h.Typeflag = tar.TypeReg
			h.Size = info.Size()
			h.Mode = 0600 | int64(info.Mode()&0111)
			total += h.Size
			if h.Size < 0 || total > lim.Bytes {
				return ErrOverLimit
			}
		case info.Mode()&os.ModeSymlink != 0:
			h.Typeflag = tar.TypeSymlink
			h.Linkname, e = dir.Readlink(name)
			if e != nil {
				return e
			}
			if filepath.IsAbs(h.Linkname) {
				return errors.New("absolute symlink")
			}
			if _, e = dir.Stat(name); e != nil {
				return errors.New("invalid or escaping symlink")
			}
		default:
			return errors.New("special file in skill")
		}
		if e = tw.WriteHeader(h); e != nil {
			return e
		}
		if h.Typeflag == tar.TypeReg {
			f, e := dir.Open(name)
			if e != nil {
				return e
			}
			actual, e := f.Stat()
			if e != nil || !os.SameFile(info, actual) {
				f.Close()
				return errors.New("skill changed during import")
			}
			n, e := io.Copy(tw, io.LimitReader(f, h.Size+1))
			closeErr := f.Close()
			if e != nil {
				return e
			}
			if closeErr != nil {
				return closeErr
			}
			if n != h.Size {
				return errors.New("skill changed during import")
			}
		}
		if int64(out.Len()) > lim.Bytes {
			return ErrOverLimit
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err = tw.Close(); err != nil {
		return nil, err
	}
	if int64(out.Len()) > lim.Bytes {
		return nil, ErrOverLimit
	}
	return out.Bytes(), nil
}

// UnpackSkillLimit is UnpackSkill with the given limit.
func UnpackSkillLimit(data []byte, dest string, lim BundleLimit) error {
	if int64(len(data)) > lim.Bytes {
		return ErrOverLimit
	}
	root, err := os.OpenRoot(dest)
	if err != nil {
		return err
	}
	defer root.Close()
	return UnpackSkillRootLimit(data, root, lim)
}

// UnpackSkillRootLimit is UnpackSkillRoot with the given limit.
func UnpackSkillRootLimit(data []byte, root *os.Root, lim BundleLimit) error {
	if int64(len(data)) > lim.Bytes {
		return ErrOverLimit
	}
	tr := tar.NewReader(bytes.NewReader(data))
	seen := map[string]bool{}
	var links []string
	var total int64
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return errors.New("invalid skill bundle")
		}
		name := h.Name
		if !fs.ValidPath(name) || name == "." || strings.Contains(name, "\\") || seen[name] {
			return errors.New("invalid skill path")
		}
		seen[name] = true
		if len(seen) > lim.Entries {
			return errors.New("skill exceeds entry limit")
		}
		// Require parents to be real directories, never symlinks.
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			info, e := root.Lstat(parent)
			if e != nil || !info.IsDir() {
				return errors.New("invalid skill parent")
			}
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if e = root.Mkdir(name, 0700); e != nil {
				return fmt.Errorf("invalid skill directory: %w", e)
			}
		case tar.TypeReg, tar.TypeRegA:
			total += h.Size
			if h.Size < 0 || total > lim.Bytes {
				return ErrOverLimit
			}
			f, e := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if e != nil {
				return e
			}
			n, e := io.Copy(f, io.LimitReader(tr, h.Size+1))
			if ce := f.Close(); e == nil {
				e = ce
			}
			if e != nil || n != h.Size {
				return errors.New("invalid skill file")
			}
			if e = root.Chmod(name, 0600|os.FileMode(h.Mode)&0111); e != nil {
				return e
			}
		case tar.TypeSymlink:
			if h.Linkname == "" || filepath.IsAbs(h.Linkname) || strings.Contains(h.Linkname, "\\") {
				return errors.New("invalid symlink")
			}
			if e = root.Symlink(h.Linkname, name); e != nil {
				return e
			}
			links = append(links, name)
		default:
			return errors.New("unsupported skill file type")
		}
	}
	for _, name := range links {
		if _, err := root.Stat(name); err != nil {
			return errors.New("invalid or escaping symlink")
		}
	}
	return nil
}

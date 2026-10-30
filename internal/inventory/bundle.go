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

// MaxBundleBytes bounds transfer through the local socket and gateway link.
const MaxBundleBytes = 2 << 20

// MaxBundleEntries also bounds archive extraction work.
const MaxBundleEntries = 2048

// PackSkill reads a bounded snapshot without following links out of root.
func PackSkill(root string) ([]byte, error) {
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
	var out bytes.Buffer
	tw := tar.NewWriter(&out)
	count := 0
	var total int64
	err = fs.WalkDir(dir.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == "." {
			return nil
		}
		count++
		if count > MaxBundleEntries {
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
			if h.Size < 0 || total > MaxBundleBytes {
				return errors.New("skill exceeds bundle limit")
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
		if out.Len() > MaxBundleBytes {
			return errors.New("skill exceeds bundle limit")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err = tw.Close(); err != nil {
		return nil, err
	}
	if out.Len() > MaxBundleBytes {
		return nil, errors.New("skill exceeds bundle limit")
	}
	return out.Bytes(), nil
}

// UnpackSkill accepts only relative paths, regular files, directories and safe links.
// The destination must be a new empty private directory.
func UnpackSkill(data []byte, dest string) error {
	if len(data) > MaxBundleBytes {
		return errors.New("skill exceeds bundle limit")
	}
	root, err := os.OpenRoot(dest)
	if err != nil {
		return err
	}
	defer root.Close()
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
		if len(seen) > MaxBundleEntries {
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
			if h.Size < 0 || total > MaxBundleBytes {
				return errors.New("skill exceeds bundle limit")
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
		if _, err = root.Stat(name); err != nil {
			return errors.New("invalid or escaping symlink")
		}
	}
	return nil
}

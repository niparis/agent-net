package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/niparis/agent-net/internal/inventory"
)

// skillPath keeps every filesystem operation relative to its opened root.
// name is never a descriptor path or an absolute path.
type skillPath struct {
	root *os.Root
	name string
}

func (p skillPath) join(names ...string) skillPath {
	return skillPath{p.root, filepath.Join(append([]string{p.name}, names...)...)}
}
func (p skillPath) lstat() (os.FileInfo, error) { return p.root.Lstat(p.name) }
func (p skillPath) removeAll() error            { return p.root.RemoveAll(p.name) }
func (p skillPath) mkdir() error                { return p.root.Mkdir(p.name, 0700) }
func (p skillPath) mkdirAll() error             { return p.root.MkdirAll(p.name, 0700) }
func (p skillPath) openRoot() (*os.Root, error) {
	info, err := p.lstat()
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("skill path is not a directory")
	}
	r, err := p.root.OpenRoot(p.name)
	if err != nil {
		return nil, err
	}
	if opened, e := r.Stat("."); e != nil || !os.SameFile(info, opened) {
		r.Close()
		return nil, errors.New("skill directory changed")
	}
	return r, nil
}
func skillHashAt(p skillPath) (string, error) {
	r, err := p.openRoot()
	if err != nil {
		return "", err
	}
	defer r.Close()
	return inventory.SkillHashRoot(r)
}
func packSkillAt(p skillPath) ([]byte, error) {
	r, err := p.openRoot()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return inventory.PackSkillRoot(r)
}
func unpackSkillAt(data []byte, p skillPath) error {
	r, err := p.openRoot()
	if err != nil {
		return err
	}
	defer r.Close()
	return inventory.UnpackSkillRoot(data, r)
}
func walkSkillAt(p skillPath, fn fs.WalkDirFunc) error {
	r, err := p.openRoot()
	if err != nil {
		return err
	}
	defer r.Close()
	return fs.WalkDir(r.FS(), ".", fn)
}

// makeSkillRoot refuses symlink components and pins each directory as it walks.
func makeSkillRoot(base, rel string) (*os.Root, error) {
	info, err := os.Lstat(base)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("unsafe skill root")
	}
	r, err := os.OpenRoot(base)
	if err != nil {
		return nil, err
	}
	if opened, e := r.Stat("."); e != nil || !os.SameFile(info, opened) {
		r.Close()
		return nil, errors.New("skill root changed")
	}
	if !filepath.IsLocal(rel) {
		r.Close()
		return nil, errors.New("unsafe skill path")
	}
	for _, part := range strings.Split(filepath.Clean(rel), string(os.PathSeparator)) {
		if part == "." {
			continue
		}
		if err = r.Mkdir(part, 0700); err != nil && !os.IsExist(err) {
			r.Close()
			return nil, err
		}
		info, err = r.Lstat(part)
		if err != nil || !info.IsDir() {
			r.Close()
			return nil, errors.New("unsafe skill directory")
		}
		next, e := r.OpenRoot(part)
		r.Close()
		if e != nil {
			return nil, e
		}
		if opened, e := next.Stat("."); e != nil || !os.SameFile(info, opened) {
			next.Close()
			return nil, errors.New("skill directory changed")
		}
		r = next
	}
	return r, nil
}

func skillTempName(prefix string) string {
	var b [16]byte
	// crypto/rand.Read cannot fail on supported Go platforms.
	_, _ = rand.Read(b[:])
	return prefix + hex.EncodeToString(b[:])
}
func skillTempDir(root *os.Root, prefix string) (skillPath, error) {
	p := skillPath{root, skillTempName(prefix)}
	return p, p.mkdir()
}
func skillTempFile(root *os.Root, prefix string) (*os.File, string, error) {
	name := skillTempName(prefix)
	f, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	return f, name, err
}

func skillTreeHasSymlinkAt(p skillPath) bool {
	return walkSkillAt(p, func(_ string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.Type()&os.ModeSymlink != 0 {
			return fs.ErrInvalid
		}
		return nil
	}) != nil
}

func lockdownSkillBackupAt(p skillPath) {
	r, err := p.openRoot()
	if err != nil {
		return
	}
	defer r.Close()
	_ = fs.WalkDir(r.FS(), ".", func(name string, e fs.DirEntry, err error) error {
		if err != nil || e.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if e.IsDir() {
			_ = r.Chmod(name, 0700)
		} else if info, err := e.Info(); err == nil {
			mode := os.FileMode(0600)
			if info.Mode().Perm()&0100 != 0 {
				mode = 0700
			}
			_ = r.Chmod(name, mode)
		}
		return nil
	})
}

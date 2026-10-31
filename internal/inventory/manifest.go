package inventory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/niparis/agent-net/internal/protocol"
)

// skillManifestRoot reads only bounded file digests. In-tree links are recorded
// as links, not followed. A nil manifest from an older daemon means unknown.
func skillManifestRoot(dir *os.Root) (*protocol.SkillManifest, error) {
	m := &protocol.SkillManifest{Files: []protocol.SkillFile{}}
	var total int64
	err := fs.WalkDir(dir.FS(), ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == "." {
			return nil
		}
		if skillNoise(filepath.Base(path)) {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if len(m.Files) >= 200 {
			m.TooLarge = true
			return fs.SkipAll
		}
		info, err := dir.Lstat(path)
		if err != nil {
			return err
		}
		h := sha256.New()
		switch {
		case info.Mode().IsRegular():
			total += info.Size()
			if info.Size() < 0 || total > maxBytes {
				return fmt.Errorf("skill exceeds size limit")
			}
			f, err := dir.Open(path)
			if err != nil {
				return err
			}
			actual, err := f.Stat()
			if err != nil || !os.SameFile(info, actual) {
				f.Close()
				return fmt.Errorf("skill changed during scan")
			}
			h.Write([]byte("file\x00"))
			n, err := io.Copy(h, io.LimitReader(f, info.Size()+1))
			closeErr := f.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
			if n != info.Size() {
				return fmt.Errorf("skill changed during scan")
			}
		case info.Mode()&os.ModeSymlink != 0:
			target, err := dir.Readlink(path)
			if err != nil {
				return err
			}
			if filepath.IsAbs(target) {
				return fmt.Errorf("absolute symlink")
			}
			if _, err := dir.Stat(path); err != nil {
				return fmt.Errorf("invalid or escaping symlink")
			}
			h.Write([]byte("symlink\x00"))
			h.Write([]byte(filepath.ToSlash(target)))
		default:
			return fmt.Errorf("special file in skill")
		}
		m.Files = append(m.Files, protocol.SkillFile{Path: path, Size: info.Size(), Fingerprint: hex.EncodeToString(h.Sum(nil))})
		if info.ModTime().After(m.Newest) {
			m.Newest = info.ModTime().UTC()
		}
		b, err := json.Marshal(m)
		if err != nil {
			return err
		}
		if len(b) > 64<<10 {
			m.TooLarge = true
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if m.TooLarge {
		m.Files = nil
		m.Newest = m.Newest.UTC()
	}
	return m, nil
}

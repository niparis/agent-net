package inventory

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxSkillTextBytes bounds owner-requested file previews.
const MaxSkillTextBytes = 64 << 10

// ReadSkillText reads a regular UTF-8 file confined to the skill root.
func ReadSkillText(root, name string) ([]byte, error) {
	if !fs.ValidPath(name) || name == "." || strings.Contains(name, "\\") {
		return nil, errors.New("invalid skill file path")
	}
	dir, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	parts := strings.Split(name, "/")
	for i := range parts {
		info, err := dir.Lstat(path.Join(parts[:i+1]...))
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 || (i < len(parts)-1 && !info.IsDir()) || (i == len(parts)-1 && !info.Mode().IsRegular()) {
			return nil, errors.New("not a regular skill file")
		}
	}
	f, err := dir.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > MaxSkillTextBytes {
		return nil, errors.New("invalid or oversized skill file")
	}
	b, err := io.ReadAll(io.LimitReader(f, MaxSkillTextBytes+1))
	if err != nil || len(b) > MaxSkillTextBytes || !utf8.Valid(b) || strings.ContainsRune(string(b), 0) {
		return nil, errors.New("not a bounded text file")
	}
	for _, r := range string(b) {
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
			return nil, errors.New("not a bounded text file")
		}
	}
	return b, nil
}

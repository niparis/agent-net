package inventory

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"

	"github.com/niparis/agent-net/internal/protocol"
)

// A prompt (slash command) or an agent (subagent) is one Markdown file. It is
// stored and hashed as a bundle of one file, named <name>.md, so the skill
// store, the hash check and the copy code work for it without a second path.

// hashField writes one length-prefixed field, the framing of SkillHash.
func hashField(h hash.Hash, b []byte) {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(b)))
	h.Write(n[:])
	h.Write(b)
}

// FileBundleHash is SkillHash of a folder that holds only the file name with
// this content. execBits is the file's mode & 0111, which SkillHash includes
// unless contentOnly is set.
func FileBundleHash(name string, data []byte, execBits byte, contentOnly bool) string {
	h := sha256.New()
	hashField(h, []byte(name))
	hashField(h, []byte("file"))
	if !contentOnly {
		hashField(h, []byte{execBits})
	}
	hashField(h, data)
	return hex.EncodeToString(h.Sum(nil))
}

// fileFingerprint is the per-file digest of a manifest.
func fileFingerprint(data []byte) string {
	sum := sha256.Sum256(append([]byte("file\x00"), data...))
	return hex.EncodeToString(sum[:])
}

// readBundleFile reads one regular file below root without leaving it. The
// error text names the file and the rule, never its contents.
func readBundleFile(root *os.Root, rel string) ([]byte, os.FileInfo, error) {
	info, err := root.Lstat(rel)
	if err != nil {
		return nil, nil, skillReadError(rel, err)
	}
	if !info.Mode().IsRegular() {
		return nil, nil, errNotRegular
	}
	if info.Size() < 0 || info.Size() > maxBytes {
		return nil, nil, skillSizeError(rel, info.Size(), info.Size())
	}
	f, err := root.Open(rel)
	if err != nil {
		return nil, nil, skillReadError(rel, err)
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, nil, fmt.Errorf("file changed during scan (%s)", rel)
	}
	data, err := io.ReadAll(io.LimitReader(f, info.Size()+1))
	if err != nil {
		return nil, nil, skillReadError(rel, err)
	}
	if int64(len(data)) != info.Size() {
		return nil, nil, fmt.Errorf("file changed during scan (%s)", rel)
	}
	return data, info, nil
}

var errNotRegular = errors.New("not a regular file")

// fileObservation is what a scan records about one prompt or agent file.
type fileObservation struct {
	Hash, Fingerprint, Description string
	Manifest                       *protocol.SkillManifest
	Files                          []string
}

func observeFile(name string, data []byte, info os.FileInfo) fileObservation {
	file := protocol.BundleFileName(name)
	execBits := byte(info.Mode() & 0111)
	return fileObservation{
		Hash:        FileBundleHash(file, data, execBits, false),
		Fingerprint: FileBundleHash(file, data, execBits, true),
		Description: skillDescription(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})),
		Manifest: &protocol.SkillManifest{
			Files:  []protocol.SkillFile{{Path: file, Size: info.Size(), Fingerprint: fileFingerprint(data)}},
			Newest: info.ModTime().UTC(),
		},
		Files: []string{file},
	}
}

// PackFile packs one prompt or agent file as a bundle of one file named after
// the file on disk.
func PackFile(path string) ([]byte, error) { return PackNamedFile(path, filepath.Base(path)) }

// PackNamedFile packs one regular file as a bundle that holds it under name.
func PackNamedFile(path, name string) ([]byte, error) {
	if name == "" || name != filepath.Base(name) {
		return nil, errors.New("invalid file name")
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	info, err := root.Lstat(filepath.Base(path))
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	if info.Size() > MaxBundleBytes {
		return nil, errors.New("skill exceeds bundle limit")
	}
	data, _, err := readBundleFile(root, filepath.Base(path))
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	tw := tar.NewWriter(&out)
	if err = tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Size: int64(len(data)), Mode: 0600 | int64(info.Mode()&0111)}); err != nil {
		return nil, err
	}
	if _, err = tw.Write(data); err != nil {
		return nil, err
	}
	if err = tw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// ObserveFile reads one prompt or agent file again and returns its strict hash
// and its manifest, the way a scan records them. It is used to check an
// observation before the file is imported or read.
func ObserveFile(path, name string) (string, *protocol.SkillManifest, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return "", nil, err
	}
	defer root.Close()
	data, info, err := readBundleFile(root, filepath.Base(path))
	if err != nil {
		return "", nil, err
	}
	obs := observeFile(name, data, info)
	return obs.Hash, obs.Manifest, nil
}

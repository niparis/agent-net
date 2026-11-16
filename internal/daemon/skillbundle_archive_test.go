package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
)

// observedFolder makes a skill folder with one sparse file per size, so the
// test writes no data.
func observedFolder(t *testing.T, sizes ...int64) string {
	t.Helper()
	folder := t.TempDir()
	if err := os.WriteFile(filepath.Join(folder, "SKILL.md"), []byte("---\ndescription: Large.\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for i, size := range sizes {
		name := filepath.Join(folder, fmt.Sprintf("f%d.bin", i))
		if err := os.WriteFile(name, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Truncate(name, size); err != nil {
			t.Fatal(err)
		}
	}
	return folder
}

// observedSkill is observedFolder for a daemon that lists it, as its last scan
// would, with the folder's real hash.
func observedSkill(t *testing.T, sizes ...int64) (*Daemon, protocol.SkillBundleRequest) {
	t.Helper()
	folder := observedFolder(t, sizes...)
	hash, err := inventory.SkillHash(folder)
	if err != nil {
		t.Fatal(err)
	}
	d := &Daemon{sourceSkills: map[string]bool{sourceKey("large", hash, folder): true}}
	return d, protocol.SkillBundleRequest{Name: "large", Hash: hash, Location: folder}
}

// The archive answer of a skill over one part comes in parts, each under one
// part in size. Joined, the parts are the bundle at the listed hash, and the
// snapshot is gone after its last part.
func TestSkillBundleArchiveAnswersInParts(t *testing.T) {
	d, req := observedSkill(t, 3<<20, 3<<20)
	req.Archive = true
	first, err := d.skillBundleAnswer(req)
	if err != nil {
		t.Fatal(err)
	}
	if first.Parts < 2 || first.Snapshot == "" {
		t.Fatalf("first answer is not in parts: parts %d, snapshot %q", first.Parts, first.Snapshot)
	}
	whole := append([]byte(nil), first.Data...)
	for i := 1; i < first.Parts; i++ {
		part, err := d.skillBundleAnswer(protocol.SkillBundleRequest{Snapshot: first.Snapshot, Part: i})
		if err != nil {
			t.Fatalf("part %d: %v", i, err)
		}
		if len(part.Data) > protocol.TransferPartSize {
			t.Fatalf("part %d is %d bytes, over one part", i, len(part.Data))
		}
		whole = append(whole, part.Data...)
	}
	if int64(len(whole)) != first.Size {
		t.Fatalf("joined %d bytes, the answer says %d", len(whole), first.Size)
	}
	sum := sha256.Sum256(whole)
	if hex.EncodeToString(sum[:]) != first.SHA256 {
		t.Fatal("joined parts do not match the whole SHA-256")
	}
	out := t.TempDir()
	if err := inventory.UnpackSkillLimit(whole, out, inventory.ArchiveLimit); err != nil {
		t.Fatal(err)
	}
	if got, _ := inventory.SkillHash(out); got != req.Hash {
		t.Fatalf("unpacked hash %s, want %s", got, req.Hash)
	}
	if _, err := d.skillBundleAnswer(protocol.SkillBundleRequest{Snapshot: first.Snapshot, Part: first.Parts - 1}); err == nil {
		t.Fatal("a snapshot was served after its last part")
	}
}

// A small archive skill is one answer, as before, with no snapshot.
func TestSkillBundleArchiveSmallSkillIsOneAnswer(t *testing.T) {
	d, req := observedSkill(t, 10)
	req.Archive = true
	answer, err := d.skillBundleAnswer(req)
	if err != nil {
		t.Fatal(err)
	}
	if answer.Parts != 0 || answer.Snapshot != "" || len(answer.Data) == 0 {
		t.Fatalf("small skill answered in parts: %+v", answer)
	}
}

// The import limit still refuses a 6 MB skill and names itself. The archive
// refuses a skill past 64 MB and names the archive limit.
func TestSkillBundleLimitsNameTheirLimit(t *testing.T) {
	d, req := observedSkill(t, 3<<20, 3<<20)
	if _, err := d.skillBundleAnswer(req); err == nil || !strings.Contains(err.Error(), "2 MB import limit") {
		t.Fatalf("import limit answer: %v", err)
	}
	// Past 64 MB the pack refuses before it reads a byte, so the hash is not
	// needed: a placeholder keeps the test from hashing 64 MB.
	folder := observedFolder(t, inventory.MaxArchiveBytes+1)
	d = &Daemon{sourceSkills: map[string]bool{sourceKey("large", "placeholder", folder): true}}
	req = protocol.SkillBundleRequest{Name: "large", Hash: "placeholder", Location: folder, Archive: true}
	if _, err := d.skillBundleAnswer(req); err == nil || !strings.Contains(err.Error(), "64 MB archive limit") {
		t.Fatalf("archive limit answer: %v", err)
	}
}

// A snapshot that nothing asks for again is dropped once its time runs out.
func TestSkillSnapshotExpires(t *testing.T) {
	var s skillSnapshots
	start := time.Now()
	old := s.put(make([]byte, 3<<20), start)
	s.put(make([]byte, 3<<20), start.Add(skillSnapshotTTL+time.Second))
	if _, err := s.part(old, 0, start.Add(skillSnapshotTTL+time.Second)); err == nil {
		t.Fatal("a snapshot past its time was served")
	}
}

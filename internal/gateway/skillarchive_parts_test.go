package gateway

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
)

// archiveLargeFolder writes a skill of two 3 MB files. That is over the import
// limit (decision 0048) and under the archive limit (decision 0169).
func archiveLargeFolder(t *testing.T, dir string) (string, string) {
	t.Helper()
	folder, _ := archiveSkillFolder(t, dir, "---\ndescription: A large skill.\n---\nbody\n")
	for _, name := range []string{"a.bin", "b.bin"} {
		path := filepath.Join(folder, name)
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Truncate(path, 3<<20); err != nil {
			t.Fatal(err)
		}
	}
	hash, err := inventory.SkillHash(folder)
	if err != nil {
		t.Fatal(err)
	}
	return folder, hash
}

// archiveBundleAt packs a skill folder with the archive limit, as the daemon
// packs it for an archive request.
func archiveBundleAt(t *testing.T, folder string) []byte {
	t.Helper()
	bundle, err := inventory.PackSkillLimit(folder, inventory.ArchiveLimit)
	if err != nil {
		t.Fatal(err)
	}
	return bundle
}

// A 6 MB skill from a machine folder is stored by its hash and reads back
// intact. The machine answers in parts, and no answer is over the link's read
// limit.
func TestSkillArchiveAddTakesSkillOverTheImportLimit(t *testing.T) {
	g, n := archiveFixture(t)
	folder, hash := archiveLargeFolder(t, filepath.Join(t.TempDir(), "large"))
	bundle := archiveBundleAt(t, folder)
	if len(bundle) <= protocol.TransferPartSize {
		t.Fatalf("bundle is %d bytes, not over one part", len(bundle))
	}
	frames := archiveFakeNodeAnswer(t, g, n, map[string][]byte{folder: bundle}, &protocol.SkillSourcesResult{UserRead: true, Sources: []protocol.SkillSource{
		{Name: "large", Hash: hash, Location: folder, Origin: "user", State: protocol.InventoryOK},
	}})

	got := archiveCall[protocol.SkillArchiveAddResult](t, g, protocol.FrameSkillArchiveAdd, protocol.SkillArchiveAdd{
		Location: protocol.ToolImportLocation{NodeID: n, Location: folder}, By: "cto",
	})
	if got.State != "added" || got.Version != 1 || got.Hash != hash {
		t.Fatalf("add: %+v", got)
	}
	read, err := g.skillArchiveRead(protocol.SkillArchiveVersion{Name: "large", Version: 1})
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if read.Hash != hash {
		t.Fatalf("read hash %s, want %s", read.Hash, hash)
	}
	sizes := map[string]int64{}
	for _, f := range read.Files {
		sizes[f.Path] = f.Size
	}
	if sizes["a.bin"] != 3<<20 || sizes["b.bin"] != 3<<20 {
		t.Fatalf("read files %v", sizes)
	}
	if largest := frames.largest(); largest == 0 || largest > 4<<20 {
		t.Fatalf("largest answer is %d bytes, want under the 4 MB read limit", largest)
	}
}

// Intake takes the 6 MB skill and skips one past the archive limit, with the
// archive limit in the reason. The other skills are stored as before.
func TestSkillArchiveIntakeTakesLargeSkillAndSkipsPastTheArchiveLimit(t *testing.T) {
	g, n := archiveFixture(t)
	dir := t.TempDir()
	large, largeHash := archiveLargeFolder(t, filepath.Join(dir, "large"))
	small, smallHash := archiveSkillFolder(t, filepath.Join(dir, "small"), "---\ndescription: Small.\n---\nbody\n")
	const hugeHash = "hugehash"
	// The huge bundle is never unpacked: the machine refuses it first.
	bundles := map[string][]byte{
		large:          archiveBundleAt(t, large),
		small:          archiveBundleAt(t, small),
		"/skills/huge": make([]byte, inventory.ArchiveLimit.Bytes+1),
	}
	frames := archiveFakeNodeAnswer(t, g, n, bundles, &protocol.SkillSourcesResult{UserRead: true, Sources: []protocol.SkillSource{
		{Name: "large", Hash: largeHash, Location: large, Origin: "user", State: protocol.InventoryOK},
		{Name: "small", Hash: smallHash, Location: small, Origin: "user", State: protocol.InventoryOK},
		{Name: "huge", Hash: hugeHash, Location: "/skills/huge", Origin: "user", State: protocol.InventoryOK},
	}})

	got := archiveCall[protocol.SkillArchiveMultiResult](t, g, protocol.FrameSkillArchiveIntake, protocol.SkillArchiveIntake{By: "cto"})
	added := map[string]bool{}
	for _, s := range got.Skills {
		added[s.Name] = s.State == "added"
	}
	if !added["large"] || !added["small"] || len(got.Skills) != 2 {
		t.Fatalf("intake stored %+v", got.Skills)
	}
	if len(got.Skipped) != 1 || got.Skipped[0].Name != "huge" || !strings.Contains(got.Skipped[0].Reason, "64 MB archive limit") {
		t.Fatalf("skipped %+v", got.Skipped)
	}
	if largest := frames.largest(); largest > 4<<20 {
		t.Fatalf("largest answer is %d bytes, over the 4 MB read limit", largest)
	}
}

// A stored version over one part is served part by part to delivery. The
// parts join to the bundle, and a version of one part is one answer.
func TestSkillVersionServesLargeVersionInParts(t *testing.T) {
	g, _ := archiveFixture(t)
	dir := t.TempDir()
	folder, hash := archiveLargeFolder(t, filepath.Join(dir, "large"))
	bundle := archiveBundleAt(t, folder)
	if _, err := g.skillArchivePut(skillArchiveContent{Name: "large", Hash: hash, Bundle: bundle, Description: "A large skill.", SizeTokens: 1, ContextTokens: 1},
		skillArchiveSource{Kind: "agent-net"}, "cto", false); err != nil {
		t.Fatal(err)
	}
	first, err := g.store.SkillVersion("large", 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if first.Parts != protocol.PartCount(int64(len(bundle))) || first.Size != int64(len(bundle)) {
		t.Fatalf("first part says %d parts of %d bytes", first.Parts, first.Size)
	}
	var whole []byte
	for i := 0; i < first.Parts; i++ {
		part, err := g.store.SkillVersion("large", 1, i)
		if err != nil {
			t.Fatalf("part %d: %v", i, err)
		}
		if len(part.Data) > protocol.TransferPartSize {
			t.Fatalf("part %d is %d bytes", i, len(part.Data))
		}
		whole = append(whole, part.Data...)
	}
	if !bytes.Equal(whole, bundle) {
		t.Fatal("joined parts are not the stored bundle")
	}
	if _, err := g.store.SkillVersion("large", 1, first.Parts); err == nil {
		t.Fatal("a part past the last was served")
	}

	const small = "small"
	smallFolder, smallHash := archiveSkillFolder(t, filepath.Join(dir, small), "---\ndescription: Small.\n---\nbody\n")
	smallBundle := archiveBundleAt(t, smallFolder)
	if _, err := g.skillArchivePut(skillArchiveContent{Name: small, Hash: smallHash, Bundle: smallBundle, Description: "Small.", SizeTokens: 1, ContextTokens: 1},
		skillArchiveSource{Kind: "agent-net"}, "cto", false); err != nil {
		t.Fatal(err)
	}
	one, err := g.store.SkillVersion(small, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if one.Parts != 0 || !bytes.Equal(one.Data, smallBundle) {
		t.Fatalf("small version answered in parts: parts %d", one.Parts)
	}
}

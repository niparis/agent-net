package gateway

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
)

// An add stores the folder as it is on disk now. The scan that the gateway
// holds is older, and the folder was edited after it, so the add takes the
// edited content under its own hash.
func TestSkillArchiveAddStoresFolderAsItIsNow(t *testing.T) {
	g, n, _ := toggleFixture(t)
	folder, oldHash := archiveSkillFolder(t, filepath.Join(t.TempDir(), "demo"), "---\ndescription: Demo.\n---\nold body\n")
	if err := g.store.SetInventory(n, protocol.InventoryReport{State: "ok", ScannedAt: time.Now(), Items: []protocol.InventoryItem{
		{Kind: "skill", Name: "demo", Hash: oldHash, State: "ok", Scope: "user", Location: folder},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "SKILL.md"), []byte("---\ndescription: Demo.\n---\nnew body\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	newHash, err := inventory.SkillHash(folder)
	if err != nil {
		t.Fatal(err)
	}
	archiveFakeNode(t, g, n, nil)

	got := archiveCall[protocol.SkillArchiveAddResult](t, g, protocol.FrameSkillArchiveAdd, protocol.SkillArchiveAdd{
		Location: protocol.ToolImportLocation{NodeID: n, Location: folder}, By: "cto",
	})
	if got.State != "added" || got.Hash != newHash {
		t.Fatalf("add: %+v, want the hash now %s", got, newHash)
	}
	read, err := g.skillArchiveRead("demo", got.Version)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if read.Hash != newHash {
		t.Fatalf("read hash %s, want %s", read.Hash, newHash)
	}
	for _, f := range read.Files {
		if f.Path == "SKILL.md" && !strings.Contains(f.Text, "new body") {
			t.Fatalf("stored SKILL.md is not the edited file: %q", f.Text)
		}
	}
}

// Intake takes the user folders from the live skill sources answer, not from
// the last inventory scan. An edited folder is stored at its new hash, and a
// folder the scan still lists but the machine no longer has is not listed.
func TestSkillArchiveIntakeReadsUserFoldersLive(t *testing.T) {
	g, n, _ := toggleFixture(t)
	dir := t.TempDir()
	edited, oldHash := archiveSkillFolder(t, filepath.Join(dir, "edited"), "---\ndescription: Edited.\n---\nold body\n")
	if err := os.WriteFile(filepath.Join(edited, "SKILL.md"), []byte("---\ndescription: Edited.\n---\nnew body\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	newHash, err := inventory.SkillHash(edited)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.store.SetInventory(n, protocol.InventoryReport{State: "ok", ScannedAt: time.Now(), Items: []protocol.InventoryItem{
		{Kind: "skill", Name: "edited", Hash: oldHash, State: "ok", Scope: "user", Location: edited},
		{Kind: "skill", Name: "removed", Hash: "removedhash", State: "ok", Scope: "user", Location: filepath.Join(dir, "removed")},
	}}); err != nil {
		t.Fatal(err)
	}
	archiveFakeNodeAnswer(t, g, n, nil, &protocol.SkillSourcesResult{UserRead: true, Sources: []protocol.SkillSource{
		{Name: "edited", Hash: newHash, Location: edited, Origin: "user", State: protocol.InventoryOK},
	}})

	got := archiveCall[protocol.SkillArchiveMultiResult](t, g, protocol.FrameSkillArchiveIntake, protocol.SkillArchiveIntake{By: "cto"})
	if len(got.Skills) != 1 || got.Skills[0].Name != "edited" || got.Skills[0].State != "added" || got.Skills[0].Hash != newHash {
		t.Fatalf("intake stored %+v, want the edited folder at %s", got.Skills, newHash)
	}
	if len(got.Skipped) != 0 {
		t.Fatalf("intake skipped %+v, want nothing: the removed folder is not listed", got.Skipped)
	}
}

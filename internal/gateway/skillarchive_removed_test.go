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

// removedFixture is one machine whose inventory lists the given folders of one
// skill name, and an archive that can take them.
type removedFixture struct {
	t       *testing.T
	g       *Gateway
	node    string
	dir     string
	bundles map[string][]byte
	items   []protocol.InventoryItem
}

func newRemovedFixture(t *testing.T) *removedFixture {
	t.Helper()
	g := archiveGateway(t)
	f := &removedFixture{t: t, g: g, dir: t.TempDir(), bundles: map[string][]byte{}}
	f.node = intakeNode(t, g, "alpha", nil)
	return f
}

// folder writes one skill folder, lists it in the machine's inventory and
// returns its content as the archive reads it.
func (f *removedFixture) folder(name, key, body string) skillArchiveContent {
	f.t.Helper()
	dir, hash := archiveSkillFolder(f.t, filepath.Join(f.dir, key, name), "---\ndescription: "+body+".\n---\n"+body+"\n")
	f.bundles[hash] = archiveBundle(f.t, dir)
	f.items = append(f.items, protocol.InventoryItem{Kind: "skill", Name: name, Hash: hash, State: "ok", Scope: "user", Location: dir})
	return skillContentOf(f.t, name, dir, hash)
}

// intake runs one intake over the folders listed so far.
func (f *removedFixture) intake() protocol.SkillArchiveMultiResult {
	f.t.Helper()
	if err := f.g.store.SetInventory(f.node, protocol.InventoryReport{State: "ok", ScannedAt: time.Now(), Items: f.items}); err != nil {
		f.t.Fatal(err)
	}
	archiveFakeNode(f.t, f.g, f.node, f.bundles)
	return archiveCall[protocol.SkillArchiveMultiResult](f.t, f.g, protocol.FrameSkillArchiveIntake, protocol.SkillArchiveIntake{By: "cto"})
}

func (f *removedFixture) add(c skillArchiveContent) protocol.SkillArchiveAddResult {
	f.t.Helper()
	out, err := f.g.skillArchivePut(c, skillArchiveSource{Kind: "machine", Node: f.node, Path: "/x/" + c.Name}, "owner", false)
	if err != nil {
		f.t.Fatal(err)
	}
	return out
}

func (f *removedFixture) remove(name string, version int) protocol.SkillArchiveAddResult {
	f.t.Helper()
	return archiveCall[protocol.SkillArchiveAddResult](f.t, f.g, protocol.FrameSkillArchiveRemove, protocol.SkillArchiveRemove{Name: name, Version: version, By: "cto"})
}

func skippedReason(got protocol.SkillArchiveMultiResult, name string) string {
	for _, s := range got.Skipped {
		if s.Name == name {
			return s.Reason
		}
	}
	return ""
}

// A name the CTO removed does not come back from another folder on the next
// intake (mastering-postgresql came back from ~/.codex/skills). Only an
// explicit add clears it.
func TestSkillArchiveIntakeLeavesARemovedNameOut(t *testing.T) {
	f := newRemovedFixture(t)
	first := f.folder("mastering-postgresql", "claude", "first")
	f.add(first)
	f.remove("mastering-postgresql", 0)
	f.folder("mastering-postgresql", "codex", "other content")

	got := f.intake()
	if len(got.Skills) != 0 {
		t.Fatalf("intake stored a removed name again: %+v", got.Skills)
	}
	if reason := skippedReason(got, "mastering-postgresql"); !strings.HasPrefix(reason, "removed by cto") {
		t.Fatalf("skip reason %q, report %+v", reason, got.Skipped)
	}
	if m := got.Machines[0]; m.Skipped != 1 || m.Found != 1 || m.Added != 0 {
		t.Fatalf("machine counts: %+v", m)
	}
	if rows := archiveList(t, f.g); len(rows) != 0 {
		t.Fatalf("the removed name is live again: %+v", rows)
	}

	// An explicit add of the other content brings the name back. A third
	// content then arrives as a candidate again: the block is gone.
	if added := f.add(skillContentOf(t, "mastering-postgresql", filepath.Join(f.dir, "codex", "mastering-postgresql"), f.items[1].Hash)); added.State != "added" {
		t.Fatalf("explicit add: %+v", added)
	}
	f.folder("mastering-postgresql", "third", "third content")
	got = f.intake()
	if len(got.Skills) != 1 || got.Skills[0].Version != 3 || got.Skills[0].Latest != 2 {
		t.Fatalf("after an explicit add intake adds candidates again: %+v", got)
	}
	// The first content was removed with the name and the explicit add did not
	// name it: it stays out, and the report says so.
	if reason := skippedReason(got, "mastering-postgresql"); !strings.HasPrefix(reason, "removed by cto") {
		t.Fatalf("the removed content came back: skip %q, report %+v", reason, got.Skipped)
	}
}

// Removing one version keeps that content out of later intakes (code-review
// got a different skill under its name from the Codex folder), and leaves the
// other versions alone.
func TestSkillArchiveIntakeLeavesARemovedVersionOut(t *testing.T) {
	f := newRemovedFixture(t)
	good := f.folder("code-review", "claude", "the good one")
	f.add(good)
	wrong := f.folder("code-review", "codex", "a different skill")

	got := f.intake()
	if len(got.Skills) != 1 || got.Skills[0].Version != 2 || got.Skills[0].Latest != 1 {
		t.Fatalf("first intake: %+v", got.Skills)
	}
	removed := f.remove("code-review", 2)
	if removed.State != "removed" || removed.Version != 2 || removed.Versions != 1 || removed.Removed != 1 {
		t.Fatalf("remove one version: %+v", removed)
	}

	got = f.intake()
	if len(got.Skills) != 0 {
		t.Fatalf("intake stored the removed content again: %+v", got.Skills)
	}
	if reason := skippedReason(got, "code-review"); !strings.HasPrefix(reason, "removed by cto") {
		t.Fatalf("skip reason %q", reason)
	}
	rows := archiveList(t, f.g)
	if len(rows) != 1 || rows[0].Version != 1 || rows[0].Versions != 1 || len(rows[0].Candidates) != 0 {
		t.Fatalf("list: %+v", rows)
	}
	if added := f.add(wrong); added.State != "restored" || added.Version != 2 {
		t.Fatalf("explicit add of the removed content: %+v", added)
	}
}

// Undo of a removal puts the skill back and lifts the block with it.
func TestSkillArchiveUndoOfRemoveLiftsTheBlock(t *testing.T) {
	f := newRemovedFixture(t)
	f.add(f.folder("demo", "claude", "one"))
	removed := f.remove("demo", 0)
	archiveCall[protocol.SkillArchiveAddResult](t, f.g, protocol.FrameSkillArchiveUndo, protocol.SkillArchiveUndo{Change: removed.Change, By: "owner"})
	f.folder("demo", "codex", "two")
	got := f.intake()
	if len(got.Skills) != 1 || got.Skills[0].Version != 2 {
		t.Fatalf("after undo, intake adds a candidate again: %+v", got)
	}
}

// A name removed and then added again with new content has version 2 and one
// live version. Every answer says so the same way: versions counts the live
// ones, removed counts the ones that keep their numbers.
func TestSkillArchiveVersionNumbersAfterRemoveAndAdd(t *testing.T) {
	f := newRemovedFixture(t)
	f.add(f.folder("mastering-postgresql", "claude", "one"))
	f.remove("mastering-postgresql", 0)
	added := f.add(f.folder("mastering-postgresql", "codex", "two"))
	if added.State != "added" || added.Version != 2 || added.Latest != 2 || added.Versions != 1 || added.Removed != 1 {
		t.Fatalf("add after remove: %+v", added)
	}
	rows := archiveList(t, f.g)
	if len(rows) != 1 || rows[0].Version != 2 || rows[0].Versions != 1 || rows[0].Removed != 1 || len(rows[0].Candidates) != 0 {
		t.Fatalf("list after remove and add: %+v", rows)
	}
	read, err := f.g.skillArchiveRead("mastering-postgresql", 0)
	if err != nil || read.Version != rows[0].Version {
		t.Fatalf("read of the latest: %+v, %v", read, err)
	}
}

// Codex and the agents keep built-in skills in .system folders. A machine whose
// daemon lists no live folders reports them in its inventory as system items:
// intake leaves them out.
func TestSkillArchiveIntakeLeavesSystemItemsOut(t *testing.T) {
	f := newRemovedFixture(t)
	f.folder("mine", "claude", "mine")
	system := f.folder("imagegen", "codex/.system", "built in")
	for i := range f.items {
		if f.items[i].Hash == system.Hash {
			f.items[i].Source = protocol.InventorySystem
		}
	}
	got := f.intake()
	if len(got.Skills) != 1 || got.Skills[0].Name != "mine" || len(got.Skipped) != 0 {
		t.Fatalf("intake took a system skill: %+v", got)
	}
}

// Each stored skill has two figures: the name and description that every
// session loads, and the whole bundle that loads only when it is used.
func TestSkillArchiveListShowsContextAndSizeTokens(t *testing.T) {
	f := newRemovedFixture(t)
	dir, hash := archiveSkillFolder(t, filepath.Join(f.dir, "bulky"), "---\ndescription: A short description.\n---\nbody\n")
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte(strings.Repeat("long reference text ", 2000)), 0o600); err != nil {
		t.Fatal(err)
	}
	hash, err := inventory.SkillHash(dir)
	if err != nil {
		t.Fatal(err)
	}
	f.add(skillContentOf(t, "bulky", dir, hash))
	rows := archiveList(t, f.g)
	wantContext := skillContextTokens("bulky", "A short description.")
	if len(rows) != 1 || rows[0].ContextTokens != wantContext || rows[0].ContextTokens > 20 || rows[0].SizeTokens < 10000 {
		t.Fatalf("list tokens: %+v", rows)
	}
}

// The migration blocks what was removed before it ran, and gives every row a
// context figure.
func TestSkillArchiveGuardMigrationBackfills(t *testing.T) {
	f := newRemovedFixture(t)
	db := f.g.store.db
	for _, row := range []struct {
		name, hash string
		version    int
		removed    int64
	}{{"gone", "h1", 1, 5}, {"gone", "h2", 2, 6}, {"half", "h3", 1, 7}, {"half", "h4", 2, 0}} {
		if _, err := db.Exec(`INSERT INTO skill_archive(name,version,hash,content,description,tokens,added_at,removed_at) VALUES(?,?,?,x'00','Does things.',100,1,?)`, row.name, row.version, row.hash, row.removed); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO skill_archive_changes(id,action,name,actor,at) VALUES('sc_1','remove','gone','cto',6)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(skillArchiveGuardMigration); err != nil {
		t.Fatal(err)
	}
	for _, want := range []struct{ name, hash, actor string }{{"gone", "h1", "cto"}, {"gone", "h2", "cto"}, {"gone", "", "cto"}, {"half", "h3", ""}} {
		var actor string
		if err := db.QueryRow(`SELECT actor FROM skill_archive_blocks WHERE name=? AND hash=?`, want.name, want.hash).Scan(&actor); err != nil || actor != want.actor {
			t.Errorf("block %+v: actor %q, %v", want, actor, err)
		}
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM skill_archive_blocks WHERE name='half'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("a name with a live version has no whole-name block, %d half rows, %v", n, err)
	}
	var context int
	if err := db.QueryRow(`SELECT context_tokens FROM skill_archive WHERE name='gone' AND version=1`).Scan(&context); err != nil || context != skillContextTokens("gone", "Does things.") {
		t.Errorf("backfilled context tokens %d, %v", context, err)
	}
}

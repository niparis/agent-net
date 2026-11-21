package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
)

// userSourceHashes lists the user-level folders of one skill sources answer,
// with the hash each has now.
func userSourceHashes(t *testing.T, d *Daemon) map[string]string {
	t.Helper()
	sources, _ := d.skillSources(nil)
	out := map[string]string{}
	for _, s := range sources {
		if s.Origin == "user" && s.State == protocol.InventoryOK {
			out[s.Location] = s.Hash
		}
	}
	return out
}

// The skill sources answer reads the user-level skill folders as they are on
// disk, so an edited folder comes at its new hash and a removed one is not
// listed (decision 0169, live intake).
func TestSkillSourcesListsUserFoldersAsTheyAreNow(t *testing.T) {
	d, home := skillTestDaemon(t)
	alpha := filepath.Join(home, ".claude/skills/alpha")
	beta := filepath.Join(home, ".agents/skills/beta")
	writeSourceSkill(t, alpha, "---\ndescription: Alpha.\n---\nold\n")
	writeSourceSkill(t, beta, "---\ndescription: Beta.\n---\nbody\n")
	before := userSourceHashes(t, d)
	if before[alpha] == "" || before[beta] == "" {
		t.Fatalf("user folders not listed: %v", before)
	}

	writeSourceSkill(t, alpha, "---\ndescription: Alpha.\n---\nnew\n")
	if err := os.RemoveAll(beta); err != nil {
		t.Fatal(err)
	}
	after := userSourceHashes(t, d)
	if after[alpha] == "" || after[alpha] == before[alpha] {
		t.Fatalf("edited folder listed at %q, was %q", after[alpha], before[alpha])
	}
	if _, listed := after[beta]; listed {
		t.Fatalf("removed folder is still listed: %v", after)
	}
}

// An archive request packs the folder as it is on disk now. The hash the
// gateway holds may be from an older scan, and the answer is the new content.
func TestSkillBundleArchivePacksFolderAsItIsNow(t *testing.T) {
	d, home := skillTestDaemon(t)
	alpha := filepath.Join(home, ".claude/skills/alpha")
	writeSourceSkill(t, alpha, "---\ndescription: Alpha.\n---\nold\n")
	if _, err := d.skillSources(nil); err != nil {
		t.Fatal(err)
	}
	writeSourceSkill(t, alpha, "---\ndescription: Alpha.\n---\nnew\n")
	newHash, err := inventory.SkillHash(alpha)
	if err != nil {
		t.Fatal(err)
	}
	answer, err := d.skillBundleAnswer(protocol.SkillBundleRequest{Name: "alpha", Location: alpha})
	if err != nil {
		t.Fatalf("archive pack of an edited folder: %v", err)
	}
	out := t.TempDir()
	if err := inventory.UnpackSkillLimit(answer.Data, out, inventory.ArchiveLimit); err != nil {
		t.Fatal(err)
	}
	if got, _ := inventory.SkillHash(out); got != newHash {
		t.Fatalf("packed hash %s, want the folder's hash now %s", got, newHash)
	}
}

// The archive packs only folders that the last skill sources answer
// listed. A folder outside every skill root is refused.
func TestSkillBundleArchiveRefusesUnlistedFolder(t *testing.T) {
	d, home := skillTestDaemon(t)
	loose := filepath.Join(home, "notes/skills/loose")
	writeSourceSkill(t, loose, "---\ndescription: Loose.\n---\nbody\n")
	if _, err := d.skillBundleAnswer(protocol.SkillBundleRequest{Name: "loose", Location: loose}); err == nil {
		t.Fatal("an unlisted folder was packed")
	}
}

// A listed folder that is replaced by a link to another folder is not packed:
// the archive reads no folder through a link.
func TestSkillBundleArchiveRefusesFolderReplacedByLink(t *testing.T) {
	d, home := skillTestDaemon(t)
	alpha := filepath.Join(home, ".claude/skills/alpha")
	writeSourceSkill(t, alpha, "---\ndescription: Alpha.\n---\nbody\n")
	if _, err := d.skillSources(nil); err != nil {
		t.Fatal(err)
	}
	elsewhere := filepath.Join(t.TempDir(), "elsewhere")
	writeSourceSkill(t, elsewhere, "---\ndescription: Elsewhere.\n---\nbody\n")
	if err := os.RemoveAll(alpha); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, alpha); err != nil {
		t.Fatal(err)
	}
	if _, err := d.skillBundleAnswer(protocol.SkillBundleRequest{Name: "alpha", Location: alpha}); err == nil {
		t.Fatal("a folder replaced by a link was packed")
	}
}

// Codex's built-in skills under skills/.system ship with Codex. They are not
// archive material, so the skill sources answer does not list them.
func TestSkillSourcesLeavesOutSystemFolders(t *testing.T) {
	d, home := skillTestDaemon(t)
	user := filepath.Join(home, ".codex/skills/mine")
	system := filepath.Join(home, ".codex/skills/.system/imagegen")
	agentsSystem := filepath.Join(home, ".agents/skills/.system/openai-docs")
	for _, dir := range []string{user, system, agentsSystem} {
		writeSourceSkill(t, dir, "---\ndescription: A skill.\n---\nbody\n")
	}
	got := userSourceHashes(t, d)
	if got[user] == "" {
		t.Fatalf("user folder not listed: %v", got)
	}
	for _, dir := range []string{system, agentsSystem} {
		if _, listed := got[dir]; listed {
			t.Errorf("system folder %s is listed: %v", dir, got)
		}
	}
}

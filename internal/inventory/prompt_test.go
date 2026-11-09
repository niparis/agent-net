package inventory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func writeTestFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

func findItem(r protocol.InventoryReport, kind, scope, name string) *protocol.InventoryItem {
	for i := range r.Items {
		it := &r.Items[i]
		if it.Kind == kind && it.Scope == scope && it.Name == name {
			return it
		}
	}
	return nil
}

func TestInventoryFindsCommandAndAgentFiles(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	writeTestFile(t, filepath.Join(home, ".claude/commands/review.md"), "---\ndescription: Review a diff\n---\nLook at it.\n")
	writeTestFile(t, filepath.Join(home, ".claude/agents/planner.md"), "---\nname: planner\ndescription: Plans work\n---\nPlan.\n")
	writeTestFile(t, filepath.Join(project, ".claude/commands/ship.md"), "Ship it.\n")
	writeTestFile(t, filepath.Join(project, ".claude/agents/qa.md"), "Test it.\n")
	// Not items: a nested namespace folder, a non-Markdown file and a hidden file.
	writeTestFile(t, filepath.Join(home, ".claude/commands/ns/inner.md"), "nested")
	writeTestFile(t, filepath.Join(home, ".claude/commands/notes.txt"), "text")
	writeTestFile(t, filepath.Join(home, ".claude/commands/.hidden.md"), "hidden")

	r := Scan(Options{Home: home, Machine: "m", Projects: []protocol.ProjectEntry{{Name: "p", Path: project}}})

	review := findItem(r, protocol.InventoryPrompt, protocol.InventoryUser, "review")
	if review == nil {
		t.Fatalf("user command review not found: %+v", r.Items)
	}
	if review.Harness != "claude" || review.State != protocol.InventoryOK || review.Source != protocol.InventoryNative {
		t.Fatalf("bad command item: %+v", review)
	}
	if review.Location != filepath.Join(home, ".claude/commands/review.md") {
		t.Fatalf("location %q", review.Location)
	}
	if review.Description != "Review a diff" {
		t.Fatalf("description %q", review.Description)
	}
	if review.Hash == "" || review.ContentFingerprint == "" {
		t.Fatalf("missing hashes: %+v", review)
	}
	if review.Manifest == nil || len(review.Manifest.Files) != 1 || review.Manifest.Files[0].Path != "review.md" || review.Manifest.Files[0].Size == 0 {
		t.Fatalf("bad manifest: %+v", review.Manifest)
	}
	if len(review.Files) != 1 || review.Files[0] != "review.md" {
		t.Fatalf("files %v", review.Files)
	}

	planner := findItem(r, protocol.InventoryAgent, protocol.InventoryUser, "planner")
	if planner == nil || planner.Description != "Plans work" || planner.Location != filepath.Join(home, ".claude/agents/planner.md") {
		t.Fatalf("user agent planner: %+v", planner)
	}
	ship := findItem(r, protocol.InventoryPrompt, protocol.InventoryProject, "ship")
	if ship == nil || ship.Project != "p" || ship.ProjectRoot != project || ship.Location != filepath.Join(project, ".claude/commands/ship.md") {
		t.Fatalf("project command ship: %+v", ship)
	}
	qa := findItem(r, protocol.InventoryAgent, protocol.InventoryProject, "qa")
	if qa == nil || qa.Project != "p" || qa.Harness != "claude" {
		t.Fatalf("project agent qa: %+v", qa)
	}
	for _, it := range r.Items {
		if it.Kind == protocol.InventoryPrompt || it.Kind == protocol.InventoryAgent {
			for _, bad := range []string{"inner", "notes", ".hidden", "ns"} {
				if it.Name == bad {
					t.Fatalf("%s must not be an item", bad)
				}
			}
		}
	}
}

func TestFileKindHashMatchesItsBundle(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".claude/commands/review.md")
	writeTestFile(t, path, "Look at the diff.\n")
	r := Scan(Options{Home: home, Machine: "m"})
	it := findItem(r, protocol.InventoryPrompt, protocol.InventoryUser, "review")
	if it == nil {
		t.Fatal("command not found")
	}
	bundle, err := PackFile(path)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err = UnpackSkill(bundle, dir); err != nil {
		t.Fatal(err)
	}
	// The bundle holds exactly one file, named like the file in the harness folder.
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "review.md" {
		t.Fatalf("bundle entries %v err %v", entries, err)
	}
	hash, err := SkillHash(dir)
	if err != nil || hash != it.Hash {
		t.Fatalf("bundle hash %q err %v, item hash %q", hash, err, it.Hash)
	}
	// A changed file has a different hash.
	writeTestFile(t, path, "Look at the diff twice.\n")
	r = Scan(Options{Home: home, Machine: "m"})
	if again := findItem(r, protocol.InventoryPrompt, protocol.InventoryUser, "review"); again == nil || again.Hash == it.Hash {
		t.Fatalf("hash did not change: %+v", again)
	}
}

func TestFileKindRefusesOversizedFileAndEscapingLink(t *testing.T) {
	home, outside := t.TempDir(), t.TempDir()
	big := filepath.Join(home, ".claude/agents/huge.md")
	writeTestFile(t, big, "x")
	if err := os.Truncate(big, 65<<20); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(outside, "evil.md"), "outside")
	if err := os.MkdirAll(filepath.Join(home, ".claude/commands"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "evil.md"), filepath.Join(home, ".claude/commands/evil.md")); err != nil {
		t.Fatal(err)
	}
	r := Scan(Options{Home: home, Machine: "m"})
	huge := findItem(r, protocol.InventoryAgent, protocol.InventoryUser, "huge")
	if huge == nil || huge.State != protocol.InventoryError || !strings.Contains(huge.Reason, "64 MiB") || huge.Hash != "" {
		t.Fatalf("oversized agent: %+v", huge)
	}
	evil := findItem(r, protocol.InventoryPrompt, protocol.InventoryUser, "evil")
	if evil == nil || evil.State != protocol.InventoryError || evil.Hash != "" {
		t.Fatalf("escaping link: %+v", evil)
	}
}

func TestFileKindFollowsInHomeLinkWithAlias(t *testing.T) {
	home := t.TempDir()
	real := filepath.Join(home, "dotfiles/commands/review.md")
	writeTestFile(t, real, "shared\n")
	if err := os.MkdirAll(filepath.Join(home, ".claude/commands"), 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, ".claude/commands/review.md")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	r := Scan(Options{Home: home, Machine: "m"})
	it := findItem(r, protocol.InventoryPrompt, protocol.InventoryUser, "review")
	if it == nil || it.State != protocol.InventoryOK || it.Location != real || len(it.Aliases) != 1 || it.Aliases[0] != link {
		t.Fatalf("linked command: %+v", it)
	}
}

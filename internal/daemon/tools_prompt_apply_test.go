package daemon

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
)

// fileKindFixture builds the stored bundle of a one-file item and a copy
// target for it. kind is prompt or agent.
func fileKindFixture(t *testing.T, kind, name, body string) (*Daemon, protocol.ToolTarget, []byte, string) {
	t.Helper()
	home, checkout, state, src := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
	file := filepath.Join(src, name+".md")
	if err := os.WriteFile(file, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	bundle, err := inventory.PackFile(file)
	if err != nil {
		t.Fatal(err)
	}
	unpacked := t.TempDir()
	if err = inventory.UnpackSkill(bundle, unpacked); err != nil {
		t.Fatal(err)
	}
	hash, err := inventory.SkillHash(unpacked)
	if err != nil {
		t.Fatal(err)
	}
	item, _ := json.Marshal([]string{kind, name, hash})
	root := protocol.FileKindRoots(kind)[0]
	d := &Daemon{cfg: Config{InventoryHome: home, StateDir: state}}
	target := protocol.ToolTarget{Name: name, Revision: protocol.ToolRevision{Item: string(item), Revision: 1, Version: hash, Assignment: protocol.ToolAssignment{Scope: "projects", Projects: []string{"project"}}},
		Target: protocol.ToolPlanTarget{Harness: "claude", Root: root, Checkout: checkout, Project: "project", Action: "copy"}}
	return d, target, bundle, hash
}

func manifestEntries(t *testing.T, d *Daemon) []skillManifestEntry {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(d.cfg.StateDir, "skill-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var entries []skillManifestEntry
	if err = json.Unmarshal(b, &entries); err != nil {
		t.Fatal(err)
	}
	return entries
}

func TestProjectCopyOfCommandAndAgentWritesFileAndExcludeLine(t *testing.T) {
	for _, c := range []struct{ kind, name, root string }{{"prompt", "review", ".claude/commands"}, {"agent", "planner", ".claude/agents"}} {
		t.Run(c.kind, func(t *testing.T) {
			d, target, bundle, _ := fileKindFixture(t, c.kind, c.name, "Do the thing.\n")
			checkout := target.Target.Checkout
			gitIn(t, checkout, "init", "-q")
			r := d.copyProjectSkill(target, bundle)
			if r.State != "applied" {
				t.Fatalf("result: %+v", r)
			}
			dest := filepath.Join(checkout, c.root, c.name+".md")
			if b, err := os.ReadFile(dest); err != nil || string(b) != "Do the thing.\n" {
				t.Fatalf("copy: %q %v", b, err)
			}
			// No staging folder is left next to the file.
			entries, _ := os.ReadDir(filepath.Dir(dest))
			if len(entries) != 1 {
				t.Fatalf("leftovers in %s: %v", filepath.Dir(dest), entries)
			}
			// The exclude line names the file, with no trailing slash.
			b, err := os.ReadFile(filepath.Join(checkout, ".git/info/exclude"))
			line := "/" + c.root + "/" + c.name + ".md"
			if err != nil || !strings.Contains(string(b), line+"\n") || strings.Contains(string(b), line+"/") {
				t.Fatalf("exclude: %q %v", b, err)
			}
			// Git ignores the copy.
			cmd := exec.Command("git", "-C", checkout, "status", "--porcelain", "--untracked-files=all")
			if out, err := cmd.Output(); err != nil || len(out) != 0 {
				t.Fatalf("git sees the copy: %q %v", out, err)
			}
			// The manifest owns the file and its exclude line.
			var found bool
			for _, e := range manifestEntries(t, d) {
				if e.Path == dest {
					found = true
					if !e.Owned || !e.ExcludeOwned || e.ExcludeLine != line || e.Item != target.Revision.Item {
						t.Fatalf("manifest entry: %+v", e)
					}
				}
			}
			if !found {
				t.Fatalf("manifest lacks %s: %+v", dest, manifestEntries(t, d))
			}
			// A second apply finds the same content and changes nothing.
			if again := d.copyProjectSkill(target, bundle); again.State != "applied" {
				t.Fatalf("repeat: %+v", again)
			}
		})
	}
}

func TestProjectCopyOfCommandRefusesWrongRootAndEditedFile(t *testing.T) {
	d, target, bundle, _ := fileKindFixture(t, "prompt", "review", "Do the thing.\n")
	checkout := target.Target.Checkout
	gitIn(t, checkout, "init", "-q")
	wrong := target
	wrong.Target.Root = ".claude/skills"
	if r := d.copyProjectSkill(wrong, bundle); r.State != "conflict" || r.Reason != "unsupported skill root" {
		t.Fatalf("wrong root: %+v", r)
	}
	other := target
	other.Target.Harness = "codex"
	if r := d.copyProjectSkill(other, bundle); r.State != "conflict" {
		t.Fatalf("wrong harness: %+v", r)
	}
	if _, err := os.Lstat(filepath.Join(checkout, ".claude")); !os.IsNotExist(err) {
		t.Fatalf("refused copy touched the checkout: %v", err)
	}
	dest := filepath.Join(checkout, ".claude/commands/review.md")
	if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("my own edit\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if r := d.copyProjectSkill(target, bundle); r.State != "conflict" {
		t.Fatalf("edited file: %+v", r)
	}
	if b, _ := os.ReadFile(dest); string(b) != "my own edit\n" {
		t.Fatalf("edit lost: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(checkout, ".git/info/exclude")); strings.Contains(string(b), "review.md") {
		t.Fatalf("refused copy left an exclude line: %q", b)
	}
}

func TestGlobalCopyOfAgentAndUndoKeepsBackup(t *testing.T) {
	d, target, bundle, hash := fileKindFixture(t, "agent", "planner", "Plan it.\n")
	target.Revision.Assignment = protocol.ToolAssignment{Scope: "global"}
	target.Target.Checkout, target.Target.Project = "", ""
	if r := d.copyGlobalSkill(target, bundle); r.State != "applied" {
		t.Fatalf("copy: %+v", r)
	}
	dest := filepath.Join(d.cfg.InventoryHome, ".claude/agents/planner.md")
	if b, err := os.ReadFile(dest); err != nil || string(b) != "Plan it.\n" {
		t.Fatalf("copy: %q %v", b, err)
	}
	if !skillHashIs(dest, hash) {
		t.Fatal("copy does not have the stored hash")
	}
	undo := undoTargetOf(target, "undo_copy")
	if r := d.undoCopySkill(undo, nil); r.State != "applied" {
		t.Fatalf("undo: %+v", r)
	}
	if _, err := os.Lstat(dest); !os.IsNotExist(err) {
		t.Fatalf("copy remains: %v", err)
	}
	if d.findSkillBackup(target.Revision.Item, dest, hash) == "" {
		t.Fatal("no backup of the removed file")
	}
	if backups := d.skillBackups(target.Revision.Item); len(backups) != 1 || !strings.HasSuffix(backups[0].Source, "planner.md") {
		t.Fatalf("backups: %+v", backups)
	}
	// Undoing the removal puts the file back from the backup.
	remove := target
	remove.Target = protocol.ToolPlanTarget{Harness: "claude", Root: ".claude/agents", Action: "undo_remove", Source: dest}
	if r := d.undoRemoveSkill(remove); r.State != "applied" {
		t.Fatalf("undo remove: %+v", r)
	}
	if !skillHashIs(dest, hash) {
		t.Fatal("file was not restored")
	}
}

func TestUndoCopyOfProjectCommandReleasesExcludeLine(t *testing.T) {
	d, target, bundle, _ := fileKindFixture(t, "prompt", "review", "Do the thing.\n")
	checkout := target.Target.Checkout
	gitIn(t, checkout, "init", "-q")
	if r := d.copyProjectSkill(target, bundle); r.State != "applied" {
		t.Fatalf("copy: %+v", r)
	}
	if r := d.undoCopySkill(undoTargetOf(target, "undo_copy"), nil); r.State != "applied" {
		t.Fatalf("undo: %+v", r)
	}
	if _, err := os.Lstat(filepath.Join(checkout, ".claude/commands/review.md")); !os.IsNotExist(err) {
		t.Fatalf("copy remains: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(checkout, ".git/info/exclude")); strings.Contains(string(b), "review.md") {
		t.Fatalf("owned exclude line remains: %q", b)
	}
}

// Removing an observed command backs it up and forgets it; the same works when
// the backup folder is on another filesystem. An undo restores it.
func TestRemoveObservedCommandAndAgentBacksUpAndRestores(t *testing.T) {
	for _, exdevMode := range []bool{false, true} {
		for _, c := range []struct{ kind, name, root string }{{"prompt", "review", ".claude/commands"}, {"agent", "planner", ".claude/agents"}} {
			name := c.kind
			if exdevMode {
				name += "-across-filesystems"
			}
			t.Run(name, func(t *testing.T) {
				d, target, _, hash := fileKindFixture(t, c.kind, c.name, "Do the thing.\n")
				if exdevMode {
					skillRename = exdev
					defer func() { skillRename = func(from, to skillPath) error { return renameSkillAt(from, to, false) } }()
				}
				source := filepath.Join(d.cfg.InventoryHome, c.root, c.name+".md")
				if err := os.MkdirAll(filepath.Dir(source), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(source, []byte("Do the thing.\n"), 0600); err != nil {
					t.Fatal(err)
				}
				d.toolsReport.Items = []protocol.InventoryItem{{Kind: c.kind, Name: c.name, Location: source, Hash: hash, Source: protocol.InventoryNative, Scope: protocol.InventoryUser, State: protocol.InventoryOK}}
				target.Revision.Assignment = protocol.ToolAssignment{Scope: "uninstalled"}
				target.Target = protocol.ToolPlanTarget{Harness: "claude", Action: "remove", Source: source}
				if r := d.removeSkill(target); r.State != "applied" {
					t.Fatalf("remove: %+v", r)
				}
				if _, err := os.Lstat(source); !os.IsNotExist(err) {
					t.Fatalf("source remains: %v", err)
				}
				if d.findSkillBackup(target.Revision.Item, source, hash) == "" {
					t.Fatal("no backup of the removed file")
				}
				_ = filepath.WalkDir(d.cfg.StateDir, func(p string, e os.DirEntry, err error) error {
					if err == nil && strings.HasPrefix(e.Name(), skillStagePrefix) {
						t.Errorf("stage remains: %s", p)
					}
					return nil
				})
				if journalCount(t, d.cfg.StateDir) != 0 {
					t.Fatal("journal remains")
				}
				if r := d.undoRemoveSkill(undoTargetOf(target, "undo_remove")); r.State != "applied" {
					t.Fatalf("undo: %+v", r)
				}
				if !skillHashIs(source, hash) {
					t.Fatal("file was not restored")
				}
			})
		}
	}
}

func TestRemoveCommandRefusesChangedFile(t *testing.T) {
	d, target, _, hash := fileKindFixture(t, "prompt", "review", "Do the thing.\n")
	source := filepath.Join(d.cfg.InventoryHome, ".claude/commands/review.md")
	if err := os.MkdirAll(filepath.Dir(source), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("edited since the scan\n"), 0600); err != nil {
		t.Fatal(err)
	}
	d.toolsReport.Items = []protocol.InventoryItem{{Kind: "prompt", Name: "review", Location: source, Hash: hash, Source: protocol.InventoryNative, Scope: protocol.InventoryUser, State: protocol.InventoryOK}}
	target.Target = protocol.ToolPlanTarget{Harness: "claude", Action: "remove", Source: source}
	if r := d.removeSkill(target); r.State != "conflict" || r.Reason != "changed since plan" {
		t.Fatalf("remove: %+v", r)
	}
	if b, _ := os.ReadFile(source); string(b) != "edited since the scan\n" {
		t.Fatalf("file changed: %q", b)
	}
}

func TestProbeOfCommandDestination(t *testing.T) {
	d, target, _, _ := fileKindFixture(t, "prompt", "review", "x")
	gitIn(t, target.Target.Checkout, "init", "-q")
	probe := protocol.ToolPlanProbe{Kind: "prompt", Name: "review", Harness: "claude", Root: ".claude/commands", Project: "project", Checkout: target.Target.Checkout}
	r := d.probeToolDestination(probe)
	if r.Conflict != "" || r.Destination != filepath.Join(target.Target.Checkout, ".claude/commands/review.md") {
		t.Fatalf("probe: %+v", r)
	}
	bad := probe
	bad.Root = ".agents/skills"
	if r = d.probeToolDestination(bad); r.Conflict == "" {
		t.Fatalf("wrong root accepted: %+v", r)
	}
	if err := os.MkdirAll(filepath.Join(target.Target.Checkout, ".claude/commands"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target.Target.Checkout, ".claude/commands/review.md"), []byte("mine"), 0600); err != nil {
		t.Fatal(err)
	}
	if r = d.probeToolDestination(probe); r.Conflict != "destination already exists and is not owned" {
		t.Fatalf("existing file: %+v", r)
	}
}

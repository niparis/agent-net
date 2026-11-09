package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
)

// deployFixture puts a skill into a temp home through the real copy path, so
// the manifest records what the daemon deployed.
func deployFixture(t *testing.T, d *Daemon, name, content string) (hash string, bundle []byte) {
	t.Helper()
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	hash, err := inventory.SkillHash(src)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err = inventory.PackSkill(src)
	if err != nil {
		t.Fatal(err)
	}
	target := protocol.ToolTarget{Name: name, Revision: protocol.ToolRevision{Item: "item-" + hash[:8], Revision: 1, Version: hash, Assignment: protocol.ToolAssignment{Scope: "global"}}, Target: protocol.ToolPlanTarget{Harness: "claude", Action: "copy"}}
	if r := d.copyGlobalSkill(target, bundle); r.State != "applied" {
		t.Fatalf("deploy: %+v", r)
	}
	return hash, bundle
}

// scanFixture scans the fake home and applies the daemon's drift marking, the
// same two steps scanTools runs, without the gateway calls.
func scanFixture(d *Daemon) protocol.InventoryReport {
	report := inventory.Scan(inventory.Options{Home: d.cfg.InventoryHome, Machine: d.cfg.Hostname})
	d.markChangedSkills(&report)
	return report
}

func findSkill(t *testing.T, report protocol.InventoryReport, name string) protocol.InventoryItem {
	t.Helper()
	for _, it := range report.Items {
		if it.Kind == protocol.InventorySkill && it.Name == name && it.Harness == "claude" {
			return it
		}
	}
	t.Fatalf("skill %s not in report", name)
	return protocol.InventoryItem{}
}

func TestDeployedCopyThatIsEditedIsReportedChanged(t *testing.T) {
	home, state := t.TempDir(), t.TempDir()
	d := &Daemon{cfg: Config{InventoryHome: home, StateDir: state, Hostname: "fixture"}}
	hash, _ := deployFixture(t, d, "demo", "v1 text")

	// An unedited copy is not reported.
	it := findSkill(t, scanFixture(d), "demo")
	if it.ChangedHere || it.DeployedHash != hash {
		t.Fatalf("unedited copy: changed=%v deployed=%q want hash %q", it.ChangedHere, it.DeployedHash, hash)
	}

	// Edit it: now it is changed here and says which version it came from.
	if err := os.WriteFile(filepath.Join(home, ".claude/skills/demo/SKILL.md"), []byte("v1 text, improved"), 0600); err != nil {
		t.Fatal(err)
	}
	it = findSkill(t, scanFixture(d), "demo")
	if !it.ChangedHere || it.DeployedHash != hash || it.Hash == hash || it.Hash == "" {
		t.Fatalf("edited copy: changed=%v deployed=%q hash=%q", it.ChangedHere, it.DeployedHash, it.Hash)
	}
}

func TestHandMadeCopyIsNeverChangedHere(t *testing.T) {
	home, state := t.TempDir(), t.TempDir()
	d := &Daemon{cfg: Config{InventoryHome: home, StateDir: state, Hostname: "fixture"}}
	dir := filepath.Join(home, ".claude/skills/mine")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("mine"), 0600); err != nil {
		t.Fatal(err)
	}
	it := findSkill(t, scanFixture(d), "mine")
	if it.ChangedHere || it.DeployedHash != "" {
		t.Fatalf("hand-made copy: %+v", it)
	}
}

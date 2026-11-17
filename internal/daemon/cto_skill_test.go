package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/niparis/agent-net/internal/daemon/ctoskill"
	"github.com/niparis/agent-net/internal/protocol"
)

// ctoSkillGateway is the gateway the CTO's skill needs: it takes the bundle the
// daemon ships, serves it in the CTO's skill folder, and records the list adds.
type ctoSkillGateway struct {
	mu       sync.Mutex
	skills   *skillFolderGateway
	shipped  []protocol.SkillArchiveBundled
	adds     []protocol.SkillSetRequest
	failShip bool
}

func (g *ctoSkillGateway) call(ctx context.Context, typ string, params any) (json.RawMessage, bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	switch typ {
	case protocol.FrameSkillArchiveBundled:
		p := params.(protocol.SkillArchiveBundled)
		if g.failShip {
			return nil, true, errors.New("gateway is down")
		}
		g.shipped = append(g.shipped, p)
		_, hash, err := packCTOSkillOnce()
		if err != nil {
			return nil, true, err
		}
		g.skills.bundles = map[string][]byte{skillVersionKey(p.Name, 1): p.Bundle}
		g.skills.folder = protocol.SkillFolder{Level: "agent", Scope: "cto", Skills: []protocol.EffectiveSkill{{Name: p.Name, Version: 1, Hash: hash}}}
		return json.RawMessage(`{}`), true, nil
	case protocol.FrameSkillsSet:
		g.adds = append(g.adds, params.(protocol.SkillSetRequest))
		return json.RawMessage(`{}`), true, nil
	case protocol.FrameSkillsFolder, protocol.FrameSkillsVersion:
		return g.skills.call(ctx, typ, params)
	}
	return json.RawMessage(`null`), true, nil
}

// ctoSkillDaemon is a daemon with a state folder and the fake gateway.
func ctoSkillDaemon(t *testing.T) (*Daemon, *ctoSkillGateway) {
	t.Helper()
	st, err := openState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.close() })
	g := &ctoSkillGateway{skills: &skillFolderGateway{}}
	d := &Daemon{cfg: Config{StateDir: t.TempDir(), testGatewayCall: g.call}, st: st, log: log.New(io.Discard, "", 0)}
	return d, g
}

// The CTO's skill goes to the archive once, and is added to the CTO's list once.
// A second ensure with the same content sends nothing, and an owner who took it
// off the list keeps it off.
func TestCTOSkillShipsOnceAndListsOnce(t *testing.T) {
	d, g := ctoSkillDaemon(t)
	ctx := context.Background()
	if err := d.ensureCTOSkill(ctx); err != nil {
		t.Fatal(err)
	}
	if len(g.shipped) != 1 || g.shipped[0].Name != ctoskill.Name || g.shipped[0].By != "cto" {
		t.Fatalf("shipped %+v", g.shipped)
	}
	want := protocol.SkillSetRequest{Level: "agent", Scope: "cto", Add: []string{ctoskill.Name}, By: "cto"}
	if len(g.adds) != 1 || g.adds[0].Level != want.Level || g.adds[0].Scope != want.Scope || len(g.adds[0].Add) != 1 || g.adds[0].Add[0] != ctoskill.Name {
		t.Fatalf("list adds %+v", g.adds)
	}
	if err := d.ensureCTOSkill(ctx); err != nil {
		t.Fatal(err)
	}
	if len(g.shipped) != 1 || len(g.adds) != 1 {
		t.Fatalf("a second ensure sent again: shipped %d, adds %d", len(g.shipped), len(g.adds))
	}
}

// Both CTO harnesses get the skill from the CTO node's skill folder: Claude as a
// plugin folder, OpenCode as its skills root.
func TestCTOSkillReachesBothHarnessesFromTheArchive(t *testing.T) {
	d, _ := ctoSkillDaemon(t)
	ctx := context.Background()
	if err := d.ensureCTOSkill(ctx); err != nil {
		t.Fatal(err)
	}
	folder, err := d.skillFolderFor(ctx, skillNode{Agent: "cto"})
	if err != nil || folder.Dir == "" {
		t.Fatalf("CTO skill folder: %+v %v", folder, err)
	}
	got, err := os.ReadFile(filepath.Join(folder.Root, ctoskill.Name, "SKILL.md"))
	want, _ := ctoskill.File("SKILL.md")
	if err != nil || string(got) != string(want) {
		t.Fatalf("skill in the folder: %v", err)
	}

	spec := d.claudeStartSpec(ctx, claudeStartInput{Role: roleCTO, Skills: skillNode{Agent: "cto"}})
	if argValue(spec.Flags, "--plugin-dir") != folder.Dir {
		t.Fatalf("Claude CTO flags %v, want the plugin folder %s", spec.Flags, folder.Dir)
	}

	var cfg struct {
		Skills []string `json:"skills"`
	}
	if err := json.Unmarshal([]byte(d.openCodeSkillsConfig(ctx, skillNode{Agent: "cto"}, "")), &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Skills) != 1 || cfg.Skills[0] != folder.Root {
		t.Fatalf("OpenCode CTO skills %v, want %s", cfg.Skills, folder.Root)
	}
}

// The copies that earlier builds wrote into the CTO's harness folders are gone
// after an ensure, and an ensure does not write them again.
func TestCTOSkillOldHarnessCopiesAreRemoved(t *testing.T) {
	d, _ := ctoSkillDaemon(t)
	old := []string{
		filepath.Join(d.cfg.StateDir, "cto", "claude", ".claude", "skills", ctoskill.Name),
		filepath.Join(d.cfg.StateDir, "cto", ".claude", "skills", ctoskill.Name),
	}
	for _, dir := range old {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("old"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.ensureCTOSkill(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, dir := range old {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatalf("old copy %s is still there: %v", dir, err)
		}
	}
}

// A failed ship is retried by the next start: the daemon marks the skill as
// ensured only after the gateway took it.
func TestCTOSkillEnsureRetriesAfterGatewayFailure(t *testing.T) {
	d, g := ctoSkillDaemon(t)
	g.failShip = true
	d.ensureCTOSkillLogged()
	if d.ctoSkillDone.Load() {
		t.Fatal("a failed ship marked the skill as done")
	}
	g.failShip = false
	d.ensureCTOSkillLogged()
	if !d.ctoSkillDone.Load() || len(g.shipped) != 1 || len(g.adds) != 1 {
		t.Fatalf("retry: done %v, shipped %d, adds %d", d.ctoSkillDone.Load(), len(g.shipped), len(g.adds))
	}
}

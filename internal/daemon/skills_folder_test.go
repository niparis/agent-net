package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
)

// skillBundle packs one skill folder and returns its bundle and hash, the way
// the archive stores a version.
func skillBundle(t *testing.T, body string) ([]byte, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	hash, err := inventory.SkillHash(dir)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := inventory.PackSkill(dir)
	if err != nil {
		t.Fatal(err)
	}
	return bundle, hash
}

// skillFolderGateway answers the launch reads of a folder-build test:
// skills.folder for the set and skills.version for each bundle.
type skillFolderGateway struct {
	mu      sync.Mutex
	folder  protocol.SkillFolder
	bundles map[string][]byte
	err     error
	calls   int
	reqs    []protocol.SkillFolderRequest
}

func (g *skillFolderGateway) call(_ context.Context, typ string, params any) (json.RawMessage, bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if typ != protocol.FrameSkillsFolder && typ != protocol.FrameSkillsVersion {
		return nil, false, nil
	}
	g.calls++
	if req, ok := params.(protocol.SkillFolderRequest); ok {
		g.reqs = append(g.reqs, req)
	}
	if g.err != nil {
		return nil, true, g.err
	}
	if typ == protocol.FrameSkillsFolder {
		raw, err := json.Marshal(g.folder)
		return raw, true, err
	}
	req, _ := params.(protocol.SkillVersionRequest)
	version := protocol.SkillVersion{Name: req.Name, Version: req.Version, Data: g.bundles[skillVersionKey(req.Name, req.Version)]}
	for _, skill := range g.folder.Skills {
		if skill.Name == req.Name && skill.Version == req.Version {
			version.Hash = skill.Hash
		}
	}
	raw, err := json.Marshal(version)
	return raw, true, err
}

func skillVersionKey(name string, version int) string { return name + "@" + strconv.Itoa(version) }

func skillFolderDaemon(t *testing.T, g *skillFolderGateway) (*Daemon, *logBuf) {
	t.Helper()
	logs := &logBuf{}
	return &Daemon{cfg: Config{StateDir: t.TempDir(), testGatewayCall: g.call}, log: log.New(logs, "", 0)}, logs
}

// addSkill stores one version in the fake gateway and returns its entry in the
// set. The folder build then reads the bundle by name and version.
func addSkill(t *testing.T, g *skillFolderGateway, name string, version int, body string) protocol.EffectiveSkill {
	t.Helper()
	data, hash := skillBundle(t, body)
	if g.bundles == nil {
		g.bundles = map[string][]byte{}
	}
	g.bundles[skillVersionKey(name, version)] = data
	return protocol.EffectiveSkill{Name: name, Version: version, Hash: hash}
}

// The folder holds exactly the effective skills at their resolved versions,
// in Claude Code's plugin form, and a second launch of the same set reuses it
// while a changed set builds a new one.
func TestSkillFolderBuildReuseAndChange(t *testing.T) {
	g := &skillFolderGateway{folder: protocol.SkillFolder{Level: "repo", Scope: "shop"}}
	g.folder.Skills = append(g.folder.Skills, addSkill(t, g, "alpha", 1, "alpha body"), addSkill(t, g, "beta", 2, "beta body"))
	d, _ := skillFolderDaemon(t, g)

	first, ok := d.skillsForStart(context.Background(), skillNode{Repo: "shop"})
	if !ok || first.Dir == "" {
		t.Fatalf("no folder: %+v %v", first, ok)
	}
	wantRoot := filepath.Join(d.cfg.StateDir, "skills")
	if !strings.HasPrefix(first.Dir, wantRoot+string(filepath.Separator)) {
		t.Fatalf("folder %q is not under %q", first.Dir, wantRoot)
	}
	var plugin struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	raw, err := os.ReadFile(filepath.Join(first.Dir, ".claude-plugin", "plugin.json"))
	if err != nil || json.Unmarshal(raw, &plugin) != nil || plugin.Name == "" || plugin.Version == "" {
		t.Fatalf("plugin.json: %v %s", err, raw)
	}
	for name, version := range map[string]string{"alpha": "alpha body", "beta": "beta body"} {
		body, err := os.ReadFile(filepath.Join(first.Root, name, "SKILL.md"))
		if err != nil || string(body) != version {
			t.Fatalf("%s: %v %q", name, err, body)
		}
	}
	if len(first.Skills) != 2 || first.Skills[0] != filepath.Join(first.Root, "alpha") || first.Skills[1] != filepath.Join(first.Root, "beta") {
		t.Fatalf("skill folders: %v", first.Skills)
	}
	if len(g.reqs) != 1 || g.reqs[0].Repo != "shop" || g.reqs[0].By != "owner" {
		t.Fatalf("targets: %+v", g.reqs)
	}

	marker := filepath.Join(first.Root, "alpha", "marker")
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	again, ok := d.skillsForStart(context.Background(), skillNode{Repo: "shop"})
	if !ok || again.Dir != first.Dir {
		t.Fatalf("the same set built another folder: %q then %q", first.Dir, again.Dir)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the folder was rebuilt: %v", err)
	}

	g.mu.Lock()
	g.folder = protocol.SkillFolder{Level: "repo", Scope: "shop"}
	g.mu.Unlock()
	changedSkill := addSkill(t, g, "alpha", 3, "alpha version three")
	g.mu.Lock()
	g.folder.Skills = []protocol.EffectiveSkill{changedSkill}
	g.mu.Unlock()
	changed, ok := d.skillsForStart(context.Background(), skillNode{Repo: "shop"})
	if !ok || changed.Dir == first.Dir {
		t.Fatalf("a changed set reused the old folder: %q", changed.Dir)
	}
	body, err := os.ReadFile(filepath.Join(changed.Root, "alpha", "SKILL.md"))
	if err != nil || string(body) != "alpha version three" {
		t.Fatalf("changed content: %v %q", err, body)
	}
	if len(changed.Skills) != 1 {
		t.Fatalf("changed folders: %v", changed.Skills)
	}
}

// An empty effective list gets no folder and no error.
func TestSkillFolderEmptyListBuildsNothing(t *testing.T) {
	g := &skillFolderGateway{folder: protocol.SkillFolder{Level: "repo", Scope: "shop"}}
	d, logs := skillFolderDaemon(t, g)
	f, ok := d.skillsForStart(context.Background(), skillNode{Repo: "shop"})
	if ok || f.Dir != "" || len(f.Skills) != 0 {
		t.Fatalf("empty list: %+v %v", f, ok)
	}
	if len(logs.lines()) != 0 {
		t.Fatalf("an empty list was logged as a failure: %v", logs.lines())
	}
	if entries, _ := os.ReadDir(filepath.Join(d.cfg.StateDir, "skills")); len(entries) != 0 {
		t.Fatalf("a folder was made: %v", entries)
	}
}

// A failure to build never blocks the launch: the caller gets no folder, and
// one log line says why.
func TestSkillFolderFailureIsLoggedNotFatal(t *testing.T) {
	g := &skillFolderGateway{err: errors.New("gateway is away")}
	d, logs := skillFolderDaemon(t, g)
	f, ok := d.skillsForStart(context.Background(), skillNode{Agent: "ag"})
	if ok || f.Dir != "" {
		t.Fatalf("a failed build gave a folder: %+v", f)
	}
	lines := logs.lines("skills:")
	if len(lines) != 1 || !strings.Contains(lines[0], "gateway is away") {
		t.Fatalf("log lines: %v", logs.lines())
	}
}

// A skill name that could escape the folder is refused, not written.
func TestSkillFolderRefusesUnsafeName(t *testing.T) {
	g := &skillFolderGateway{folder: protocol.SkillFolder{Level: "global"}}
	bad := addSkill(t, g, "..", 1, "body")
	g.folder.Skills = []protocol.EffectiveSkill{bad}
	d, _ := skillFolderDaemon(t, g)
	if f, ok := d.skillsForStart(context.Background(), skillNode{}); ok || f.Dir != "" {
		t.Fatalf("an unsafe name was accepted: %+v", f)
	}
}

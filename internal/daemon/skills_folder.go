package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
)

// Skills a launch gets (decision 0169, task 4). The daemon owns one folder per
// effective skill set under its own state folder. The folder is in Claude
// Code's plugin form, so Claude takes it as one --plugin-dir; the harnesses
// that take one path per skill read <dir>/skills/<name>, and the ones that take
// one root of skill folders read <dir>/skills. Nothing is written into a
// project, a worktree or a user skill folder.

// skillNode names the graph node a launch takes its skills from. Session is an
// existing session, which the gateway resolves to its Agent, else its Repo.
type skillNode struct {
	Session string
	Agent   string
	Repo    string
}

func (n skillNode) empty() bool { return n.Session == "" && n.Agent == "" && n.Repo == "" }

// skillFolder is a built set: Dir is the plugin root, Root the folder that
// holds one folder per skill, Skills each skill folder.
type skillFolder struct {
	Dir    string
	Root   string
	Skills []string
}

// skillBuildTimeout bounds the gateway read and the unpack, so a gateway that
// stops answering delays a start by seconds and not by its start timeout. A
// failure only costs the skills: the launch goes on without the folder.
const skillBuildTimeout = 10 * time.Second

// skillsForStart is the launcher's way in. It never fails the launch: a build
// that does not work gives no folder and one log line that says why.
func (d *Daemon) skillsForStart(ctx context.Context, n skillNode) (skillFolder, bool) {
	folder, err := d.skillFolderFor(ctx, n)
	if err != nil {
		d.Logf("skills: no skills folder for %s: %v", n.describe(), err)
		return skillFolder{}, false
	}
	return folder, folder.Dir != ""
}

// describe names the target in a log line without a session ID when it has
// none.
func (n skillNode) describe() string {
	switch {
	case n.Session != "":
		return "session " + n.Session
	case n.Agent != "":
		return "agent " + n.Agent
	case n.Repo != "":
		return "repo " + n.Repo
	}
	return "the global node"
}

// skillFolderFor reads one node's effective skills and returns its folder,
// building it when this set has none yet. A set is named by the hash of its
// skills at their resolved versions, so an unchanged set is reused and a
// changed one gets a new folder. An empty set has no folder.
func (d *Daemon) skillFolderFor(ctx context.Context, n skillNode) (skillFolder, error) {
	if d.cfg.StateDir == "" {
		return skillFolder{}, errors.New("no state folder")
	}
	rctx, cancel := context.WithTimeout(ctx, skillBuildTimeout)
	defer cancel()
	raw, err := d.call(rctx, protocol.FrameSkillsFolder, protocol.SkillFolderRequest{Session: n.Session, Agent: n.Agent, Repo: n.Repo, By: "owner"})
	if err != nil {
		return skillFolder{}, err
	}
	var set protocol.SkillFolder
	if err := json.Unmarshal(raw, &set); err != nil {
		return skillFolder{}, err
	}
	// A name whose pin left the archive has no version to deliver.
	skills := make([]protocol.EffectiveSkill, 0, len(set.Skills))
	for _, skill := range set.Skills {
		if skill.Version > 0 && skill.Hash != "" {
			skills = append(skills, skill)
		}
	}
	if len(skills) == 0 {
		return skillFolder{}, nil
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })
	dir := filepath.Join(d.cfg.StateDir, "skills", skillSetHash(skills))
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		return skillFolderAt(dir), nil
	}
	if err := d.buildSkillFolder(rctx, dir, skills); err != nil {
		// Another launch of the same set may have built it while this one
		// unpacked. Its folder is the right answer.
		if info, statErr := os.Stat(dir); statErr == nil && info.IsDir() {
			return skillFolderAt(dir), nil
		}
		return skillFolder{}, err
	}
	return skillFolderAt(dir), nil
}

// skillSetHash names one set. The version and the content hash both count, so
// a pin to another version or a replaced archive bundle builds a new folder.
func skillSetHash(skills []protocol.EffectiveSkill) string {
	h := sha256.New()
	for _, skill := range skills {
		fmt.Fprintf(h, "%s\x00%d\x00%s\n", skill.Name, skill.Version, skill.Hash)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// skillFolderAt is the folder and the paths the harnesses take from it.
func skillFolderAt(dir string) skillFolder {
	root := filepath.Join(dir, "skills")
	folder := skillFolder{Dir: dir, Root: root}
	entries, _ := os.ReadDir(root)
	for _, entry := range entries {
		if entry.IsDir() {
			folder.Skills = append(folder.Skills, filepath.Join(root, entry.Name()))
		}
	}
	return folder
}

// buildSkillFolder unpacks the set into a fresh folder and moves it into place
// in one rename, so a launch never sees a half-built set.
func (d *Daemon) buildSkillFolder(ctx context.Context, dir string, skills []protocol.EffectiveSkill) error {
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(parent, ".build-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	for _, skill := range skills {
		if !safeSkillDirName(skill.Name) {
			return fmt.Errorf("skill name %q cannot be a folder", skill.Name)
		}
		data, err := d.skillVersionBundle(ctx, skill)
		if err != nil {
			return err
		}
		target := filepath.Join(tmp, "skills", skill.Name)
		if err := os.MkdirAll(target, 0o700); err != nil {
			return err
		}
		if err := inventory.UnpackSkill(data, target); err != nil {
			return fmt.Errorf("skill %s: %w", skill.Name, err)
		}
		// The gateway sent the version whose bundle it stored. Check the bytes
		// that arrived are that version before a session sees them.
		got, err := inventory.SkillHash(target)
		if err != nil {
			return fmt.Errorf("skill %s: %w", skill.Name, err)
		}
		if got != skill.Hash {
			return fmt.Errorf("skill %s: bundle hash %s, archive says %s", skill.Name, got, skill.Hash)
		}
	}
	pluginDir := filepath.Join(tmp, ".claude-plugin")
	if err := os.MkdirAll(pluginDir, 0o700); err != nil {
		return err
	}
	name := "agent-net-skills-" + filepath.Base(dir)
	plugin, _ := json.Marshal(map[string]string{"name": name, "version": filepath.Base(dir)})
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.json"), plugin, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, dir)
}

// skillVersionBundle reads the stored bundle of one version. The gateway
// sends the hash it stored, and the caller checks the unpacked files against
// it, so a damaged bundle never reaches a session.
func (d *Daemon) skillVersionBundle(ctx context.Context, skill protocol.EffectiveSkill) ([]byte, error) {
	raw, err := d.call(ctx, protocol.FrameSkillsVersion, protocol.SkillVersionRequest{Name: skill.Name, Version: skill.Version, By: "owner"})
	if err != nil {
		return nil, fmt.Errorf("skill %s: %w", skill.Name, err)
	}
	var version protocol.SkillVersion
	if err := json.Unmarshal(raw, &version); err != nil {
		return nil, fmt.Errorf("skill %s: %w", skill.Name, err)
	}
	if version.Version != skill.Version || version.Hash != skill.Hash {
		return nil, fmt.Errorf("skill %s: the archive returned version %d at hash %s, wanted %d at %s",
			skill.Name, version.Version, version.Hash, skill.Version, skill.Hash)
	}
	return version.Data, nil
}

// safeSkillDirName keeps every skill inside the set folder. The archive names
// are safe already; this is the check at the folder, where it matters.
func safeSkillDirName(name string) bool {
	if name == "" || name == "." || name == ".." || len(name) > 200 {
		return false
	}
	if strings.TrimSpace(name) != name || strings.ContainsAny(name, `/\`+"\x00") {
		return false
	}
	for _, r := range name {
		if r < 0x20 {
			return false
		}
	}
	return true
}

// openCodeSkillsEnv is OpenCode's per-process config with the set's skill
// folder root. It merges with a config the launcher already passes, and adds
// to the owner's own skill folders: OpenCode does not replace them
// (decision 0169, task 4). An empty root leaves the config as it was.
func openCodeSkillsEnv(existing, root string) (string, error) {
	if root == "" {
		return existing, nil
	}
	cfg := map[string]any{}
	if existing != "" {
		if err := json.Unmarshal([]byte(existing), &cfg); err != nil {
			return "", fmt.Errorf("existing OpenCode config is not JSON: %w", err)
		}
	}
	cfg["skills"] = []string{root}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// codexSkillServer is a Codex app server the daemon drives: a worker's own one
// or the owner's shared one.
type codexSkillServer interface {
	call(ctx context.Context, method string, params any) (json.RawMessage, error)
}

// setCodexSkillRoots tells a Codex app server where this launch's skills are.
// Codex has no flag for skills: the running server takes the roots and picks
// up a change live (decision 0169, task 4). A failure costs only the skills.
func (d *Daemon) setCodexSkillRoots(ctx context.Context, cc codexSkillServer, n skillNode) {
	folder, ok := d.skillsForStart(ctx, n)
	if !ok || cc == nil {
		return
	}
	if _, err := cc.call(ctx, "skills/extraRoots/set", map[string]any{"extraRoots": []string{folder.Root}}); err != nil {
		d.Logf("skills: Codex did not take the skills folder of %s: %v", n.describe(), err)
	}
}

// openCodeSkillsConfig is openCodeSkillsEnv for one launch target: the merged
// config to hand the process, or the config it already had when the set is
// empty or cannot be built.
func (d *Daemon) openCodeSkillsConfig(ctx context.Context, n skillNode, existing string) string {
	folder, ok := d.skillsForStart(ctx, n)
	if !ok {
		return existing
	}
	cfg, err := openCodeSkillsEnv(existing, folder.Root)
	if err != nil {
		d.Logf("skills: no OpenCode skill folder for %s: %v", n.describe(), err)
		return existing
	}
	return cfg
}

// startSkillNode is the node a new session reads: the Agent the owner chose,
// else the Repo of its project.
func startSkillNode(agent, project string) skillNode {
	if agent != "" {
		return skillNode{Agent: agent}
	}
	return skillNode{Repo: project}
}

// workerSkillNode is the node a worker launch reads. A worker has no Agent
// identity of its own (only its caller has one), so its node is the project's
// Repo, which the gateway resolves along global, team and repo.
func workerSkillNode(w *worker) skillNode {
	return skillNode{Repo: w.project}
}

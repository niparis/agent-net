package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
)

type skillManifestEntry struct {
	Item         string `json:"item"`
	Path         string `json:"path"`
	Hash         string `json:"hash"`
	Revision     int64  `json:"revision"`
	Owned        bool   `json:"owned"`
	Exclude      string `json:"exclude,omitempty"`
	ExcludeLine  string `json:"exclude_line,omitempty"`
	ExcludeOwned bool   `json:"exclude_owned,omitempty"`
}

func (d *Daemon) recordSkill(t protocol.ToolTarget, dest string, owned bool) error {
	path := filepath.Join(d.cfg.StateDir, "skill-manifest.json")
	var entries []skillManifestEntry
	b, err := os.ReadFile(path)
	if err == nil {
		if err = json.Unmarshal(b, &entries); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	found := false
	for i := range entries {
		if entries[i].Path == dest {
			if entries[i].Item == t.Revision.Item && entries[i].Owned {
				if info, err := os.Lstat(dest); err == nil && (info.IsDir() || info.Mode().IsRegular()) {
					owned = true
				}
			}
			previous := entries[i]
			entries[i] = skillManifestEntry{Item: t.Revision.Item, Path: dest, Hash: t.Revision.Version, Revision: t.Revision.Revision, Owned: owned, Exclude: previous.Exclude, ExcludeLine: previous.ExcludeLine, ExcludeOwned: previous.ExcludeOwned}
			found = true
			break
		}
	}
	if !found {
		entries = append(entries, skillManifestEntry{Item: t.Revision.Item, Path: dest, Hash: t.Revision.Version, Revision: t.Revision.Revision, Owned: owned})
	}
	b, err = json.Marshal(entries)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(d.cfg.StateDir, ".skill-manifest-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}

func (d *Daemon) copyGlobalSkill(t protocol.ToolTarget, bundle []byte) protocol.ToolResult {
	r := protocol.ToolResult{Item: t.Revision.Item, Revision: t.Revision.Revision, Index: t.Index}
	fail := func(state, reason string) protocol.ToolResult { r.State = state; r.Reason = reason; return r }
	if t.Revision.Assignment.Scope != "global" || t.Target.Checkout != "" || t.Target.Action != "copy" {
		return fail("pending", "operation is not supported yet")
	}
	if t.Target.Conflict != "" && t.Target.Conflict != "destination already exists and is not owned" && t.Target.Conflict != "machine offline; destination cannot be inspected" {
		return fail("conflict", "plan target requires inspection")
	}
	kind := itemKind(t.Revision.Item)
	entry := protocol.BundleEntryName(kind, t.Name)
	root := d.cfg.InventoryHome
	rel := globalSkillRel(t.Target.Harness)
	if protocol.IsFileKind(kind) {
		// A prompt or an agent has one root, named by the plan.
		if !fileKindRoot(kind, t.Target.Root) || t.Target.Harness != "claude" {
			return fail("conflict", "unsupported skill root")
		}
		rel = t.Target.Root
	} else if t.Target.Root != "" {
		rel = t.Target.Root
		if rel != ".claude/skills" && rel != ".agents/skills" && rel != ".gemini/config/skills" {
			return fail("conflict", "unsupported skill root")
		}
		if rel == ".gemini/config/skills" {
			info, err := os.Lstat(filepath.Join(root, ".gemini"))
			if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return fail("conflict", "destination root unavailable")
			}
		}
	}
	if rel == "" {
		return fail("manual", "unsupported harness")
	}
	if t.Target.Destination != "" && filepath.Clean(t.Target.Destination) != filepath.Join(root, rel, entry) {
		return fail("conflict", "planned destination differs")
	}
	if t.Target.Root != "" && !protocol.IsFileKind(kind) {
		for _, other := range []string{".codex/skills", ".config/opencode/skills", ".pi/agent/skills"} {
			if _, err := os.Lstat(filepath.Join(root, other, t.Name)); err == nil || !os.IsNotExist(err) {
				return fail("conflict", "another skill source may shadow the planned path")
			}
		}
	}
	return d.copySkillAt(t, bundle, root, rel)
}

// globalSkillRel is the user skill root of a harness, relative to home.
func globalSkillRel(harness string) string {
	switch harness {
	case "claude":
		return ".claude/skills"
	case "codex":
		return ".agents/skills"
	case "opencode":
		return ".config/opencode/skills"
	case "pi":
		return ".pi/agent/skills"
	}
	return ""
}

// copySkillAt stages and installs a verified bundle under the given root.
// Callers must verify that the root and destination are authorized.
func (d *Daemon) copySkillAt(t protocol.ToolTarget, bundle []byte, root, rel string) protocol.ToolResult {
	r := protocol.ToolResult{Item: t.Revision.Item, Revision: t.Revision.Revision, Index: t.Index}
	fail := func(state, reason string) protocol.ToolResult { r.State = state; r.Reason = reason; return r }
	if t.Name == "" || t.Name == "." || t.Name == ".." || filepath.Base(t.Name) != t.Name || strings.ContainsAny(t.Name, "/\\\x00") {
		return fail("conflict", "invalid skill name")
	}
	if root == "" || !filepath.IsAbs(root) || d.cfg.StateDir == "" {
		return fail("failed", "local directories unavailable")
	}
	kind := itemKind(t.Revision.Item)
	entry := protocol.BundleEntryName(kind, t.Name)
	manifestDest := filepath.Join(root, rel, entry)
	if reason := d.skillConflict(manifestDest); reason != "" {
		return fail("conflict", reason)
	}
	j, err := newSkillJournal(d.cfg.StateDir, skillJournalRecord{Operation: "copy", Target: manifestDest, AfterHash: t.Revision.Version, Item: t.Revision.Item, Revision: t.Revision.Revision, Step: "prepared"})
	if err != nil {
		return fail("failed", "cannot write skill journal")
	}
	retain := false
	defer func() {
		if !retain {
			_ = j.finish()
		}
	}()
	cur, err := makeSkillRoot(root, rel)
	if err != nil {
		return fail("conflict", "skill root cannot be opened safely")
	}
	defer cur.Close()
	dest := skillPath{cur, entry}
	if _, err = dest.lstat(); err == nil {
		hash, e := skillHashAt(dest)
		if e != nil || hash != t.Revision.Version {
			return fail("conflict", d.destinationDiffersReason(manifestDest, hash, t.Revision.Item))
		}
		if e = d.recordSkill(t, manifestDest, false); e != nil {
			return fail("failed", "cannot record manifest")
		}
		return fail("applied", "")
	} else if !os.IsNotExist(err) {
		return fail("failed", "cannot inspect destination")
	}
	stageName := skillStagePrefix + filepath.Base(j.path)
	stage := skillPath{cur, stageName}
	j.record.Stage = filepath.Join(root, rel, stageName)
	if err = j.advance("staging"); err != nil {
		retain = true
		return fail("failed", "cannot update skill journal")
	}
	if err = stage.mkdir(); err != nil {
		return fail("failed", "cannot create staging directory")
	}
	defer func() {
		if !retain {
			_ = stage.removeAll()
		}
	}()
	if err = unpackSkillAt(bundle, stage); err != nil {
		return fail("failed", "invalid skill bundle")
	}
	hash, err := skillHashAt(stage)
	if err != nil || hash != t.Revision.Version {
		return fail("failed", "skill hash mismatch")
	}
	if err = j.advance("staged"); err != nil {
		retain = true
		return fail("failed", "skill operation interrupted")
	}
	if err = installStaged(kind, stage, dest, entry); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fail("conflict", "destination appeared during copy")
		}
		return fail("failed", "cannot install skill")
	}
	if err = j.advance("installed"); err != nil {
		retain = true
		return fail("failed", "skill operation interrupted")
	}
	if err = d.recordSkill(t, manifestDest, true); err != nil {
		retain = true
		return fail("failed", "cannot record manifest")
	}
	if err = j.advance("recorded"); err != nil {
		retain = true
		return fail("failed", "skill operation interrupted")
	}
	return fail("applied", "")
}

// targetsForThisNode keeps the targets this machine must act on. The gateway
// already sends only these. The check stays here so that a machine the owner
// did not choose never changes, whatever it is sent.
func (d *Daemon) targetsForThisNode(in []protocol.ToolTarget) []protocol.ToolTarget {
	var out []protocol.ToolTarget
	for _, t := range in {
		if t.Target.NodeID != "" && t.Target.NodeID != d.cfg.NodeID {
			continue
		}
		if !t.Revision.Assignment.OnMachine(d.cfg.NodeID) {
			continue
		}
		out = append(out, t)
	}
	return out
}

func (d *Daemon) reconcileTools(ctx context.Context) {
	raw, err := d.call(ctx, "tools.targets", struct{}{})
	if err != nil {
		return
	}
	var targets []protocol.ToolTarget
	if json.Unmarshal(raw, &targets) != nil {
		return
	}
	targets = d.targetsForThisNode(targets)
	pass := d.beginToolsPass(targets)
	projectConflicts := projectTargets(targets)
	projects := map[string]bool{}
	// Project folders here are registered or seen in a session: any known
	// Git checkout is a skill destination (decision 0048).
	worktrees := map[string][]string{}
	projectsReady := false
	if raw, e := d.call(ctx, "query.projects", struct{}{}); e == nil {
		var result protocol.ProjectsResult
		if json.Unmarshal(raw, &result) == nil {
			projectsReady = true
			for _, p := range result.Projects {
				if p.NodeID != d.cfg.NodeID {
					continue
				}
				projects[p.Name+"\x00"+filepath.Clean(p.Path)] = true
				if _, ok := worktrees[p.Name]; !ok {
					worktrees[p.Name] = []string{}
				}
				for _, wt := range p.Worktrees {
					projects[p.Name+"\x00"+filepath.Clean(wt)] = true
					worktrees[p.Name] = append(worktrees[p.Name], wt)
				}
			}
		}
	}
	blocked := map[string]bool{}
	for _, t := range targets {
		if t.Target.Action == "inspect" && t.Target.Conflict != "" {
			blocked[t.Revision.Item] = true
		}
	}
	for _, t := range targets {
		if pass.unchanged(t) {
			continue
		}
		result := protocol.ToolResult{Item: t.Revision.Item, Revision: t.Revision.Revision, Index: t.Index, State: "pending", Reason: "operation is not supported yet"}
		if blocked[t.Revision.Item] {
			result.State = "conflict"
			result.Reason = "plan requires inspection"
		} else if t.Revision.Assignment.Scope == "projects" && t.Target.Action == "copy" && t.Target.Checkout != "" && !projectsReady {
			result.Reason = "project registry unavailable"
		} else if t.Revision.Assignment.Scope == "projects" && t.Target.Action == "copy" && t.Target.Checkout != "" && !projects[t.Target.Project+"\x00"+filepath.Clean(t.Target.Checkout)] {
			result.State = "conflict"
			result.Reason = "checkout is no longer a known project"
		} else if t.Revision.Assignment.Scope == "projects" && t.Target.Action == "copy" && t.Target.Checkout != "" && projectConflicts[t.Revision.Item+"\x00"+t.Target.Checkout] != "" {
			result.State = "conflict"
			result.Reason = projectConflicts[t.Revision.Item+"\x00"+t.Target.Checkout]
		} else if t.Target.Action == "repo_remove" && !projectsReady {
			result.Reason = "project registry unavailable"
		} else if t.Target.Action == "repo_remove" && !projects[t.Target.Project+"\x00"+filepath.Clean(t.Target.Checkout)] {
			result.State = "conflict"
			result.Reason = "checkout is no longer registered"
		} else if t.Target.Action == "repo_remove" {
			result = d.removeRepoSkill(t)
		} else if t.Target.Action == "remove" && !t.CopiesReady {
			result.Reason = "waiting for copies to be applied"
		} else if t.Target.Action == "remove" {
			result = d.removeSkill(t)
		} else if t.Target.Action == "verify" {
			result = d.verifySkillAbsence(t)
		} else if t.Target.Action == "undo_copy" {
			result = d.undoCopySkill(t, targets)
		} else if t.Target.Action == "undo_remove" {
			result = d.undoRemoveSkill(t)
		} else if t.Target.Action == "copy" && ((t.Revision.Assignment.Scope == "global" && t.Target.Checkout == "") || (t.Revision.Assignment.Scope == "projects" && t.Target.Checkout != "")) {
			data, err := pass.bundle(ctx, t)
			if errors.Is(err, errInvalidStoredBundle) {
				result.State = "failed"
				result.Reason = "invalid stored bundle"
			} else if err != nil {
				result.State = "failed"
				result.Reason = "stored skill unavailable"
			} else {
				if t.Revision.Assignment.Scope == "projects" {
					if t.Target.Destination == "" {
						var names []string
						for _, v := range targets {
							if v.Revision.Item == t.Revision.Item && v.Target.Checkout == t.Target.Checkout && v.Target.Action == "copy" {
								names = append(names, v.Target.Harness)
							}
						}
						root, _ := inventory.ProjectSkillRoot(t.Target.Harness, names)
						if t.Target.Root != "" {
							root = t.Target.Root
						}
						t.Target.Destination = filepath.Join(t.Target.Checkout, root, protocol.BundleEntryName(itemKind(t.Revision.Item), t.Name))
					}
					result = d.copyProjectSkillIn(t, data, worktrees[t.Target.Project], true)
				} else {
					result = d.copyGlobalSkill(t, data)
				}
				if result.State == "failed" && (result.Reason == "invalid skill bundle" || result.Reason == "skill hash mismatch") {
					pass.dropBundle(t)
				}
			}
		}
		pass.report(ctx, t, result)
	}
}

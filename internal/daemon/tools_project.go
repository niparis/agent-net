package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
)

func projectGit(checkout string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	all := []string{"-C", checkout, "-c", "core.fsmonitor=false"}
	if len(args) > 0 && args[0] == "ls-files" {
		all = append(all, "--literal-pathspecs")
	}
	all = append(all, args...)
	cmd := exec.CommandContext(ctx, "git", all...)
	env := os.Environ()
	cmd.Env = make([]string, 0, len(env))
	for _, v := range env {
		if !strings.HasPrefix(v, "GIT_") {
			cmd.Env = append(cmd.Env, v)
		}
	}
	return cmd.Output()
}

func projectExcludePattern(rel string) string {
	var b strings.Builder
	b.WriteByte('/')
	for _, r := range filepath.ToSlash(rel) {
		switch r {
		case '*', '?', '[', ']', '\\', ' ', '#', '!':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	b.WriteByte('/')
	return b.String()
}

func (d *Daemon) copyProjectSkill(t protocol.ToolTarget, bundle []byte) protocol.ToolResult {
	r := protocol.ToolResult{Item: t.Revision.Item, Revision: t.Revision.Revision, Index: t.Index}
	fail := func(state, reason string) protocol.ToolResult { r.State = state; r.Reason = reason; return r }
	if t.Revision.Assignment.Scope != "projects" || t.Target.Action != "copy" || !filepath.IsAbs(t.Target.Checkout) {
		return fail("pending", "operation is not supported yet")
	}
	selected := false
	for _, p := range t.Revision.Assignment.Projects {
		if p == t.Target.Project {
			selected = true
		}
	}
	if !selected {
		return fail("conflict", "project is not assigned")
	}
	if t.Target.Conflict != "" && t.Target.Conflict != "destination already exists and is not owned" && t.Target.Conflict != "machine offline; destination cannot be inspected" {
		return fail("conflict", "plan target requires inspection")
	}
	if t.Name == "" || t.Name == "." || t.Name == ".." || filepath.Base(t.Name) != t.Name || strings.ContainsAny(t.Name, "/\\\x00\r\n") {
		return fail("conflict", "invalid skill name")
	}
	root := ""
	switch t.Target.Harness {
	case "claude":
		root = ".claude/skills"
	case "codex":
		root = ".agents/skills"
	case "opencode":
		root = ".opencode/skills"
	case "pi":
		root = ".pi/skills"
	default:
		return fail("manual", "unsupported harness")
	}
	if t.Target.Destination != "" {
		if filepath.Base(t.Target.Destination) != t.Name {
			return fail("conflict", "planned destination differs")
		}
		planned, err := filepath.Rel(t.Target.Checkout, filepath.Dir(t.Target.Destination))
		if err != nil {
			return fail("conflict", "planned destination differs")
		}
		allowed := planned == root || (t.Target.Harness == "opencode" && (planned == ".claude/skills" || planned == ".agents/skills")) || (t.Target.Harness == "pi" && planned == ".agents/skills")
		if !allowed {
			return fail("conflict", "planned destination differs")
		}
		root = planned
	}

	// Reject an invalid or mismatched archive before touching Git metadata.
	stage, err := os.MkdirTemp(d.cfg.StateDir, ".project-verify-")
	if err != nil {
		return fail("failed", "cannot verify skill bundle")
	}
	defer os.RemoveAll(stage)
	if err = inventory.UnpackSkill(bundle, stage); err != nil {
		return fail("failed", "invalid skill bundle")
	}
	hash, err := inventory.SkillHash(stage)
	if err != nil || hash != t.Revision.Version {
		return fail("failed", "skill hash mismatch")
	}
	checkout := filepath.Clean(t.Target.Checkout)
	top, err := projectGit(checkout, "rev-parse", "--show-toplevel")
	if err != nil {
		return fail("pending", "owner must choose an ignore mechanism")
	}
	topPath := strings.TrimSpace(string(top))
	if filepath.Clean(topPath) != checkout {
		return fail("conflict", "checkout is not the Git worktree root")
	}
	gitDirBytes, err := projectGit(checkout, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return fail("failed", "cannot inspect Git worktree")
	}
	gitDir := strings.TrimSpace(string(gitDirBytes))
	indexLock := filepath.Join(gitDir, "index.lock")
	f, err := os.OpenFile(indexLock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fail("pending", "Git index is locked")
	}
	f.Close()
	defer os.Remove(indexLock)
	rel := filepath.Join(root, t.Name)
	tracked, err := projectGit(checkout, "ls-files", "-z", "--", rel)
	if err != nil {
		return fail("failed", "cannot inspect tracked paths")
	}
	if len(tracked) > 0 {
		return fail("conflict", "destination contains tracked files")
	}

	// Check every existing path component without following symlinks before
	// changing the shared exclude file.
	cur := checkout
	for _, part := range strings.Split(rel, string(os.PathSeparator)) {
		cur = filepath.Join(cur, part)
		info, e := os.Lstat(cur)
		if os.IsNotExist(e) {
			break
		}
		if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fail("conflict", "destination contains a symlink or non-directory")
		}
		if cur == filepath.Join(checkout, rel) {
			h, e := inventory.SkillHash(cur)
			if e != nil || h != t.Revision.Version {
				return fail("conflict", "destination differs")
			}
		}
	}
	excludeBytes, err := projectGit(checkout, "rev-parse", "--git-path", "info/exclude")
	if err != nil {
		return fail("failed", "cannot locate Git exclude")
	}
	exclude := strings.TrimSpace(string(excludeBytes))
	if !filepath.IsAbs(exclude) {
		exclude = filepath.Join(checkout, exclude)
	}
	exclude = filepath.Clean(exclude)
	unlock, err := lockSkillExclude(exclude)
	if err != nil {
		return fail("pending", "cannot lock Git exclude")
	}
	defer unlock()
	pattern := projectExcludePattern(rel)
	before, err := readSkillExclude(exclude)
	if err != nil {
		return fail("pending", "cannot read Git exclude")
	}
	present := false
	for _, line := range bytes.Split(before, []byte("\n")) {
		if string(line) == pattern {
			present = true
			break
		}
	}
	if !present {
		addition := []byte(pattern + "\n")
		if len(before) > 0 && before[len(before)-1] != '\n' {
			addition = append([]byte("\n"), addition...)
		}
		if err = appendSkillExclude(exclude, addition); err != nil {
			return fail("pending", "cannot update Git exclude")
		}
	}
	if _, err = projectGit(checkout, "check-ignore", "--no-index", "-q", "--", filepath.Join(rel, "SKILL.md")); err != nil {
		return fail("pending", "managed path is not ignored by Git")
	}
	result := d.copySkillAt(t, bundle, checkout, root)
	if result.State != "applied" {
		return result
	}
	if err = d.recordProjectExclude(filepath.Join(checkout, rel), exclude, pattern, !present); err != nil {
		return fail("failed", "cannot record Git exclude")
	}
	return result
}

func (d *Daemon) recordProjectExclude(path, exclude, line string, owned bool) error {
	manifest := filepath.Join(d.cfg.StateDir, "skill-manifest.json")
	b, err := os.ReadFile(manifest)
	if err != nil {
		return err
	}
	var entries []skillManifestEntry
	if err = json.Unmarshal(b, &entries); err != nil {
		return err
	}
	for _, e := range entries {
		if e.Exclude == exclude && e.ExcludeLine == line && e.ExcludeOwned {
			owned = true
		}
	}
	found := false
	for i := range entries {
		if entries[i].Path == path {
			entries[i].Exclude = exclude
			entries[i].ExcludeLine = line
			entries[i].ExcludeOwned = owned
			found = true
		}
	}
	if !found {
		return errors.New("manifest entry unavailable")
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
	return os.Rename(name, manifest)
}

// projectTargets validates the exact planned path and detects compatibility
// sources that would make a skill visible more than once.
func projectTargets(targets []protocol.ToolTarget) map[string]string {
	type group struct {
		item, checkout, name string
		harnesses            []string
		targets              []protocol.ToolTarget
	}
	groups := map[string]*group{}
	for _, t := range targets {
		if t.Revision.Assignment.Scope != "projects" || t.Target.Action != "copy" || t.Target.Checkout == "" {
			continue
		}
		key := t.Revision.Item + "\x00" + t.Target.Checkout
		g := groups[key]
		if g == nil {
			g = &group{item: t.Revision.Item, checkout: t.Target.Checkout, name: t.Name}
			groups[key] = g
		}
		g.harnesses = append(g.harnesses, t.Target.Harness)
		g.targets = append(g.targets, t)
	}
	conflicts := map[string]string{}
	for key, g := range groups {
		for _, t := range g.targets {
			root, conflict := inventory.ProjectSkillRoot(t.Target.Harness, g.harnesses)
			if conflict {
				conflicts[key] = "installed harnesses have overlapping skill roots"
				break
			}
			dest := filepath.Join(g.checkout, root, g.name)
			if t.Target.Destination != "" && filepath.Clean(t.Target.Destination) != dest {
				conflicts[key] = "planned destination differs"
				break
			}
			var visible []string
			switch t.Target.Harness {
			case "claude":
				visible = []string{".claude/skills"}
			case "codex":
				visible = []string{".agents/skills"}
			case "opencode":
				visible = []string{".opencode/skills", ".claude/skills", ".agents/skills"}
			case "pi":
				visible = []string{".pi/skills", ".agents/skills"}
			}
			for _, other := range visible {
				if other == root {
					continue
				}
				_, err := os.Lstat(filepath.Join(g.checkout, other, g.name))
				if err == nil || !os.IsNotExist(err) {
					conflicts[key] = "another skill source may shadow the planned path"
					break
				}
			}
			if conflicts[key] != "" {
				break
			}
		}
	}
	return conflicts
}

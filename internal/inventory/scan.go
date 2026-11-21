package inventory

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// PartialScanReason marks a bounded scan with individual source failures.
const PartialScanReason = "one or more sources could not be scanned"

// SkillRoot is one folder of skill folders. Base is the folder the path must
// stay inside without a link. Project names the checkout the folder belongs
// to; it is empty for a user folder.
type SkillRoot struct {
	Harness, Base, Path string
	Project             string
}

// projectSkillDirs are the folders, under a project checkout, that each harness
// reads project skills from.
var projectSkillDirs = [][2]string{{"antigravity", ".agent/skills"}, {"claude", ".claude/skills"}, {"codex", ".agents/skills"}, {"opencode", ".opencode/skills"}, {"opencode", ".claude/skills"}, {"opencode", ".agents/skills"}, {"pi", ".pi/skills"}, {"pi", ".agents/skills"}}

// ProjectSkillRoots lists the skill folders of one project checkout, one for
// each harness that reads project skills from it.
func ProjectSkillRoots(name, base string) []SkillRoot {
	base = filepath.Clean(base)
	out := make([]SkillRoot, 0, len(projectSkillDirs))
	for _, x := range projectSkillDirs {
		out = append(out, SkillRoot{Harness: x[0], Base: base, Path: filepath.Join(base, x[1]), Project: name})
	}
	return out
}

// codexHome is the Codex home the scan reads: the one given, else ~/.codex.
func codexHome(home, codex string) string {
	if codex == "" {
		return filepath.Join(home, ".codex")
	}
	return codex
}

// UserSkillRoots lists the user-level skill folders the scan reads, in the
// order it reads them. Codex's folder sits under the Codex home; the others sit
// under the home folder.
func UserSkillRoots(home, codex string) []SkillRoot {
	home = filepath.Clean(home)
	out := make([]SkillRoot, 0, 9)
	for _, x := range [][2]string{{"antigravity", ".gemini/config/skills"}, {"claude", ".claude/skills"}, {"codex", ".agents/skills"}, {"opencode", ".config/opencode/skills"}, {"opencode", ".claude/skills"}, {"opencode", ".agents/skills"}, {"pi", ".pi/agent/skills"}, {"pi", ".agents/skills"}} {
		out = append(out, SkillRoot{Harness: x[0], Base: home, Path: filepath.Join(home, x[1])})
	}
	return append(out, SkillRoot{Harness: "codex", Base: codexHome(home, codex), Path: filepath.Join(codexHome(home, codex), "skills")})
}

// ScanSkillRoot lists the skill folders in one root without executing their
// contents. The archive intake uses it for user, project, plugin and agent-net
// skill folders.
func ScanSkillRoot(home string, r SkillRoot) protocol.InventoryReport {
	now := time.Now().UTC()
	report := protocol.InventoryReport{ScannedAt: now, State: protocol.InventoryOK, Items: []protocol.InventoryItem{}}
	totalFields := 0
	add := func(h, k, scope, project, root, path, name, hash, state, reason string) {
		total := len(h) + len(k) + len(scope) + len(project) + len(root) + len(path) + len(name) + len(hash) + len(reason)
		if len(name) > 4096 || len(path) > 4096 || len(root) > 4096 || len(project) > 4096 || len(report.Items) >= 10000 || totalFields+total > 512<<10 {
			report.State = protocol.InventoryError
			report.Reason = "inventory exceeds report limit"
			return
		}
		totalFields += total
		if state == protocol.InventoryError && (report.Reason == "" || report.Reason == PartialScanReason) {
			report.State = protocol.InventoryError
			report.Reason = PartialScanReason
		}
		report.Items = append(report.Items, protocol.InventoryItem{Harness: h, Kind: k, Scope: scope, Project: project, ProjectRoot: root, Location: path, Name: name, Hash: hash, ScannedAt: now, State: state, Reason: reason})
	}
	// Never traverse links in a source path or skill root.
	safe := safePath
	sourceCount := 0
	var skills func(h, scope, project, root, base, path string)
	skills = func(h, scope, project, root, base, path string) {
		sourceCount++
		relDepth, depthErr := filepath.Rel(base, path)
		if depthErr != nil || sourceCount > maxFiles || strings.Count(relDepth, string(os.PathSeparator)) > 32 {
			add(h, protocol.InventorySkill, scope, project, root, path, "", "", protocol.InventoryError, "skill source exceeds traversal limit")
			return
		}
		if !safe(base, path) {
			add(h, protocol.InventorySkill, scope, project, root, path, "", "", protocol.InventoryError, "source contains a symlink")
			return
		}
		baseRoot, err := os.OpenRoot(base)
		if errors.Is(err, os.ErrNotExist) {
			return
		}
		if err != nil {
			add(h, protocol.InventorySkill, scope, project, root, path, "", "", protocol.InventoryError, "cannot open source root")
			return
		}
		defer baseRoot.Close()
		rel, err := filepath.Rel(base, path)
		if err != nil {
			return
		}
		sourceRoot, err := baseRoot.OpenRoot(rel)
		if errors.Is(err, os.ErrNotExist) {
			return
		}
		if err != nil {
			add(h, protocol.InventorySkill, scope, project, root, path, "", "", protocol.InventoryError, "cannot read source")
			return
		}
		defer sourceRoot.Close()
		source, err := sourceRoot.Open(".")
		if err != nil {
			add(h, protocol.InventorySkill, scope, project, root, path, "", "", protocol.InventoryError, "cannot read source")
			return
		}
		entries, err := source.ReadDir(maxFiles + 1)
		source.Close()
		if err != nil && !errors.Is(err, io.EOF) {
			add(h, protocol.InventorySkill, scope, project, root, path, "", "", protocol.InventoryError, "cannot read source")
			return
		}
		if len(entries) > maxFiles {
			add(h, protocol.InventorySkill, scope, project, root, path, "", "", protocol.InventoryError, "source exceeds entry limit")
			return
		}
		for _, e := range entries {
			p := filepath.Join(path, e.Name())
			actual := p
			var alias string
			var targetInfo os.FileInfo
			if e.Type()&os.ModeSymlink != 0 {
				target, resolveErr := filepath.EvalSymlinks(p)
				if resolveErr != nil || !safePath(home, target) {
					link, _ := os.Readlink(p)
					add(h, protocol.InventorySkill, scope, project, root, p, e.Name(), "", protocol.InventoryError, "skill root is a symlink: "+link)
					continue
				}
				info, statErr := os.Lstat(target)
				if statErr != nil || !info.IsDir() {
					continue
				}
				actual, alias, targetInfo = target, p, info
			} else if !e.IsDir() {
				continue
			}
			var skillRoot *os.Root
			if alias != "" {
				homeRoot, openErr := os.OpenRoot(home)
				if openErr != nil {
					err = openErr
				} else {
					relTarget, relErr := filepath.Rel(home, actual)
					if relErr != nil {
						err = relErr
					} else {
						skillRoot, err = homeRoot.OpenRoot(relTarget)
					}
					homeRoot.Close()
					if err == nil {
						opened, statErr := skillRoot.Stat(".")
						again, resolveErr := filepath.EvalSymlinks(p)
						if statErr != nil || resolveErr != nil || again != actual || !os.SameFile(targetInfo, opened) {
							skillRoot.Close()
							skillRoot = nil
							err = errors.New("skill target changed during scan")
						}
					}
				}
			} else {
				skillRoot, err = sourceRoot.OpenRoot(e.Name())
			}
			if err != nil {
				add(h, protocol.InventorySkill, scope, project, root, p, e.Name(), "", protocol.InventoryError, "cannot open skill safely")
				continue
			}
			info, err := skillRoot.Lstat("SKILL.md")
			if errors.Is(err, os.ErrNotExist) {
				skillRoot.Close()
				if h == "pi" {
					skills(h, scope, project, root, base, p)
				}
				continue
			}
			if err == nil && info.Mode()&os.ModeSymlink != 0 {
				info, err = skillRoot.Stat("SKILL.md")
			}
			if err != nil || !info.Mode().IsRegular() {
				skillRoot.Close()
				add(h, protocol.InventorySkill, scope, project, root, p, e.Name(), "", protocol.InventoryError, "invalid skill file")
				continue
			}
			hash, err := skillHashRoot(skillRoot)
			skillRoot.Close()
			if err != nil {
				add(h, protocol.InventorySkill, scope, project, root, actual, e.Name(), "", protocol.InventoryError, err.Error())
				continue
			}
			add(h, protocol.InventorySkill, scope, project, root, actual, e.Name(), hash, protocol.InventoryOK, "")
		}
	}
	if r.Project != "" {
		skills(r.Harness, protocol.InventoryProject, r.Project, r.Base, r.Base, r.Path)
	} else {
		skills(r.Harness, protocol.InventoryUser, "", "", r.Base, r.Path)
	}
	return report
}

// safePath reports whether no part of path below base is a symlink.
func safePath(base, path string) bool {
	rel, err := filepath.Rel(base, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return false
	}
	cur := base
	for _, part := range strings.Split(rel, string(os.PathSeparator)) {
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		if err != nil {
			return errors.Is(err, os.ErrNotExist)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return false
		}
	}
	return true
}

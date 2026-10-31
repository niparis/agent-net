package daemon

import (
	"path/filepath"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// projectSkillRoots are the folders a harness reads project skills from.
var projectSkillRoots = []string{".claude/skills", ".agents/skills", ".opencode/skills", ".pi/skills"}

// trackedUnder lists the folders (slash paths, relative to root) that hold at
// least one file git tracks under a skill root. It runs one git ls-files and
// uses no network. A folder that is not a repository gives an empty set.
func trackedUnder(root string) map[string]bool {
	out := map[string]bool{}
	args := append([]string{"ls-files", "-z", "--"}, projectSkillRoots...)
	raw, err := projectGit(root, args...)
	if err != nil {
		return out
	}
	for _, p := range strings.Split(string(raw), "\x00") {
		for dir := filepath.ToSlash(filepath.Dir(p)); p != "" && dir != "." && dir != "/" && !out[dir]; dir = filepath.ToSlash(filepath.Dir(dir)) {
			out[dir] = true
		}
	}
	return out
}

// markTrackedSkills sets Tracked on every project skill that git tracks.
func markTrackedSkills(report *protocol.InventoryReport) {
	markTrackedSkillsWith(report, trackedUnder)
}

// markTrackedSkillsWith is markTrackedSkills with the listing replaced. It asks
// for each project root once per scan.
func markTrackedSkillsWith(report *protocol.InventoryReport, list func(root string) map[string]bool) {
	cache := map[string]map[string]bool{}
	for i := range report.Items {
		it := &report.Items[i]
		if it.Kind != protocol.InventorySkill || it.Scope != protocol.InventoryProject || it.ProjectRoot == "" {
			continue
		}
		root := filepath.Clean(it.ProjectRoot)
		set, ok := cache[root]
		if !ok {
			set = list(root)
			cache[root] = set
		}
		rel, err := filepath.Rel(root, it.Location)
		if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
			continue
		}
		it.Tracked = set[filepath.ToSlash(rel)]
	}
}

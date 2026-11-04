package daemon

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/repoinfo"
)

const (
	maxLayoutScanDepth = 4
	maxLayoutRepos     = 500
	layoutScanTimeout  = 15 * time.Second
	layoutCacheTTL     = 10 * time.Minute
)

func (d *Daemon) homeDir() string {
	if d.cfg.HomeDir != "" {
		return d.cfg.HomeDir
	}
	if h := os.Getenv("AGENT_NET_HOME_DIR"); h != "" {
		return h
	}
	return home()
}

// InvalidateLayoutCache clears the cached project layout so the next call
// rescans the disk.
func (d *Daemon) InvalidateLayoutCache() {
	d.layoutMu.Lock()
	defer d.layoutMu.Unlock()
	d.layoutCache = nil
}

func (d *Daemon) onProjectsLayout(ctx context.Context, l *link, id string) {
	layout, err := d.ProjectsLayoutLocal(ctx)
	if res, encErr := protocol.EncodeResult(id, layout, err); encErr == nil {
		l.write(ctx, res)
	}
}

// ProjectsLayoutLocal returns this machine's layout, using a 10-minute cache.
func (d *Daemon) ProjectsLayoutLocal(ctx context.Context) (protocol.MachineLayout, error) {
	d.layoutMu.Lock()
	if d.layoutCache != nil && time.Since(d.layoutTime) < layoutCacheTTL {
		cached := *d.layoutCache
		d.layoutMu.Unlock()
		return cached, nil
	}
	d.layoutMu.Unlock()

	layout, err := d.scanProjectsLayout(ctx, d.homeDir())
	if err != nil {
		return protocol.MachineLayout{}, err
	}

	d.layoutMu.Lock()
	d.layoutCache = &layout
	d.layoutTime = time.Now()
	d.layoutMu.Unlock()

	return layout, nil
}

type discoveredRepo struct {
	path       string
	name       string
	remote     string
	lastCommit int64
}

func (d *Daemon) scanProjectsLayout(ctx context.Context, homeDir string) (protocol.MachineLayout, error) {
	cleanHome := filepath.Clean(homeDir)
	host := d.hostName()

	ctx, cancel := context.WithTimeout(ctx, layoutScanTimeout)
	defer cancel()

	var (
		repos     []discoveredRepo
		truncated bool
	)

	noiseFolders := map[string]bool{
		"node_modules":       true,
		"vendor":             true,
		"cache":              true,
		"caches":             true,
		"tmp":                true,
		"temp":               true,
		"worktrees":          true,
		"isolated-worktrees": true,
	}

	var walk func(dir string, level int)
	walk = func(dir string, level int) {
		if ctx.Err() != nil {
			truncated = true
			return
		}
		if len(repos) >= maxLayoutRepos {
			truncated = true
			return
		}

		// Check if this directory is a git repo or git worktree
		if dir != cleanHome {
			gitPath := filepath.Join(dir, ".git")
			if st, err := os.Lstat(gitPath); err == nil {
				if !st.IsDir() {
					// Git worktrees have a .git file, not a directory -> drop as noise.
					return
				}
				// Valid git repository with a .git directory.
				remote := readRepoRemote(ctx, dir)
				lastCommit := readRepoCommit(ctx, dir)
				repos = append(repos, discoveredRepo{
					path:       dir,
					name:       filepath.Base(dir),
					remote:     remote,
					lastCommit: lastCommit,
				})
				// Do not recurse into repository once found.
				return
			}
		}

		if level >= maxLayoutScanDepth {
			return
		}

		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

		for _, ent := range entries {
			if !ent.IsDir() {
				continue
			}
			name := ent.Name()
			childPath := filepath.Join(dir, name)
			relToHome, err := filepath.Rel(cleanHome, childPath)
			if err != nil {
				continue
			}
			relSlash := filepath.ToSlash(relToHome)

			if strings.Contains(relSlash, ".local/state/agent-net/daemon/worktrees") ||
				strings.Contains(relSlash, ".claude/worktrees") ||
				strings.Contains(relSlash, "code/isolated-worktrees") {
				continue
			}

			if strings.HasPrefix(name, ".") {
				if name != ".local" && name != ".claude" {
					continue
				}
			}
			if strings.HasPrefix(relSlash, ".local") && !strings.HasPrefix(relSlash, ".local/state") {
				continue
			}
			if strings.HasPrefix(relSlash, ".claude") && relSlash != ".claude" {
				continue
			}

			if noiseFolders[strings.ToLower(name)] {
				// Skip dependency, cache, temp and worktree folders
				continue
			}
			walk(childPath, level+1)
		}
	}

	walk(cleanHome, 0)

	roots, loose := computeLayoutRoots(cleanHome, repos)
	if roots == nil {
		roots = []protocol.LayoutRoot{}
	}
	if loose == nil {
		loose = []protocol.LayoutProject{}
	}

	return protocol.MachineLayout{
		Machine:   host,
		Home:      cleanHome,
		Roots:     roots,
		Loose:     loose,
		ScannedAt: time.Now().Unix(),
		Truncated: truncated,
	}, nil
}

func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil && cmd.Process.Pid > 0 {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
}

func readRepoRemote(ctx context.Context, dir string) string {
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, "git", "-C", dir, "config", "--get", "remote.origin.url")
	setProcessGroup(cmd)
	cmd.WaitDelay = 200 * time.Millisecond
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return repoinfo.StripCredentials(strings.TrimSpace(string(out)))
}

func readRepoCommit(ctx context.Context, dir string) int64 {
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, "git", "-C", dir, "log", "-1", "--format=%ct")
	setProcessGroup(cmd)
	cmd.WaitDelay = 200 * time.Millisecond
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	ts, _ := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	return ts
}

func formatPathTilde(path, home string) string {
	cleanPath := filepath.Clean(path)
	cleanHome := filepath.Clean(home)
	if cleanPath == cleanHome {
		return "~"
	}
	if strings.HasPrefix(cleanPath, cleanHome+string(filepath.Separator)) {
		return "~" + cleanPath[len(cleanHome):]
	}
	return cleanPath
}

func extractRemoteOwner(remote string) string {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return ""
	}
	var pathPart, host string
	if strings.Contains(remote, "://") {
		u, err := url.Parse(remote)
		if err != nil {
			return ""
		}
		if strings.EqualFold(u.Scheme, "file") {
			return ""
		}
		host = strings.ToLower(u.Host)
		if i := strings.IndexByte(host, ':'); i >= 0 {
			host = host[:i]
		}
		if host == "" {
			return ""
		}
		pathPart = strings.TrimPrefix(u.Path, "/")
	} else if i := strings.IndexByte(remote, ':'); i >= 0 && !filepath.IsAbs(remote) {
		prefix := remote[:i]
		if at := strings.IndexByte(prefix, '@'); at >= 0 {
			host = prefix[at+1:]
		} else {
			host = prefix
		}
		host = strings.ToLower(host)
		pathPart = strings.TrimPrefix(remote[i+1:], "/")
	} else {
		return ""
	}
	pathPart = strings.TrimSuffix(pathPart, ".git")
	pathPart = strings.Trim(pathPart, "/")
	parts := strings.Split(pathPart, "/")
	if len(parts) < 2 {
		return ""
	}
	owner := parts[0]
	if host == "github.com" || strings.HasSuffix(host, ".github.com") {
		return owner
	}
	return host + "/" + owner
}

func computeGroupOwners(projects []discoveredRepo) []string {
	counts := map[string]int{}
	for _, p := range projects {
		owner := extractRemoteOwner(p.remote)
		if owner != "" {
			counts[owner]++
		}
	}
	if len(counts) == 0 {
		return []string{}
	}
	type item struct {
		owner string
		count int
	}
	items := make([]item, 0, len(counts))
	for o, c := range counts {
		items = append(items, item{owner: o, count: c})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].count != items[j].count {
			return items[i].count > items[j].count
		}
		return items[i].owner < items[j].owner
	})
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = fmt.Sprintf("%s (%d)", it.owner, it.count)
	}
	return out
}

func computeLayoutRoots(homeDir string, repos []discoveredRepo) ([]protocol.LayoutRoot, []protocol.LayoutProject) {
	if len(repos) == 0 {
		return []protocol.LayoutRoot{}, []protocol.LayoutProject{}
	}

	cleanHome := filepath.Clean(homeDir)

	type repoWithSegments struct {
		repo     discoveredRepo
		segments []string
	}

	list := make([]repoWithSegments, 0, len(repos))
	for _, r := range repos {
		rel, err := filepath.Rel(cleanHome, r.path)
		if err != nil || strings.HasPrefix(rel, "..") || rel == "." {
			continue
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		list = append(list, repoWithSegments{repo: r, segments: parts})
	}

	// Find candidate root folders (folders between home and repos, not home itself).
	type candidateRoot struct {
		path     string
		segments []string
	}
	candidateMap := map[string][]string{}
	for _, it := range list {
		// A repo at path a/b/c can have ancestor candidate folders "a" and "a/b".
		for i := 1; i < len(it.segments); i++ {
			cParts := it.segments[:i]
			cRel := strings.Join(cParts, string(filepath.Separator))
			cAbs := filepath.Join(cleanHome, cRel)
			candidateMap[cAbs] = cParts
		}
	}

	candidates := make([]candidateRoot, 0, len(candidateMap))
	for p, segs := range candidateMap {
		candidates = append(candidates, candidateRoot{path: p, segments: segs})
	}

	// Sort candidates by segment length ascending (shallower first), then path.
	sort.Slice(candidates, func(i, j int) bool {
		if len(candidates[i].segments) != len(candidates[j].segments) {
			return len(candidates[i].segments) < len(candidates[j].segments)
		}
		return candidates[i].path < candidates[j].path
	})

	claimed := map[string]bool{}
	roots := []protocol.LayoutRoot{}

	for _, cand := range candidates {
		// Check repos under this candidate at depth 1 or depth 2
		var held []repoWithSegments
		hasDepth2 := false
		for _, it := range list {
			if claimed[it.repo.path] {
				continue
			}
			if len(it.segments) <= len(cand.segments) {
				continue
			}
			// Check prefix match
			match := true
			for i := 0; i < len(cand.segments); i++ {
				if it.segments[i] != cand.segments[i] {
					match = false
					break
				}
			}
			if !match {
				continue
			}
			depth := len(it.segments) - len(cand.segments)
			if depth == 1 || depth == 2 {
				held = append(held, it)
				if depth == 2 {
					hasDepth2 = true
				}
			}
		}

		if len(held) < 2 {
			continue
		}

		// Valid root found!
		for _, h := range held {
			claimed[h.repo.path] = true
		}

		kind := "flat"
		if hasDepth2 {
			kind = "grouped"
		}

		root := protocol.LayoutRoot{
			Path:     formatPathTilde(cand.path, cleanHome),
			Kind:     kind,
			Groups:   []protocol.LayoutGroup{},
			Projects: []protocol.LayoutProject{},
		}

		if kind == "grouped" {
			// Group repos at depth 2 under their group folders
			groupMap := map[string][]discoveredRepo{}
			var loose []discoveredRepo
			for _, h := range held {
				depth := len(h.segments) - len(cand.segments)
				if depth == 2 {
					groupFolder := filepath.Join(cand.path, h.segments[len(cand.segments)])
					groupMap[groupFolder] = append(groupMap[groupFolder], h.repo)
				} else {
					loose = append(loose, h.repo)
				}
			}

			var groupDirs []string
			for dir := range groupMap {
				groupDirs = append(groupDirs, dir)
			}
			sort.Strings(groupDirs)

			for _, gDir := range groupDirs {
				gRepos := groupMap[gDir]
				var newest int64
				for _, r := range gRepos {
					if r.lastCommit > newest {
						newest = r.lastCommit
					}
				}
				root.Groups = append(root.Groups, protocol.LayoutGroup{
					Name:       filepath.Base(gDir),
					Path:       formatPathTilde(gDir, cleanHome),
					Projects:   len(gRepos),
					Owners:     computeGroupOwners(gRepos),
					LastCommit: newest,
				})
			}

			// Add loose projects
			sort.Slice(loose, func(i, j int) bool { return loose[i].path < loose[j].path })
			for _, l := range loose {
				root.Projects = append(root.Projects, protocol.LayoutProject{
					Name:       l.name,
					Path:       formatPathTilde(l.path, cleanHome),
					Remote:     l.remote,
					LastCommit: l.lastCommit,
				})
			}
		} else {
			// Flat root: repos at depth 1
			sort.Slice(held, func(i, j int) bool { return held[i].repo.path < held[j].repo.path })
			for _, h := range held {
				root.Projects = append(root.Projects, protocol.LayoutProject{
					Name:       h.repo.name,
					Path:       formatPathTilde(h.repo.path, cleanHome),
					Remote:     h.repo.remote,
					LastCommit: h.repo.lastCommit,
				})
			}
		}

		roots = append(roots, root)
	}

	// Home folder itself acts as a flat root when it directly holds 2 or more repos.
	var directHome []repoWithSegments
	for _, it := range list {
		if !claimed[it.repo.path] && len(it.segments) == 1 {
			directHome = append(directHome, it)
		}
	}
	if len(directHome) >= 2 {
		sort.Slice(directHome, func(i, j int) bool { return directHome[i].repo.path < directHome[j].repo.path })
		homeRoot := protocol.LayoutRoot{
			Path:     "~",
			Kind:     "flat",
			Groups:   []protocol.LayoutGroup{},
			Projects: make([]protocol.LayoutProject, 0, len(directHome)),
		}
		for _, h := range directHome {
			claimed[h.repo.path] = true
			homeRoot.Projects = append(homeRoot.Projects, protocol.LayoutProject{
				Name:       h.repo.name,
				Path:       formatPathTilde(h.repo.path, cleanHome),
				Remote:     h.repo.remote,
				LastCommit: h.repo.lastCommit,
			})
		}
		roots = append(roots, homeRoot)
	}

	// Repos not claimed by any root are listed in loose.
	loose := []protocol.LayoutProject{}
	for _, it := range list {
		if !claimed[it.repo.path] {
			loose = append(loose, protocol.LayoutProject{
				Name:       it.repo.name,
				Path:       formatPathTilde(it.repo.path, cleanHome),
				Remote:     it.repo.remote,
				LastCommit: it.repo.lastCommit,
			})
		}
	}
	sort.Slice(loose, func(i, j int) bool {
		return loose[i].Path < loose[j].Path
	})

	sort.Slice(roots, func(i, j int) bool {
		return roots[i].Path < roots[j].Path
	})

	return roots, loose
}

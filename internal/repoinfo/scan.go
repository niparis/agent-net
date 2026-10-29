package repoinfo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// Inspect checks one folder for project registration. The folder must exist
// and be inside a git working tree. The answer is for the main tree of that
// working tree: a linked worktree gives its main tree, and a submodule is its
// own project. It reads git every time and does not use the cache.
func (r *Resolver) Inspect(path string) (protocol.ProjectEntry, error) {
	var e protocol.ProjectEntry
	st, err := os.Stat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return e, errors.New("the folder does not exist")
	case err != nil:
		return e, err
	case !st.IsDir():
		return e, errors.New("not a folder")
	}
	// A symbolic link would show up in the answer of git (it trusts $PWD):
	// register the real folder.
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	ctx, cancel := context.WithTimeout(context.Background(), r.Timeout)
	defer cancel()
	top, err := r.git(ctx, path, "rev-parse", "--show-toplevel")
	if err != nil || top == "" {
		return e, errors.New("not a git working tree")
	}
	root := r.mainTree(ctx, path, filepath.Clean(top))
	e.Path, e.Name = root, filepath.Base(root)
	if remote, err := r.git(ctx, root, "config", "--get", "remote.origin.url"); err == nil {
		e.Remote = StripCredentials(remote)
	}
	if ref, err := r.git(ctx, root, "symbolic-ref", "--short", "-q", "refs/remotes/origin/HEAD"); err == nil {
		e.DefaultBranch = strings.TrimPrefix(ref, "origin/")
	}
	return e, nil
}

// Folders a scan reports and does not enter. Hidden folders are skipped without
// a report, which covers the git folder, .claude and .worktrees. The rule for
// .claude/worktrees stays explicit for a scan that starts inside .claude.
var skipDirs = map[string]string{
	"node_modules": "dependency folder",
	"vendor":       "dependency folder",
}

// ScanOptions limits a scan.
type ScanOptions struct {
	// Depth is how many levels below the start folder are looked at. The start
	// folder itself is level 0. Zero means protocol.DefaultScanDepth.
	Depth int
	// Max caps the repositories that are inspected. Zero means
	// protocol.ProjectMaxPerCall.
	Max int
}

// ScanResult is what a scan found.
type ScanResult struct {
	Projects []protocol.ProjectEntry
	Skipped  []protocol.ProjectSkip
	// Overflow counts repositories found after the cap was reached.
	Overflow int
}

// Scan walks dir for git repositories. It does not enter a repository once
// found (a submodule or a nested repository inside it is not looked at), does
// not follow symbolic links, and skips hidden folders, node_modules, vendor and
// worktree folders. A linked worktree is skipped, with its main tree named,
// because the main tree is the project.
func (r *Resolver) Scan(ctx context.Context, dir string, o ScanOptions) (ScanResult, error) {
	var res ScanResult
	if o.Depth <= 0 {
		o.Depth = protocol.DefaultScanDepth
	}
	if o.Depth > protocol.MaxScanDepth {
		return res, fmt.Errorf("depth must be at most %d", protocol.MaxScanDepth)
	}
	if o.Max <= 0 {
		o.Max = protocol.ProjectMaxPerCall
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return res, errors.New("the folder does not exist")
	}
	if st, err := os.Stat(real); err != nil || !st.IsDir() {
		return res, errors.New("not a folder")
	}
	seen := map[string]bool{}
	found := 0
	var walk func(dir string, level int)
	walk = func(dir string, level int) {
		if ctx.Err() != nil {
			return
		}
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			found++
			if found > o.Max {
				res.Overflow++
				return
			}
			e, err := r.Inspect(dir)
			switch {
			case err != nil:
				res.Skipped = append(res.Skipped, protocol.ProjectSkip{Path: dir, Reason: err.Error()})
			case e.Path != dir:
				res.Skipped = append(res.Skipped, protocol.ProjectSkip{Path: dir, Reason: "linked worktree of " + e.Path})
			case !seen[e.Path]:
				seen[e.Path] = true
				res.Projects = append(res.Projects, e)
			}
			return
		}
		if level >= o.Depth {
			return
		}
		ents, err := os.ReadDir(dir)
		if err != nil {
			res.Skipped = append(res.Skipped, protocol.ProjectSkip{Path: dir, Reason: "cannot read the folder"})
			return
		}
		sort.Slice(ents, func(i, j int) bool { return ents[i].Name() < ents[j].Name() })
		for _, ent := range ents {
			if !ent.IsDir() { // symbolic links are not folders here: they are not followed
				continue
			}
			name, child := ent.Name(), filepath.Join(dir, ent.Name())
			if why, ok := skipDirs[name]; ok {
				res.Skipped = append(res.Skipped, protocol.ProjectSkip{Path: child, Reason: why})
				continue
			}
			if name == "worktrees" && filepath.Base(dir) == ".claude" {
				res.Skipped = append(res.Skipped, protocol.ProjectSkip{Path: child, Reason: "worktree folder"})
				continue
			}
			if strings.HasPrefix(name, ".") {
				continue
			}
			walk(child, level+1)
		}
	}
	walk(real, 0)
	sort.Slice(res.Projects, func(i, j int) bool { return res.Projects[i].Path < res.Projects[j].Path })
	sort.Slice(res.Skipped, func(i, j int) bool { return res.Skipped[i].Path < res.Skipped[j].Path })
	return res, nil
}

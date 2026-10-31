// Package repoinfo works out which project a folder belongs to, using the
// git command line. It never fails: a folder that is not in a repository, a
// missing git binary or a slow repository gives a smaller answer.
package repoinfo

import (
	"bytes"
	"context"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Defaults for Resolver.
const (
	DefaultTimeout   = 3 * time.Second // for all git commands of one lookup
	DefaultStaticTTL = 5 * time.Minute // how long root, project and remote are kept
	DefaultBranchTTL = 5 * time.Second // how long a branch is kept by Lookup
)

// Resolver finds repository info per folder and caches it.
type Resolver struct {
	// Timeout bounds all git commands of one lookup.
	Timeout   time.Duration
	StaticTTL time.Duration
	BranchTTL time.Duration

	now func() time.Time
	// git runs one git command in dir and returns its trimmed output.
	git func(ctx context.Context, dir string, args ...string) (string, error)

	mu    sync.Mutex
	cache map[string]*entry
	desc  descCache
}

type entry struct {
	mu       sync.Mutex
	info     protocol.RepoInfo
	staticAt time.Time
	branchAt time.Time
	set      bool
}

// New returns a Resolver with the default limits.
func New() *Resolver {
	return &Resolver{Timeout: DefaultTimeout, StaticTTL: DefaultStaticTTL, BranchTTL: DefaultBranchTTL,
		now: time.Now, git: runGit, cache: map[string]*entry{}}
}

// Lookup returns the repo info of a folder. The branch may be up to BranchTTL old.
func (r *Resolver) Lookup(cwd string) protocol.RepoInfo { return r.lookup(cwd, false) }

// Refresh is Lookup with a fresh read of the branch. Use it when a session updates.
func (r *Resolver) Refresh(cwd string) protocol.RepoInfo { return r.lookup(cwd, true) }

func (r *Resolver) lookup(cwd string, fresh bool) protocol.RepoInfo {
	if strings.TrimSpace(cwd) == "" {
		return protocol.RepoInfo{}
	}
	cwd = filepath.Clean(cwd)
	r.mu.Lock()
	e := r.cache[cwd]
	if e == nil {
		e = &entry{}
		r.cache[cwd] = e
	}
	r.mu.Unlock()

	e.mu.Lock()
	defer e.mu.Unlock()
	now := r.now()
	ctx, cancel := context.WithTimeout(context.Background(), r.Timeout)
	defer cancel()
	if !e.set || now.Sub(e.staticAt) >= r.StaticTTL {
		info := r.readAll(ctx, cwd)
		// A folder that is gone keeps the repository it was last seen in, for
		// example a worktree removed after its pull request merged. A folder
		// that still exists and is not a repository gives its folder name.
		if info.Root == "" && e.info.Root != "" && folderGone(cwd) {
			info.Project, info.Root, info.Remote = e.info.Project, e.info.Root, e.info.Remote
			info.Worktree, info.Branch = e.info.Worktree, e.info.Branch
		}
		e.info = info
		e.staticAt, e.branchAt, e.set = now, now, true
	} else if e.info.Root != "" && (fresh || now.Sub(e.branchAt) >= r.BranchTTL) {
		e.info.Branch = r.readBranch(ctx, cwd)
		e.branchAt = now
	}
	// The description follows the instruction file (one stat per file), not
	// the static TTL. It is read from the main tree, or from the folder when
	// there is no repository.
	dir := e.info.Root
	if dir == "" {
		dir = cwd
	}
	e.info.Description = r.desc.get(dir)
	return e.info
}

// folderGone tells whether the folder itself no longer exists. A folder that
// exists but holds no repository is not gone: git simply has nothing to say
// about it.
func folderGone(path string) bool {
	_, err := os.Stat(path)
	return os.IsNotExist(err)
}

func (r *Resolver) readAll(ctx context.Context, cwd string) protocol.RepoInfo {
	info := protocol.RepoInfo{Project: filepath.Base(cwd)}
	root, err := r.git(ctx, cwd, "rev-parse", "--show-toplevel")
	if err != nil || root == "" {
		return info
	}
	info.Worktree = filepath.Clean(root)
	info.Root = r.mainTree(ctx, cwd, info.Worktree)
	info.Project = filepath.Base(info.Root)
	if remote, err := r.git(ctx, cwd, "config", "--get", "remote.origin.url"); err == nil {
		info.Remote = StripCredentials(remote)
		if name := projectFromRemote(info.Remote); name != "" {
			info.Project = name
		}
	}
	info.Branch = r.readBranch(ctx, cwd)
	return info
}

// projectFromRemote extracts the repository name from URL, scp-style, and
// local path remotes. An empty or unusable remote leaves the folder fallback.
func projectFromRemote(remote string) string {
	remote = strings.TrimSpace(remote)
	if strings.Contains(remote, "://") {
		u, err := url.Parse(remote)
		if err != nil {
			return ""
		}
		remote = u.Path
	} else if i := strings.IndexByte(remote, ':'); i >= 0 {
		remote = remote[i+1:]
	}
	remote = strings.TrimRight(remote, "/")
	if remote == "" {
		return ""
	}
	name := strings.TrimSuffix(filepath.Base(remote), ".git")
	if name == "." || name == ".." {
		return ""
	}
	return name
}

// mainTree returns the main working tree of the repository that worktree
// belongs to. In a linked git worktree, the common git directory is
// <main tree>/.git. When it has another shape (a bare repository, a
// submodule, a separate git directory), the worktree itself is the answer.
func (r *Resolver) mainTree(ctx context.Context, cwd, worktree string) string {
	common, err := r.git(ctx, cwd, "rev-parse", "--git-common-dir")
	if err != nil || common == "" {
		return worktree
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(cwd, common)
	}
	common = filepath.Clean(common)
	if filepath.Base(common) != ".git" {
		return worktree
	}
	return filepath.Dir(common)
}

func (r *Resolver) readBranch(ctx context.Context, cwd string) string {
	if b, err := r.git(ctx, cwd, "symbolic-ref", "--short", "-q", "HEAD"); err == nil && b != "" {
		return b
	}
	if sha, err := r.git(ctx, cwd, "rev-parse", "--short", "HEAD"); err == nil && sha != "" {
		return "detached@" + sha
	}
	return ""
}

// runGit runs git without prompts, optional locks or a user-chosen repository
// from the environment, and with the file system monitor hook off, because the
// folder may hold a repository that we did not make.
func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	full := append([]string{"-c", "core.fsmonitor=false", "-C", dir}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	var env []string
	for _, kv := range os.Environ() {
		switch {
		case strings.HasPrefix(kv, "GIT_DIR="), strings.HasPrefix(kv, "GIT_WORK_TREE="),
			strings.HasPrefix(kv, "GIT_INDEX_FILE="), strings.HasPrefix(kv, "GIT_COMMON_DIR="),
			strings.HasPrefix(kv, "GIT_OBJECT_DIRECTORY="), strings.HasPrefix(kv, "GIT_NAMESPACE="):
		default:
			env = append(env, kv)
		}
	}
	cmd.Env = append(env, "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	var out bytes.Buffer
	cmd.Stdout = &out
	// A child of git may keep the pipe open after git is killed.
	cmd.WaitDelay = 500 * time.Millisecond
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(out.String()), nil
}

// StripCredentials removes passwords and tokens from a remote URL. For http,
// https, ftp and git URLs it drops the whole user part (a token is often the
// user name). For other URLs, such as ssh, it drops only the password and keeps
// the user name. Query and fragment are dropped. A remote like
// git@host:owner/repo has no credentials and is kept.
func StripCredentials(raw string) string {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		// Cut anything between "://" and the last "@" of the authority.
		i := strings.Index(raw, "://") + 3
		rest := raw[i:]
		end := strings.IndexAny(rest, "/?#")
		if end < 0 {
			end = len(rest)
		}
		if at := strings.LastIndex(rest[:end], "@"); at >= 0 {
			rest = rest[at+1:]
		}
		if cut := strings.IndexAny(rest, "?#"); cut >= 0 {
			rest = rest[:cut]
		}
		return raw[:i] + rest
	}
	switch u.Scheme {
	case "ssh", "git+ssh", "ssh+git":
		if u.User != nil {
			u.User = url.User(u.User.Username())
		}
	default:
		u.User = nil
	}
	u.RawQuery, u.Fragment, u.RawFragment = "", "", ""
	return u.String()
}

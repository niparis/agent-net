package daemon

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/niparis/agent-net/internal/loginpath"
	"github.com/niparis/agent-net/internal/protocol"
)

// Git reads and worktree facts for a worker.

var gitPathOnce sync.Once

var gitSearchPath string

func worktreeGitPath() string {
	gitPathOnce.Do(func() {
		login := loginpath.Path()
		var dirs []string
		for _, name := range []string{"git", "git-lfs"} {
			for _, dir := range filepath.SplitList(login) {
				if dir == "" {
					continue
				}
				if fi, err := os.Stat(filepath.Join(dir, name)); err == nil && !fi.IsDir() && fi.Mode()&0111 != 0 {
					dirs = append(dirs, dir)
					break
				}
			}
		}
		gitSearchPath = strings.Join(append(dirs, login), string(os.PathListSeparator))
	})
	// Honor an explicitly selected git on the daemon's current PATH, while
	// adding the login-shell tools for subprocesses such as git-lfs.
	var dirs []string
	if git, err := exec.LookPath("git"); err == nil {
		dirs = append(dirs, filepath.Dir(git))
	}
	dirs = append(dirs, gitSearchPath, os.Getenv("PATH"))
	return strings.Join(dirs, string(os.PathListSeparator))
}

var workerGitCommandTimeout = func() *atomic.Int64 {
	timeout := &atomic.Int64{}
	timeout.Store(int64(20 * time.Second))
	return timeout
}()

type workerGitError struct {
	args   []string
	err    error
	stderr string
}

// git runs a git command in dir and returns its output.
func (e *workerGitError) Error() string {
	return fmt.Sprintf("git %s: %v: %s", strings.Join(e.args, " "), e.err, e.stderr)
}

func (e *workerGitError) Unwrap() error { return e.err }

// boundTask cuts a task to the 64 KiB that a worker record keeps.

func gitOut(ctx context.Context, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(workerGitCommandTimeout.Load()))
	defer cancel()
	return gitOutWithContext(ctx, dir, args...)
}

func gitOutWithContext(ctx context.Context, dir string, args ...string) (string, error) {
	return gitOutWithEnv(ctx, dir, nil, args...)
}

// gitOutWithEnv is gitOut with extra environment entries for the command, such
// as a worker's commit hook settings.
func gitOutWithEnv(ctx context.Context, dir string, extra []string, args ...string) (string, error) {
	return gitWithInput(ctx, dir, extra, nil, args...)
}

// gitOutWithInput is gitOut with stdin, for a command that reads paths such as
// check-ignore --stdin.
func gitOutWithInput(ctx context.Context, dir string, stdin []byte, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(workerGitCommandTimeout.Load()))
	defer cancel()
	return gitWithInput(ctx, dir, nil, stdin, args...)
}

func gitWithInput(ctx context.Context, dir string, extra []string, stdin []byte, args ...string) (string, error) {
	path := worktreeGitPath()
	git := "git"
	for _, dir := range filepath.SplitList(path) {
		candidate := filepath.Join(dir, "git")
		if fi, err := os.Stat(candidate); err == nil && !fi.IsDir() && fi.Mode()&0111 != 0 {
			git = candidate
			break
		}
	}
	cmd := exec.CommandContext(ctx, git, append([]string{"-C", dir}, args...)...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	base := os.Environ()
	if len(extra) > 0 {
		// Extra entries replace inherited command-scope git config and worker
		// trailer values, so an inherited set cannot win or leak in.
		kept := base[:0]
		for _, item := range base {
			k, _, _ := strings.Cut(item, "=")
			if strings.HasPrefix(k, "GIT_CONFIG_") || strings.HasPrefix(k, "AGENT_NET_TRAILER_") {
				continue
			}
			kept = append(kept, item)
		}
		base = kept
	}
	cmd.Env = append(append(base, "PATH="+path, "GIT_TERMINAL_PROMPT=0", "LC_ALL=C"), extra...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", &workerGitError{args: args, err: err, stderr: strings.TrimSpace(errb.String())}
	}
	return strings.TrimRight(out.String(), "\n"), nil
}

// isGitRepo tells whether dir is inside a git work tree with a commit, so that
// a worktree can be made from it.
func isGitRepo(ctx context.Context, dir string) bool {
	_, err := gitOut(ctx, dir, "rev-parse", "--verify", "HEAD")
	return err == nil
}

// worktreeFetchTimeout bounds the fetch that looks for a newer upstream tip.
const worktreeFetchTimeout = 10 * time.Second

// freshUpstreamBase returns the commit a new worktree should start from when
// the owner asked for no base. Work often lands on the remote default branch (a
// merged pull request or a merge queue) while a local branch stays behind. So
// it fetches origin's default branch and returns that tip, except for one case:
// the checkout is the default branch and holds every commit of origin's tip
// plus its own. Those unpushed commits are the owner's own work on the default
// branch, so the worker starts from local HEAD and gets them. A checkout on
// another branch, a detached HEAD or a diverged default branch starts from the
// tip, because its commits would otherwise come with the worker's work and
// show up as unrelated files. A missing origin or a failed fetch starts from
// local HEAD. It never moves the owner's checkout or any local branch: a fetch
// only updates refs/remotes.
func (d *Daemon) freshUpstreamBase(ctx context.Context, main, localHead string) string {
	keep := func(why string) string {
		if d.log != nil {
			d.log.Printf("worktree base: starting from local HEAD %s in %s: %s", shortCommit(localHead), main, why)
		}
		return localHead
	}
	target := cleanupDefaultBranch(ctx, main, "")
	if target == "" {
		return keep("there is no origin default branch, local main or local master")
	}
	if _, err := gitOut(ctx, main, "remote", "get-url", "origin"); err != nil {
		return keep("the repository has no origin remote")
	}
	remoteRef := "refs/remotes/origin/" + target
	fctx, cancel := context.WithTimeout(ctx, worktreeFetchTimeout)
	defer cancel()
	if _, err := gitOutWithContext(fctx, main, "fetch", "--quiet", "--no-tags", "origin", "+refs/heads/"+target+":"+remoteRef); err != nil {
		return keep("fetch of origin/" + target + " failed: " + err.Error())
	}
	tip, err := gitOut(ctx, main, "rev-parse", "--verify", remoteRef+"^{commit}")
	if err != nil {
		return keep("cannot read " + remoteRef + ": " + err.Error())
	}
	if _, err := gitOut(ctx, main, "merge-base", "--is-ancestor", localHead, tip); err == nil {
		if tip != localHead && d.log != nil {
			d.log.Printf("worktree base: starting from origin/%s %s (local %s is behind) in %s", target, shortCommit(tip), shortCommit(localHead), main)
		}
		return tip
	}
	branch, _ := gitOut(ctx, main, "symbolic-ref", "--quiet", "--short", "HEAD")
	if branch == target {
		if _, err := gitOut(ctx, main, "merge-base", "--is-ancestor", tip, localHead); err == nil {
			if d.log != nil {
				d.log.Printf("worktree base: starting from local %s %s, %d commits not yet pushed in %s", target, shortCommit(localHead), commitCount(ctx, main, tip+".."+localHead), main)
			}
			return localHead
		}
	}
	why := "local HEAD is detached"
	switch {
	case branch == "":
	case branch != target:
		why = "local HEAD is on branch " + branch + ", not " + target
	default:
		why = "local " + target + " and origin/" + target + " have diverged"
	}
	if d.log != nil {
		d.log.Printf("worktree base: starting from origin/%s %s (%s) in %s", target, shortCommit(tip), why, main)
	}
	return tip
}

// commitCount counts the commits in a rev range. It returns 0 when git cannot
// count them, which only shortens the log line.
func commitCount(ctx context.Context, dir, rng string) int {
	out, err := gitOut(ctx, dir, "rev-list", "--count", rng)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0
	}
	return n
}

func shortCommit(c string) string {
	if len(c) > 12 {
		return c[:12]
	}
	return c
}

// untrackedWorkFiles lists the untracked files in dir that the worker-end commit
// would save. Sandbox placeholders and harness files are left out, as the
// commit leaves them out.
func untrackedWorkFiles(ctx context.Context, dir string) []string {
	out, err := gitOut(ctx, dir, "ls-files", "-z", "--others", "--exclude-standard")
	if err != nil {
		return nil
	}
	var files []string
	for _, path := range strings.Split(out, "\x00") {
		if path == "" {
			continue
		}
		if notWorkerWork(dir, worktreeChange{path: path, untracked: true}) {
			continue
		}
		files = append(files, path)
	}
	return files
}

// worktreeFacts returns git diff --stat of the worktree against the commit it
// started from, with the untracked files, and whether there is anything.
func (d *Daemon) worktreeFacts(w *worker) (stat string, changed bool) {
	d.wmu.Lock()
	dir, base := w.worktree, w.base
	d.wmu.Unlock()
	if dir == "" {
		return "", false
	}
	ctx := context.Background()
	start := base
	if head, err := gitOut(ctx, dir, "rev-parse", "HEAD"); err == nil {
		base = noticeDiffBase(ctx, dir, base, head)
	}
	var parts []string
	if base != "" {
		if s, err := gitOut(ctx, dir, "diff", "--stat", base); err == nil && s != "" {
			parts = append(parts, noticeDiffStat(s))
		}
	}
	if files := untrackedWorkFiles(ctx, dir); len(files) > 0 {
		more := ""
		if len(files) > 20 {
			files, more = files[:20], fmt.Sprintf(" and %d more", len(files)-20)
		}
		parts = append(parts, "untracked: "+strings.Join(files, ", ")+more)
	}
	if n, err := gitOut(ctx, dir, "rev-list", "--count", start+"..HEAD"); err == nil && n != "0" && n != "" && len(parts) == 0 {
		parts = append(parts, n+" commit(s) with no file changes")
	}
	return strings.Join(parts, "\n"), len(parts) > 0
}

// noticeDiffBase is the commit a notice measures its diff stat from. A worker
// keeps the base it started from, so a branch stacked on an unmerged release
// train shows only the worker's own work. When the worker rebased onto newer
// work, the merge-base with the default branch is newer than that base, and
// counting from the base would include other people's files. With no recorded
// base, the merge-base is used; with no default branch either, there is no
// base and the notice has no diff stat.
func noticeDiffBase(ctx context.Context, dir, recorded, head string) string {
	if !protocol.ValidCommit(head) {
		return recorded
	}
	base := ""
	consider := func(candidate string) {
		if !protocol.ValidCommit(candidate) {
			return
		}
		if candidate != head {
			if _, err := gitOut(ctx, dir, "merge-base", "--is-ancestor", candidate, head); err != nil {
				return
			}
		}
		if base == "" {
			base = candidate
			return
		}
		if candidate == base {
			return
		}
		if _, err := gitOut(ctx, dir, "merge-base", "--is-ancestor", base, candidate); err == nil {
			base = candidate
		}
	}
	consider(recorded)
	if target := cleanupDefaultBranch(ctx, dir, ""); target != "" {
		for _, ref := range []string{target, "origin/" + target} {
			if candidate, err := gitOut(ctx, dir, "merge-base", ref, head); err == nil {
				consider(candidate)
			}
		}
	}
	return base
}

// noticeDiffStat bounds the notice's diff stat to the delivery limit, so a
// branch with a huge diff cannot push the notice past the body limit. It keeps
// whole lines and names how many files it left out. git's trailing summary line
// goes, because it counts files that are not in the list.
func noticeDiffStat(stat string) string {
	stat = cleanDiffStat(stat)
	if len(stat) <= protocol.MaxDiffStatBytes {
		return stat
	}
	lines := strings.Split(strings.TrimRight(stat, "\n"), "\n")
	if n := len(lines); n > 0 && isDiffStatSummary(lines[n-1]) {
		lines = lines[:n-1]
	}
	// Leave room for the tail that names the files left out.
	const tailRoom = 64
	budget := protocol.MaxDiffStatBytes - tailRoom
	var kept []string
	size := 0
	for _, line := range lines {
		add := len(line) + 1
		if size+add > budget {
			break
		}
		kept = append(kept, line)
		size += add
	}
	omitted := len(lines) - len(kept)
	noun := "files"
	if omitted == 1 {
		noun = "file"
	}
	tail := fmt.Sprintf("... and %d more %s", omitted, noun)
	if len(kept) == 0 {
		return tail
	}
	out := strings.Join(kept, "\n") + "\n" + tail
	if len(out) > protocol.MaxDiffStatBytes {
		out = deliveryDiffStat(out)
	}
	return out
}

// isDiffStatSummary says whether a git diff --stat line is the trailing
// summary rather than a file line.
func isDiffStatSummary(line string) bool {
	return strings.Contains(line, " changed") && !strings.Contains(line, "|")
}

// gitOperationInProgress tells whether the worktree is in the middle of a
// merge, rebase, cherry-pick or revert.
func gitOperationInProgress(ctx context.Context, dir string) bool {
	for _, name := range []string{"MERGE_HEAD", "rebase-merge", "rebase-apply", "CHERRY_PICK_HEAD", "REVERT_HEAD"} {
		p, err := gitOut(ctx, dir, "rev-parse", "--git-path", name)
		if err != nil || p == "" {
			continue
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(dir, p)
		}
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

// worktreeCommit is the full hash of the worker's worktree HEAD, or "".
func (d *Daemon) worktreeCommit(w *worker) string {
	d.wmu.Lock()
	dir := w.worktree
	d.wmu.Unlock()
	if dir == "" {
		return ""
	}
	head, err := gitOut(context.Background(), dir, "rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	return head
}

// worktreeHead is the short hash of the worker's worktree HEAD, or "".
func (d *Daemon) worktreeHead(w *worker) string {
	d.wmu.Lock()
	dir := w.worktree
	d.wmu.Unlock()
	if dir == "" {
		return ""
	}
	head, err := gitOut(context.Background(), dir, "rev-parse", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return head
}

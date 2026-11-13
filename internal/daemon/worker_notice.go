package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/niparis/agent-net/internal/loginpath"

	"github.com/niparis/agent-net/internal/protocol"
)

// Worker hand-back, completion notices and worktrees (decision 0002, W10, W11
// and W13).

// ---- worktrees ----

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

func (e *workerGitError) Error() string {
	return fmt.Sprintf("git %s: %v: %s", strings.Join(e.args, " "), e.err, e.stderr)
}

func (e *workerGitError) Unwrap() error { return e.err }

// git runs a git command in dir and returns its output.
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
// the owner asked for no base. Work often lands on the remote (a merged pull
// request) while the local branch stays behind. So it fetches the upstream of
// the checked-out branch and, when local HEAD is an ancestor of the fetched
// tip, returns that tip. In every other case (detached HEAD, no upstream,
// unpushed or diverged commits, a failed fetch) it returns localHead and logs
// why. It never moves the owner's checkout or any local branch: a fetch only
// updates refs/remotes.
func (d *Daemon) freshUpstreamBase(ctx context.Context, main, localHead string) string {
	keep := func(why string) string {
		if d.log != nil {
			d.log.Printf("worktree base: starting from local HEAD %s in %s: %s", shortCommit(localHead), main, why)
		}
		return localHead
	}
	branch, err := gitOut(ctx, main, "symbolic-ref", "--short", "-q", "HEAD")
	if err != nil || branch == "" {
		return keep("HEAD is not on a branch")
	}
	upstream, err := gitOut(ctx, main, "rev-parse", "--symbolic-full-name", "@{u}")
	if err != nil || !strings.HasPrefix(upstream, "refs/remotes/") {
		return keep("branch " + branch + " has no remote upstream")
	}
	remote, err := gitOut(ctx, main, "config", "--get", "branch."+branch+".remote")
	if err != nil || remote == "" || remote == "." || strings.HasPrefix(remote, "-") {
		return keep("branch " + branch + " has no remote")
	}
	merge, err := gitOut(ctx, main, "config", "--get", "branch."+branch+".merge")
	if err != nil || !strings.HasPrefix(merge, "refs/heads/") {
		return keep("branch " + branch + " has no upstream branch")
	}
	fctx, cancel := context.WithTimeout(ctx, worktreeFetchTimeout)
	defer cancel()
	if _, err := gitOutWithContext(fctx, main, "fetch", "--quiet", remote, "+"+merge+":"+upstream); err != nil {
		return keep("fetch of " + remote + " failed: " + err.Error())
	}
	tip, err := gitOut(ctx, main, "rev-parse", "--verify", upstream+"^{commit}")
	if err != nil {
		return keep("cannot read " + upstream + ": " + err.Error())
	}
	if _, err := gitOut(ctx, main, "merge-base", "--is-ancestor", localHead, tip); err != nil {
		return keep("local " + branch + " has commits that " + upstream + " does not")
	}
	if tip != localHead && d.log != nil {
		d.log.Printf("worktree base: starting from %s %s (local %s is behind) in %s", upstream, shortCommit(tip), shortCommit(localHead), main)
	}
	return tip
}

func shortCommit(c string) string {
	if len(c) > 12 {
		return c[:12]
	}
	return c
}

// createWorktree makes the worker's own worktree of its project, on a new
// branch, in a folder the daemon manages (not in the user's checkout). The
// worker then runs in it.
func (d *Daemon) createWorktree(ctx context.Context, w *worker, requestedBase ...string) error {
	main := w.path
	ref := "HEAD"
	requested := len(requestedBase) > 0 && requestedBase[0] != ""
	if requested {
		name := requestedBase[0]
		if strings.HasPrefix(name, "-") {
			return fmt.Errorf("invalid base branch %q", name)
		}
		if _, err := gitOut(ctx, main, "check-ref-format", "--branch", name); err != nil {
			return fmt.Errorf("invalid base branch %q: %w", name, err)
		}
		ref = "refs/heads/" + name
	}
	base, err := gitOut(ctx, main, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		if ref != "HEAD" {
			return fmt.Errorf("base branch %q does not exist: %w", requestedBase[0], err)
		}
		return fmt.Errorf("cannot make a worktree: %s is not a git repository with a commit (%v)", main, err)
	}
	if !requested {
		base = d.freshUpstreamBase(ctx, main, base)
	}
	dir := filepath.Join(d.cfg.StateDir, "worktrees", w.id)
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return err
	}
	branch := protocol.WorktreeBranchWithSettings(w.id, d.SettingsSnapshot())
	d.wmu.Lock()
	previousBase := w.base
	if w.branch != "" {
		branch = w.branch
	}
	d.wmu.Unlock()
	// A resumed worker's branch stays after its worktree folder is removed (a
	// disk cleanup). Make the worktree on that branch instead of failing on
	// -b, so the commits the branch holds are still there.
	reuse := false
	if _, err := gitOut(ctx, main, "rev-parse", "--verify", "refs/heads/"+branch+"^{commit}"); err == nil {
		reuse = true
	}
	args := []string{"worktree", "add", dir}
	if reuse {
		// A registration of the removed folder would block the add.
		if _, err := gitOut(ctx, main, "worktree", "prune"); err != nil {
			return fmt.Errorf("cannot make a worktree: git worktree prune: %w", err)
		}
		args = append(args, branch)
	} else {
		args = append(args, "-b", branch, base)
	}
	if _, err := gitOutWithContext(ctx, main, args...); err != nil {
		var gitErr *workerGitError
		if errors.As(err, &gitErr) {
			message := gitErr.stderr
			missingLFS := strings.Contains(message, "git-lfs: command not found")
			if len(message) > 200 {
				message = message[len(message)-200:]
			}
			if missingLFS {
				return fmt.Errorf("cannot make a worktree: git-lfs is missing on this machine: %s: %w", message, gitErr.err)
			}
			return fmt.Errorf("cannot make a worktree: git worktree add: %s: %w", message, gitErr.err)
		}
		return fmt.Errorf("cannot make a worktree: %w", err)
	}
	if err := d.copyWorktreeSkills(ctx, main, dir); err != nil {
		_, _ = gitOut(ctx, main, "worktree", "remove", "--force", dir)
		return err
	}
	d.wmu.Lock()
	w.mainPath, w.path, w.worktree, w.branch = main, dir, dir, branch
	if !reuse || previousBase == "" {
		w.base = base
	}
	d.wmu.Unlock()
	return nil
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
		base = handbackDiffBase(ctx, dir, base, head)
	}
	var parts []string
	if s, err := gitOut(ctx, dir, "diff", "--stat", base); err == nil && s != "" {
		parts = append(parts, s)
	}
	if u, err := gitOut(ctx, dir, "ls-files", "--others", "--exclude-standard"); err == nil && u != "" {
		files := strings.Split(u, "\n")
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

// unfinishedSubject is the subject of the commit that keeps unfinished work.
func unfinishedSubject(workerID, reason string) string {
	return "agent-net: unfinished work of worker " + workerID + " (" + reason + ")"
}

// boundTask cuts a task to the 64 KiB that a worker record keeps.
func boundTask(s string) string {
	const max = 64 * 1024
	if len(s) > max {
		s = strings.ToValidUTF8(s[:max], "")
	}
	return s
}

func first(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// saveUnfinished commits lost worker changes during adoption, before any
// notice reporting can fail. A clean worktree is an idempotent no-op.
func (d *Daemon) saveUnfinished(w *worker, reason string) bool {
	if w.worktree == "" {
		return false
	}
	unlock := d.lockWorktree(w.worktree)
	defer unlock()
	if w.harness == "agy" {
		result := d.saveAgyWorkerEnd(w, reason)
		return result.SHA != ""
	}
	result := d.commitWorkerEndWIP(context.Background(), w.worktree, w.branch, reason)
	if result.Failed != "" {
		d.Logf("worker %s: adoption WIP commit: %s", w.id, result.Failed)
	}
	return result.SHA != ""
}

type worktreeLock struct {
	mu   sync.Mutex
	refs int
}

// lockWorktree serializes git operations by path, including operations that
// rebuild a worker value from a persisted row during daemon adoption.
func (d *Daemon) lockWorktree(path string) func() {
	if path == "" {
		return func() {}
	}
	key, err := filepath.Abs(path)
	if err != nil {
		key = filepath.Clean(path)
	} else {
		key = filepath.Clean(key)
	}
	d.worktreeLocksMu.Lock()
	if d.worktreeLocks == nil {
		d.worktreeLocks = make(map[string]*worktreeLock)
	}
	lock := d.worktreeLocks[key]
	if lock == nil {
		lock = &worktreeLock{}
		d.worktreeLocks[key] = lock
	}
	lock.refs++
	d.worktreeLocksMu.Unlock()
	lock.mu.Lock()
	return func() {
		lock.mu.Unlock()
		d.worktreeLocksMu.Lock()
		lock.refs--
		if lock.refs == 0 && d.worktreeLocks[key] == lock {
			delete(d.worktreeLocks, key)
		}
		d.worktreeLocksMu.Unlock()
	}
}

type workerEndCommitResult struct {
	SHA     string
	Files   int
	Skipped string
	Failed  string
	Legacy  bool // Agy keeps its historical title-based commit subject.
}

func workerCommitFileCount(ctx context.Context, dir, rev string) int {
	files, err := gitOut(ctx, dir, "show", "--format=", "--name-only", rev)
	if err != nil {
		return 0
	}
	count := 0
	for _, line := range strings.Split(files, "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}

func (r workerEndCommitResult) noticeDetail() string {
	if r.Failed != "" {
		if r.Legacy {
			return "left uncommitted changes; unfinished work commit failed: " + r.Failed
		}
		return "left uncommitted changes; WIP commit failed: " + r.Failed
	}
	if r.Skipped == "git operation in progress" {
		return ""
	}
	if r.SHA == "" {
		if r.Skipped != "" {
			return "left uncommitted changes; skipped: " + r.Skipped
		}
		return ""
	}
	kind := "WIP "
	if r.Legacy {
		kind = ""
	}
	detail := fmt.Sprintf("left uncommitted changes; committed as %s%s: %d files", kind, r.SHA, r.Files)
	if r.Skipped != "" {
		detail += "; skipped: " + r.Skipped
	}
	return detail
}

func (r workerEndCommitResult) keepWorktree() bool {
	return r.Failed != "" || r.Skipped != ""
}

// workerIndexLockStaleAfter is how old an empty index lock must be before a
// WIP save may treat it as the one a dead git process left behind.
const workerIndexLockStaleAfter = 10 * time.Minute

// wipLockPolicy says whether a WIP save may clear a stale index lock. An empty
// WorktreesRoot leaves every lock alone.
type wipLockPolicy struct {
	WorktreesRoot string
	Logf          func(format string, args ...any)
}

// commitWorkerEndWIP is commitWorkerEndWIPWithPolicy for this daemon: a stale
// lock is cleared only inside the daemon's own worktrees folder.
func (d *Daemon) commitWorkerEndWIP(ctx context.Context, dir, branch, reason string) workerEndCommitResult {
	return commitWorkerEndWIPWithPolicy(ctx, dir, branch, reason, wipLockPolicy{WorktreesRoot: d.worktreesDir(), Logf: d.Logf})
}

// commitWorkerEndWIPWithoutLockPolicy leaves every index lock alone, for
// callers that do not own a worktrees folder.
func commitWorkerEndWIP(ctx context.Context, dir, branch, reason string) workerEndCommitResult {
	return commitWorkerEndWIPWithPolicy(ctx, dir, branch, reason, wipLockPolicy{})
}

// commitWorkerEndWIPWithPolicy saves the WIP once, and once more after it
// removes a stale index lock that a dead git process left behind.
func commitWorkerEndWIPWithPolicy(ctx context.Context, dir, branch, reason string, policy wipLockPolicy) workerEndCommitResult {
	result := commitWorkerEndWIPOnce(ctx, dir, branch, reason)
	if result.Failed == "" || policy.WorktreesRoot == "" || !strings.Contains(result.Failed, "index.lock") {
		return result
	}
	removed, _ := removeStaleIndexLock(ctx, dir, policy)
	if !removed {
		return result
	}
	if policy.Logf != nil {
		policy.Logf("worker worktree %s: removed stale index lock", dir)
	}
	return commitWorkerEndWIPOnce(ctx, dir, branch, reason)
}

// removeStaleIndexLock removes the index lock of the WIP worktree when it is
// empty, older than workerIndexLockStaleAfter, inside the daemon's worktrees
// folder, and no git process uses the worktree. It says whether it removed the
// file, and why not.
func removeStaleIndexLock(ctx context.Context, dir string, policy wipLockPolicy) (bool, string) {
	root := strings.TrimSpace(policy.WorktreesRoot)
	if root == "" {
		return false, ""
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return false, err.Error()
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false, err.Error()
	}
	if absDir == absRoot || !strings.HasPrefix(absDir, absRoot+string(filepath.Separator)) {
		return false, "the worktree is not under the daemon's worktrees folder"
	}
	indexPath, err := gitOut(ctx, dir, "rev-parse", "--git-path", "index")
	if err != nil {
		return false, err.Error()
	}
	if !filepath.IsAbs(indexPath) {
		indexPath = filepath.Join(dir, indexPath)
	}
	lockPath := indexPath + ".lock"
	info, err := os.Stat(lockPath)
	if err != nil {
		return false, ""
	}
	if info.Size() != 0 {
		return false, "the index lock is not empty"
	}
	if time.Since(info.ModTime()) < workerIndexLockStaleAfter {
		return false, "the index lock is recent"
	}
	if gitProcessUsesWorktree(absDir) {
		return false, "a git process is using the worktree"
	}
	if err := os.Remove(lockPath); err != nil {
		return false, err.Error()
	}
	return true, ""
}

// gitProcessUsesWorktree says whether a running git process works in dir or a
// folder under it. A system without /proc says yes, so a lock is never removed
// where the daemon cannot look.
func gitProcessUsesWorktree(dir string) bool {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return true
	}
	seen := false
	for _, e := range entries {
		pid := e.Name()
		if pid == "" || pid[0] < '0' || pid[0] > '9' {
			continue
		}
		seen = true
		comm, err := os.ReadFile(filepath.Join("/proc", pid, "comm"))
		if err != nil {
			continue
		}
		name := strings.TrimSpace(string(comm))
		if name != "git" && !strings.HasPrefix(name, "git-") {
			continue
		}
		if cwd, err := os.Readlink(filepath.Join("/proc", pid, "cwd")); err == nil &&
			(cwd == dir || strings.HasPrefix(cwd, dir+string(filepath.Separator))) {
			return true
		}
		cmdline, err := os.ReadFile(filepath.Join("/proc", pid, "cmdline"))
		if err != nil {
			continue
		}
		for _, arg := range strings.Split(string(cmdline), "\x00") {
			if filepath.IsAbs(arg) && (arg == dir || strings.HasPrefix(arg, dir+string(filepath.Separator))) {
				return true
			}
		}
	}
	return !seen
}

// commitWorkerEndWIP commits remaining work using a fixed daemon identity. It
// shares stageWorkerLeftovers with hand-back commits so ignored and oversized
// files follow the same filtering rules.
func commitWorkerEndWIPOnce(ctx context.Context, dir, branch, reason string) workerEndCommitResult {
	if dir == "" {
		return workerEndCommitResult{}
	}
	status, err := gitOut(ctx, dir, "status", "--porcelain")
	if err != nil {
		return workerEndCommitResult{Failed: err.Error()}
	}
	if status == "" {
		return workerEndCommitResult{}
	}
	if branch != "" {
		current, err := gitOut(ctx, dir, "branch", "--show-current")
		if err != nil || current != branch {
			return workerEndCommitResult{Failed: "worktree is not on worker branch"}
		}
	}
	if gitOperationInProgress(ctx, dir) {
		return workerEndCommitResult{Skipped: "git operation in progress"}
	}
	indexPath, err := gitOut(ctx, dir, "rev-parse", "--git-path", "index")
	if err != nil {
		return workerEndCommitResult{Failed: err.Error()}
	}
	if !filepath.IsAbs(indexPath) {
		indexPath = filepath.Join(dir, indexPath)
	}
	indexInfo, err := os.Stat(indexPath)
	if err != nil {
		return workerEndCommitResult{Failed: err.Error()}
	}
	indexBefore, err := os.ReadFile(indexPath)
	if err != nil {
		return workerEndCommitResult{Failed: err.Error()}
	}
	restoreIndex := func() {
		_ = os.WriteFile(indexPath, indexBefore, indexInfo.Mode().Perm())
	}
	excluded, err := stageWorkerLeftovers(ctx, dir)
	if err != nil {
		restoreIndex()
		return workerEndCommitResult{Skipped: excluded, Failed: err.Error()}
	}
	staged, err := gitOut(ctx, dir, "diff", "--cached", "--name-only")
	if err != nil {
		restoreIndex()
		return workerEndCommitResult{Skipped: excluded, Failed: err.Error()}
	}
	if staged == "" {
		return workerEndCommitResult{Skipped: excluded}
	}
	args := []string{"-c", "user.name=agent-net", "-c", "user.email=agent-net@localhost", "-c", "author.name=agent-net", "-c", "author.email=agent-net@localhost", "-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null", "commit", "-q", "--no-verify", "-m", "WIP: uncommitted changes at worker end (" + reason + ")"}
	if _, err := gitOut(ctx, dir, args...); err != nil {
		restoreIndex()
		return workerEndCommitResult{Skipped: excluded, Failed: err.Error()}
	}
	sha, err := gitOut(ctx, dir, "rev-parse", "--short", "HEAD")
	if err != nil {
		return workerEndCommitResult{Skipped: excluded, Failed: err.Error()}
	}
	return workerEndCommitResult{SHA: sha, Files: workerCommitFileCount(ctx, dir, "HEAD"), Skipped: excluded}
}

// saveAgyWorkerEnd preserves Antigravity's historical title-based unfinished
// commit while running it in the worker-end job.
func (d *Daemon) saveAgyWorkerEnd(w *worker, reason string) workerEndCommitResult {
	ctx := context.Background()
	status, err := gitOut(ctx, w.worktree, "status", "--porcelain")
	if err != nil {
		return workerEndCommitResult{Failed: err.Error(), Legacy: true}
	}
	if status == "" {
		return workerEndCommitResult{}
	}
	before := d.worktreeHead(w)
	d.wmu.Lock()
	subject := strings.TrimSpace(strings.SplitN(w.title, "\n", 2)[0])
	d.wmu.Unlock()
	if subject == "" {
		subject = unfinishedSubject(w.id, reason)
	}
	if !d.saveWorkerChanges(w, subject, reason, true) {
		d.wmu.Lock()
		leftAsIs, excluded := w.leftAsIs, w.excludedLeftovers
		d.wmu.Unlock()
		if leftAsIs {
			return workerEndCommitResult{Skipped: "git operation in progress", Legacy: true}
		}
		return workerEndCommitResult{Skipped: excluded, Failed: "could not commit Antigravity worker changes", Legacy: true}
	}
	sha := d.worktreeHead(w)
	if sha == before {
		return workerEndCommitResult{Failed: "Antigravity commit did not advance HEAD", Legacy: true}
	}
	d.wmu.Lock()
	excluded := w.excludedLeftovers
	d.wmu.Unlock()
	return workerEndCommitResult{SHA: sha, Files: workerCommitFileCount(ctx, w.worktree, "HEAD"), Skipped: excluded, Legacy: true}
}

func (d *Daemon) saveWorkerChanges(w *worker, subject, reason string, unfinished bool) bool {
	d.wmu.Lock()
	dir, id, branch := w.worktree, w.id, w.branch
	d.wmu.Unlock()
	if dir == "" {
		return false
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return false
	}
	ctx := context.Background()
	if branch != "" {
		if current, err := gitOut(ctx, dir, "branch", "--show-current"); err != nil || current != branch {
			d.Logf("worker %s: cannot save changes: worktree is not on worker branch", id)
			return false
		}
	}
	if st, err := gitOut(ctx, dir, "status", "--porcelain"); err != nil || st == "" {
		return false
	}
	// A commit now would drop the merge parents (or the rest of a rebase), so
	// the worktree stays as it is. It is kept because it has changes.
	if gitOperationInProgress(ctx, dir) {
		d.wmu.Lock()
		w.leftAsIs = true
		d.wmu.Unlock()
		d.Logf("worker.unfinished_left worker=%s reason=%s path=%s (a git merge, rebase, cherry-pick or revert is in progress)", id, reason, dir)
		return false
	}
	excluded, err := stageWorkerLeftovers(ctx, dir)
	d.wmu.Lock()
	w.excludedLeftovers = excluded
	d.wmu.Unlock()
	if err != nil {
		d.Logf("worker %s: keep unfinished work: %v", id, err)
		return false
	}
	if diff, err := gitOut(ctx, dir, "diff", "--cached", "--name-only"); err != nil || diff == "" {
		return false
	}
	args := []string{"-c", "commit.gpgsign=false"}
	if v, _ := gitOut(ctx, dir, "config", "user.name"); v == "" {
		args = append(args, "-c", "user.name=agent-net")
	}
	if v, _ := gitOut(ctx, dir, "config", "user.email"); v == "" {
		args = append(args, "-c", "user.email=agent-net@localhost")
	}
	args = append(args, "commit", "-q", "--no-verify", "-m", subject)
	if _, err := gitOutWithEnv(ctx, dir, d.workerGitEnv(w, nil, nil), args...); err != nil {
		d.Logf("worker %s: keep unfinished work: %v", id, err)
		return false
	}
	d.wmu.Lock()
	w.unfinished = unfinished
	d.wmu.Unlock()
	d.Logf("worker.unfinished_saved worker=%s reason=%s path=%s", id, reason, dir)
	return true
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

// finishWorktree keeps ended worktrees until the next cleanup check.
func (d *Daemon) finishWorktree(w *worker) {
	d.wmu.Lock()
	dir, branch := w.worktree, w.branch
	d.wmu.Unlock()
	if dir == "" {
		return
	}
	if d.isOwnerWorktree(dir) {
		d.Logf("worker.worktree kept owner path=%s", dir)
		return
	}
	d.Logf("worker.worktree kept worker=%s path=%s branch=%s", w.id, dir, branch)
}

// ---- stats from the harness ----

// workerStats is what the harness reports about a worker's run.
type workerStats struct {
	Tokens string
	Cost   string
	Model  string
	// ContextTokens is the tokens the model saw on its last step, ContextLimit
	// the model's context window, both 0 when the harness cannot say.
	ContextTokens int64
	ContextLimit  int64
	// TotalTokens is input plus output plus reasoning for the run, no cache
	// reads. 0 when the harness cannot say.
	TotalTokens int64
	// Usage is the same run by kind of token. Zero when the harness cannot say.
	Usage protocol.RunTokens
}

// workerStats asks the launcher, with a short time limit. When the harness
// cannot say, it keeps the last complete context pair for notices and views.
func (d *Daemon) workerStats(w *worker) workerStats {
	var st workerStats
	if ln, ok := d.launchers[w.harness].(interface {
		stats(ctx context.Context, d *Daemon, w *worker) workerStats
	}); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		st = ln.stats(ctx, d, w)
	}
	d.wmu.Lock()
	defer d.wmu.Unlock()
	return lastWorkerContext(w, st)
}

// lastWorkerContext keeps the last complete context pair through failed reads
// and turns that have not reported usage yet. A real value may be lower after
// compaction, so this is not a high-water mark. A harness without a context
// window for the model still keeps the tokens it reports: with no limit, a
// token count alone is the whole picture. The caller holds d.wmu.
func lastWorkerContext(w *worker, st workerStats) workerStats {
	if st.ContextLimit <= 0 && w.contextLimit <= 0 && st.ContextTokens > 0 {
		return st
	}
	if st.ContextTokens <= 0 || st.ContextLimit <= 0 {
		st.ContextTokens, st.ContextLimit = w.contextTokens, w.contextLimit
	}
	return st
}

// tokenText formats token counts, or "" when there are none.
func tokenText(in, out, reasoning, cacheRead, cacheWrite float64) string {
	if in+out+reasoning+cacheRead+cacheWrite == 0 {
		return ""
	}
	s := fmt.Sprintf("%.0f in, %.0f out", in, out)
	if reasoning > 0 {
		s += fmt.Sprintf(", %.0f reasoning", reasoning)
	}
	if cacheRead+cacheWrite > 0 {
		s += fmt.Sprintf(", %.0f cache read, %.0f cache write", cacheRead, cacheWrite)
	}
	return s
}

// ---- notices ----

// notice builds the facts of a completion notice.
func (d *Daemon) notice(w *worker, status, note string, st workerStats) protocol.WorkerNotice {
	d.wmu.Lock()
	sid, harness, model, project, wt, br, started, permissions, title, osSandbox := w.sessionID, w.harness, w.model, w.project, w.worktree, w.branch, w.startedAt, w.permissions, w.title, w.osSandbox
	cont, fix := w.continueCount, w.fixCount
	auto := w.autoContinueCount
	asked, used := w.difficultyAsked, w.difficultyUsed
	struggle := w.runStruggle()
	d.wmu.Unlock()
	if model == "" {
		model = st.Model
	}
	difficulty := first(asked, used)
	if difficulty == "" {
		difficulty = "normal"
	}
	n := protocol.WorkerNotice{Status: status, Note: note, Session: sid, Harness: harness, Model: model, Project: project, Title: title, Permissions: permissions, OSSandbox: osSandbox,
		Host: d.hostName(), RunTime: time.Since(started), Tokens: st.Tokens, Context: protocol.ContextText(st.ContextTokens, st.ContextLimit),
		Cost: st.Cost, Worktree: wt, Branch: br, Continue: cont, Fix: fix, AutoContinue: auto, Difficulty: difficulty, Struggle: struggle}
	n.APICost = d.apiCostText(harness, model, st.Usage)
	d.wmu.Lock()
	n.Evidence = w.evidence
	d.wmu.Unlock()
	n.DiffStat, _ = d.worktreeFacts(w)
	n.Head = d.worktreeHead(w)
	d.wmu.Lock()
	n.Saved = w.unfinished
	leftAsIs := w.leftAsIs
	excluded := w.excludedLeftovers
	d.wmu.Unlock()
	if excluded != "" {
		n.Note += "\nnot committed (build output or large file): " + excluded
	}
	if auto := protocol.WorkerAutoContinueNote(n.AutoContinue); auto != "" {
		n.Note = strings.TrimSpace(n.Note + " " + auto)
	}
	if leftAsIs {
		n.Note = strings.TrimSpace(n.Note + " A git merge was in progress; the worktree was left as is.")
	}
	return n
}

// queueWorkerEnd keeps end notices in decision order without holding up the
// worker loop. The worker stays registered until its notice and cleanup finish,
// so a resume cannot change the facts that go into the notice.
func (d *Daemon) queueWorkerEnd(fn func()) <-chan struct{} {
	d.workerEndMu.Lock()
	previous := d.workerEndTail
	done := make(chan struct{})
	d.workerEndTail = done
	// Register before Stop closes admission and waits for background work. An
	// end already in progress when admission closes must still send (or reach
	// the durable outbox), so its producer finishes it inline during shutdown.
	d.bgMu.Lock()
	background := !d.bgClosed
	if background {
		d.bg.Add(1)
	}
	d.bgMu.Unlock()
	d.workerEndMu.Unlock()
	run := func() {
		defer close(done)
		if previous != nil {
			<-previous
		}
		fn()
	}
	if background {
		go func() {
			defer d.bg.Done()
			run()
		}()
	} else {
		run()
	}
	return done
}

// rateOffer asks the gateway whether the caller should be asked to rate this
// worker. It is best effort: an old gateway, or one that is not reachable,
// leaves the notice as it is.
func (d *Daemon) rateOffer(ctx context.Context, w *worker) bool {
	d.wmu.Lock()
	sid := w.sessionID
	d.wmu.Unlock()
	if sid == "" {
		return false
	}
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	raw, err := d.call(cctx, "rate.offer", protocol.RateOfferFrame{SessionID: sid})
	if err != nil {
		return false
	}
	var out protocol.RateOfferResult
	if json.Unmarshal(raw, &out) != nil {
		return false
	}
	return out.Ask
}

// sendNotice sends the notice, with the report below it if there is one, to
// the worker's owner. It is a reply to the last request of the owner, so the
// caller sees it in that conversation. The sender is the worker's session.
func (d *Daemon) sendNotice(ctx context.Context, w *worker, n protocol.WorkerNotice, report string) (json.RawMessage, error) {
	return d.sendNoticeTo(ctx, w, n, report, "", "")
}

// sendNoticeTo is sendNotice to another session than the owner, as a reply to
// another message than the owner's last request. Empty to and replyTo mean the
// owner and its last request.
func (d *Daemon) sendNoticeTo(ctx context.Context, w *worker, n protocol.WorkerNotice, report, to, replyTo string) (json.RawMessage, error) {
	return d.sendNoticeToKey(ctx, w, n, report, to, replyTo, "", true)
}

func (d *Daemon) sendNoticeToKey(ctx context.Context, w *worker, n protocol.WorkerNotice, report, to, replyTo, noticeKey string, final bool) (json.RawMessage, error) {
	return d.sendNoticeToKeyWithFiles(ctx, w, n, report, to, replyTo, noticeKey, final, nil)
}

func (d *Daemon) sendNoticeToKeyWithFiles(ctx context.Context, w *worker, n protocol.WorkerNotice, report, to, replyTo, noticeKey string, final bool, files *OutgoingFiles) (json.RawMessage, error) {
	if final && d.rateOffer(ctx, w) {
		n.RateAsk = true
	}
	if len(report) > 48*1024 {
		report = strings.ToValidUTF8(report[:48*1024], "") + "\n[report cut at 48 KB]"
	}
	body := d.workerToolchainPermissions(w, protocol.FormatWorkerNoticeWithSettings(n, report, d.SettingsSnapshot()))
	if strings.Contains(n.DeliveryError, handbackUncommittedPushProblem) {
		body = strings.Replace(body, "\nstatus: "+n.Status+"\n", "\nstatus: "+n.Status+", NOT PUSHED: uncommitted changes in the worktree\n", 1)
	}
	raw, err := d.sendWorkerNoticeBodyWithFiles(ctx, w, body, to, replyTo, noticeKey, files, n.Status == "idle without hand-back")
	if err == nil {
		switch n.Status {
		case protocol.StatusDone, protocol.StatusFailed, protocol.StatusTimedOut, "partial", "idle without hand-back":
			d.wmu.Lock()
			if to == "" || to == w.owner {
				w.noticeSinceRequest = true
				d.saveWorkerLocked(w)
			}
			d.wmu.Unlock()
		}
	}
	return raw, err
}

// sendWorkerNoticeBody sends a worker notice through the same reply and outbox
// path used for completion notices.
func (d *Daemon) sendWorkerNoticeBody(ctx context.Context, w *worker, body, to, replyTo, noticeKey string) (json.RawMessage, error) {
	return d.sendWorkerNoticeBodyWithFiles(ctx, w, body, to, replyTo, noticeKey, nil, false)
}

func (d *Daemon) sendWorkerNoticeBodyWithFiles(ctx context.Context, w *worker, body, to, replyTo, noticeKey string, files *OutgoingFiles, idleNotice bool) (json.RawMessage, error) {
	d.wmu.Lock()
	sid, owner, parent := w.sessionID, w.owner, w.taskMsg
	d.wmu.Unlock()
	if to != "" {
		owner, parent = to, replyTo
	}
	if sid == "" {
		return nil, errors.New("the worker has no session")
	}
	if noticeKey == "" {
		noticeKey = protocol.NewID("k")
	}
	p := protocol.MsgSend{Sender: sid, Recipient: owner, Type: protocol.TypeNotification,
		Body: body, IdempotencyKey: noticeKey, WorkerIdleNotice: idleNotice, WorkerNotice: true}
	if parent != "" {
		p.Type, p.ParentID = protocol.TypeReply, parent
	}
	if files != nil {
		return d.sendWorkerNoticeFiles(ctx, FileSend{From: sid, To: owner, Body: body, Type: p.Type, ParentID: p.ParentID, IdempotencyKey: noticeKey, WorkerIdleNotice: idleNotice, WorkerNotice: true}, files)
	}
	return d.send(ctx, p)
}

// A stored notice must not be abandoned when the route lookup or a transfer
// fails. Reuse its key and checked snapshots; the receiver has its own bound
// and will deliver the report even if these attempts cannot finish.
func (d *Daemon) sendWorkerNoticeFiles(ctx context.Context, req FileSend, files *OutgoingFiles) (json.RawMessage, error) {
	transferCtx, cancel := context.WithTimeout(ctx, workerNoticeFileWait)
	defer cancel()
	var stored protocol.Message
	delay := 100 * time.Millisecond
	for {
		sent, err := d.sendPreparedFiles(transferCtx, req, files)
		if sent.Message.ID != "" {
			stored = sent.Message
		}
		if err == nil {
			return json.Marshal(protocol.MessageSent{Message: &sent.Message})
		}
		if stored.ID == "" {
			return nil, err
		}
		d.Logf("worker notice files: message=%s transfer failed, retrying: %v", stored.ID, err)
		timer := time.NewTimer(delay)
		select {
		case <-transferCtx.Done():
			timer.Stop()
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			d.Logf("worker notice files: message=%s transfer did not finish: %v", stored.ID, err)
			return json.Marshal(protocol.MessageSent{Message: &stored})
		case <-timer.C:
		}
		delay = min(delay*2, 5*time.Second)
	}
}

// prepareHandbackFiles limits workers to their own worktree, even when the
// project or /tmp would normally be a permitted source for a session.
func (d *Daemon) prepareHandbackFiles(w *worker, paths []string) (*OutgoingFiles, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	d.wmu.Lock()
	sid, root := w.sessionID, w.worktree
	d.wmu.Unlock()
	if root == "" {
		return nil, errors.New("a worker needs its own worktree to send files")
	}
	rules, err := d.fileRules(sid, false)
	if err != nil {
		return nil, err
	}
	rules.Roots, rules.Temp, rules.ConfineSymlinks = []string{root}, []string{}, true
	return PrepareFiles(paths, rules, filepath.Join(d.cfg.StateDir, fileOutgoingFolder))
}

// noticeFor maps why a worker ended to the status and note of its notice. An
// empty status means no notice (the owner is gone, or there was never a session).
func (d *Daemon) noticeFor(reason string) (status, note string) {
	set := d.workerSettings("")
	switch reason {
	case protocol.StopIdle:
		return protocol.StatusTimedOut, "no message to or from the worker for " + set.Idle.String()
	case protocol.StopMaxAge:
		return protocol.StatusTimedOut, "the worker ran for the longest time allowed (" + set.MaxAge.String() + ")"
	case protocol.StopExited:
		return protocol.StatusFailed, "the harness process ended by itself, with no hand-back"
	case protocol.StopModelFails:
		return protocol.StatusFailed, "the model call failed" // endWorker adds the error text
	case protocol.StopRunFailed:
		return protocol.StatusFailed, "the worker run failed"
	case protocol.StopRequested:
		return protocol.StatusStopped, "stopped on request, with no hand-back"
	case protocol.StopLost:
		return "lost after daemon restart", "the worker was lost while the daemon was not running"
	}
	return "", ""
}

// ---- hand-back ----

// handback serves "worker_handback" from a worker's session: the final report
// goes to the owner with the facts, and the worker is marked done.
func (d *Daemon) handback(ctx context.Context, sessionID string, req protocol.WorkerHandbackRequest) (*protocol.WorkerHandbackResult, error) {
	w := d.workerBySession(sessionID)
	if w == nil {
		return nil, errors.New("only a worker started by agent-net can hand back a report")
	}
	if w.harness == "agy" {
		files, err := d.prepareHandbackFiles(w, handbackPaths(req))
		if err != nil {
			return nil, err
		}
		out, err := d.deferAgyHandback(w, req, files)
		if err != nil {
			files.Remove()
		}
		return out, err
	}
	// The tool call returns as soon as the hand-back is recorded. A slow
	// check runs after the reply and its result comes with the notice.
	return d.handbackFlow(ctx, w, req, "", "", "", true, nil, true)
}

// handbackTo is handback to another session than the owner (see sendNoticeTo).
func (d *Daemon) handbackTo(ctx context.Context, w *worker, req protocol.WorkerHandbackRequest, to, replyTo string) (*protocol.WorkerHandbackResult, error) {
	return d.handbackToNote(ctx, w, req, to, replyTo, "", true)
}

func (d *Daemon) handbackToNote(ctx context.Context, w *worker, req protocol.WorkerHandbackRequest, to, replyTo, sandboxNote string, end bool) (*protocol.WorkerHandbackResult, error) {
	return d.handbackToNoteWithFiles(ctx, w, req, to, replyTo, sandboxNote, end, nil)
}

// An Antigravity handback carries its already checked snapshot until its
// result event. A recovered stream without one prepares it again.
func (d *Daemon) handbackToNoteWithFiles(ctx context.Context, w *worker, req protocol.WorkerHandbackRequest, to, replyTo, sandboxNote string, end bool, files *OutgoingFiles) (*protocol.WorkerHandbackResult, error) {
	return d.handbackFlow(ctx, w, req, to, replyTo, sandboxNote, end, files, false)
}

// handbackTail is what a hand-back carries from its record step to its notice.
type handbackTail struct {
	req                             protocol.WorkerHandbackRequest
	to, replyTo, sandboxNote        string
	end                             bool
	files                           *OutgoingFiles
	sessionID, committed, wipDetail string
	complete                        bool

	// persisted is true when the hand-back is saved in the state database
	// (handback_replay.go): head and noticeKey name it, and check and the
	// record fields carry what it already did.
	persisted                 bool
	head, noticeKey           string
	check                     *protocol.HandbackCheck
	recorded                  bool
	changeID, deliveryProblem string
	// stats is the worker's usage, read before a worker that ended at its
	// hand-back stopped (endAtHandback).
	stats *workerStats
}

// handbackFlow records a hand-back and sends its notice. With early set, a
// complete hand-back whose check has a command to run returns right after the
// record step: the check, the change and the notice follow in the background.
// Without it the call returns after the notice (workers that nobody waits on,
// such as Codex and Antigravity runs, use that).
func (d *Daemon) handbackFlow(ctx context.Context, w *worker, req protocol.WorkerHandbackRequest, to, replyTo, sandboxNote string, end bool, files *OutgoingFiles, early bool) (*protocol.WorkerHandbackResult, error) {
	d.wmu.Lock()
	sessionID, stopping := w.sessionID, w.stopping
	d.wmu.Unlock()
	if stopping {
		return nil, errors.New("the worker is ending")
	}
	if req.Status == "" {
		req.Status = protocol.StatusDone
	}
	if !protocol.ValidHandbackStatus(req.Status) {
		return nil, fmt.Errorf("status must be done, blocked or failed, not %q", req.Status)
	}
	if strings.TrimSpace(req.Report) == "" {
		return nil, errors.New("the report is empty: say what you did, or what blocks you")
	}
	handedOver := false
	if files == nil {
		var err error
		files, err = d.prepareHandbackFiles(w, handbackPaths(req))
		if err != nil {
			return nil, err
		}
		// The background finish takes over the snapshots and removes them.
		defer func() {
			if !handedOver {
				files.Remove()
			}
		}()
	}
	// A report that ends with "complete: no" is partial, whatever the harness
	// and even when the hand-back has no complete field (Codex reports are
	// read the same way). An explicit field wins.
	if req.Status == protocol.StatusDone && req.Complete == nil {
		if v, ok := protocol.WorkerReportComplete(req.Report); ok {
			req.Complete = &v
		}
	}
	// Carry the transfer manifest with each frame so names and hashes survive
	// the worker's worktree. The original path stays useful to a local reviewer.
	for i := range req.VisualProof {
		if files != nil {
			for j, f := range files.Files {
				if f.Source == req.VisualProof[i].File {
					files.Files[j].Entry.VisualProof = true
					entry := files.Files[j].Entry
					req.VisualProof[i].Manifest = &entry
					break
				}
			}
		}
	}
	// A partial hand-back is a worker that stopped early. Send it one more
	// message ourselves, up to the limit, and do not tell the caller yet.
	if req.Status == protocol.StatusDone && !req.IsComplete() && d.tryAutoContinue(ctx, w) {
		d.Logf("worker.handback worker=%s session=%s status=%s complete=false auto_continue=1", w.id, sessionID, req.Status)
		return &protocol.WorkerHandbackResult{Status: req.Status, Complete: false, AutoContinued: true}, nil
	}
	// Keep the explicit inability report for every ended run, including blocked
	// workers and workers without a delivery change. It survives daemon restarts.
	d.wmu.Lock()
	workerID, generation := w.id, w.runGeneration
	d.wmu.Unlock()
	if _, err := d.st.db.Exec(`INSERT INTO worker_visual_reports(worker_id,generation,reason) VALUES(?,?,?) ON CONFLICT(worker_id,generation) DO UPDATE SET reason=excluded.reason`, workerID, generation, req.ProofUnavailable); err != nil {
		d.Logf("worker visual report: %v", err)
	}
	complete := req.Status == protocol.StatusDone && req.IsComplete()
	// Read the run's own trace once, before the harness may be stopped, so
	// the notice and the delivery record carry the same counts.
	d.noteWorkerStruggle(ctx, w)
	d.captureWorkerEvidence(w, &complete)
	committed := d.commitAtHandback(w, req)
	wipDetail := ""
	if !complete && w.worktree != "" && w.harness != "agy" {
		unlock := d.lockWorktree(w.worktree)
		result := d.commitWorkerEndWIP(context.Background(), w.worktree, w.branch, "handback")
		unlock()
		wipDetail = result.noticeDetail()
		if result.Failed != "" {
			d.Logf("worker %s: end WIP commit: %s", w.id, result.Failed)
		}
		if result.Skipped == "git operation in progress" {
			d.wmu.Lock()
			w.leftAsIs = true
			d.wmu.Unlock()
		}
	}
	// A complete hand-back is a revision of a delivery change (delivery
	// pipeline build step 1). A partial one records nothing. The project's
	// pre-hand-back check runs after the commit and before the change is
	// recorded, so the change carries its result (owner decision 2026-11-12).
	t := &handbackTail{req: req, to: to, replyTo: replyTo, sandboxNote: sandboxNote, end: end, files: files,
		sessionID: sessionID, committed: committed, wipDetail: wipDetail, complete: complete}
	plan := handbackCheckPlan{}
	if complete {
		plan = d.planHandbackCheck(ctx, w)
	}
	if early && plan.runs() {
		out, started, err := d.startHandbackCheck(ctx, w, t, plan)
		handedOver = started
		return out, err
	}
	out, err := d.finishHandback(ctx, w, t, plan, nil)
	// A saved hand-back keeps its snapshots for the next connect.
	handedOver = errors.Is(err, errHandbackDeferred)
	return out, err
}

// handbackCheckRun is one running check of a worker. A newer hand-back on a
// new head cancels it and takes its place. The daemon keeps it by worker ID,
// not on the worker: the worker ends at its hand-back, and a later run of the
// same worker (a follow-up message) must still find and replace it.
type handbackCheckRun struct {
	ctx       context.Context
	cancel    context.CancelFunc
	head      string
	sessionID string
	// running is false while the check waits for the machine's test slot.
	running atomic.Bool
}

// checkState is what a worker list shows for the run.
func (r *handbackCheckRun) checkState() string {
	if r.running.Load() {
		return protocol.HandbackCheckRunning
	}
	return protocol.HandbackCheckQueued
}

// startHandbackCheck answers the worker at once and ends it as done: its run
// is over, so it frees its worker slot and shows its end while the check
// waits for the machine's test slot. The check, the change and the notice
// follow in the background. A check that already runs on the same head is not
// started again; a new head cancels the old check first. The second result
// says whether the background run took over the snapshot files.
func (d *Daemon) startHandbackCheck(ctx context.Context, w *worker, t *handbackTail, plan handbackCheckPlan) (*protocol.WorkerHandbackResult, bool, error) {
	out := &protocol.WorkerHandbackResult{Status: t.req.Status, Complete: true, Checking: true,
		Note: "handed back; you end now, and the check runs on its own: its result comes in the notice to the caller"}
	d.wmu.Lock()
	// A worker with queued messages runs them after its notice (see
	// finishHandback), so it keeps running until then.
	endNow := len(w.turnQueue) == 0 && w.codex == nil
	if old := d.checkRuns[w.id]; old != nil {
		if old.head == plan.head {
			d.wmu.Unlock()
			d.Logf("worker.handback worker=%s head=%s already_checking=1", w.id, plan.head)
			out.Note = "already handed back; check running"
			if endNow {
				d.goBackground(func() { d.endAtHandback(w, t) })
			}
			return out, false, nil
		}
		old.cancel()
	}
	// The run lasts until its notice is sent, not until the request ends.
	baseCtx := context.WithoutCancel(ctx)
	runCtx, cancel := context.WithCancel(baseCtx)
	run := &handbackCheckRun{ctx: runCtx, cancel: cancel, head: plan.head, sessionID: w.sessionID}
	if d.checkRuns == nil {
		d.checkRuns = map[string]*handbackCheckRun{}
	}
	d.checkRuns[w.id] = run
	// Mark the check: the tick must not end the worker by age or idleness
	// before the end below begins.
	w.checking = true
	d.wmu.Unlock()
	if !endNow {
		out.Note = "handed back; the check runs now and its result comes in the notice to the caller"
	}
	release := func(held bool) {
		d.wmu.Lock()
		if d.checkRuns[w.id] == run {
			delete(d.checkRuns, w.id)
			w.checking = held
		}
		d.wmu.Unlock()
	}
	stopping := func() (*protocol.WorkerHandbackResult, bool, error) {
		cancel()
		release(false)
		return nil, false, errDaemonStopping
	}
	if !d.holdCheck() {
		return stopping()
	}
	// Saved before the reply: the worker is told it can end, so from now on
	// only this row carries the hand-back across a restart.
	d.persistHandback(w, t, plan.head)
	started := d.goBackground(func() {
		defer d.checks.Done()
		defer cancel()
		if endNow {
			d.endAtHandback(w, t)
		}
		// Only the check stops when a newer hand-back replaces this one; the
		// change and the notice use a context that stays open.
		_, err := d.finishHandback(baseCtx, w, t, plan, run)
		deferred := errors.Is(err, errHandbackDeferred)
		if err != nil && !deferred && !errors.Is(err, errHandbackSuperseded) {
			d.Logf("worker %s: hand-back after the reply: %v", w.id, err)
		}
		if !deferred {
			t.files.Remove()
		}
		// The run stays registered until its notice is sent, so a repeated
		// hand-back on this head never starts a second check. A saved
		// hand-back of a worker that still runs owns its end: no nudge and no
		// idle end.
		release(deferred)
		// The ended worker's row in a list keeps its end for a while after
		// the notice, as for any other end.
		d.touchRecent(run.sessionID, time.Now())
	})
	if !started {
		d.checks.Done()
		if t.persisted {
			d.dropHandback(w.id, t.head)
		}
		return stopping()
	}
	return out, true, nil
}

// endAtHandback ends a worker that handed back complete, as done, before its
// check runs. Its usage is read first, while the harness still runs, so the
// notice carries it.
func (d *Daemon) endAtHandback(w *worker, t *handbackTail) {
	st := d.workerStats(w)
	t.stats = &st
	if t.persisted {
		if err := d.saveHandback(w.id, t); err != nil {
			d.Logf("worker %s: save the hand-back: %v", w.id, err)
		}
	}
	d.wmu.Lock()
	w.doneStatus, w.donePartial, w.handbackReport = t.req.Status, false, t.req.Report
	w.pendingHandback, w.pendingPartial = true, false
	// The hand-back answers the last request; the notice only adds the check.
	w.noticeSinceRequest = true
	d.saveWorkerLocked(w)
	d.wmu.Unlock()
	d.Logf("worker.handback worker=%s session=%s status=%s complete=true ended_before_check=1", w.id, t.sessionID, t.req.Status)
	d.endWorker(w, protocol.StopHandback)
}

// errDaemonStopping refuses a hand-back that arrives while the daemon stops:
// its check could not run, and the worker can hand back again to the next
// daemon.
var errDaemonStopping = errors.New("the agent-net daemon is stopping; hand back again in a minute")

// errHandbackSuperseded ends a background hand-back whose check a newer
// hand-back cancelled. The newer one sends the notice.
var errHandbackSuperseded = errors.New("a newer hand-back replaced this check")

// finishHandback runs the check, records the change and sends the notice. run
// is nil when the caller waits for it.
func (d *Daemon) finishHandback(ctx context.Context, w *worker, t *handbackTail, plan handbackCheckPlan, run *handbackCheckRun) (*protocol.WorkerHandbackResult, error) {
	req, to, replyTo, sandboxNote, end, files := t.req, t.to, t.replyTo, t.sandboxNote, t.end, t.files
	sessionID, committed, wipDetail := t.sessionID, t.committed, t.wipDetail
	changeID, deliveryProblem := "", ""
	handbackCheck := plan.result
	if t.complete {
		if plan.runs() {
			if run == nil {
				if d.holdCheck() {
					defer d.checks.Done()
				}
				d.wmu.Lock()
				w.checking = true
				d.wmu.Unlock()
				d.persistHandback(w, t, plan.head)
			}
			runCtx := context.WithoutCancel(ctx)
			if run != nil {
				runCtx = run.ctx
			}
			handbackCheck = d.runHandbackCheckPlan(runCtx, plan, run)
			d.wmu.Lock()
			superseded := run != nil && d.checkRuns[w.id] != run
			if !superseded && run == nil {
				w.checking = false
			}
			d.wmu.Unlock()
			if superseded {
				return nil, errHandbackSuperseded
			}
		}
		if t.persisted && d.checksStopped() {
			// The gateway may be down too (on its own machine the installer
			// restarts it first). The next daemon records and announces.
			return nil, d.deferHandback(w, t, handbackCheck, "the daemon is stopping")
		}
		if t.recorded {
			changeID, deliveryProblem = t.changeID, t.deliveryProblem
		} else {
			var offline bool
			changeID, deliveryProblem, offline = d.submitHandbackChange(ctx, w, handbackCheck, req.VisualEvidence)
			if offline && t.persisted {
				return nil, d.deferHandback(w, t, handbackCheck, deliveryProblem)
			}
			t.recorded, t.changeID, t.deliveryProblem = true, changeID, deliveryProblem
		}
	}
	var st workerStats
	if t.stats != nil {
		st = *t.stats
	} else {
		st = d.workerStats(w)
	}
	d.wmu.Lock()
	pending, cc := len(w.turnQueue) > 0, w.codex
	d.wmu.Unlock()
	if cc != nil {
		cc.mu.Lock()
		pending = pending || len(cc.queue) > 0
		cc.mu.Unlock()
	}
	note := "the worker ended after its hand-back; a message from you starts it again"
	if w.worktree != "" {
		note += " while its worktree and branch exist"
	} else {
		note += fmt.Sprintf(" until %s after it started", d.workerSettings("").MaxAge)
	}
	if pending {
		note = "the worker will run its queued messages before ending"
	} else if _, canResume := d.launchers[w.harness].(resumer); !canResume {
		note = "the worker ended after its hand-back; this harness cannot be started again (" + cannotResumeReason(w.harness) + ")"
	}
	if sandboxNote != "" {
		note = sandboxNote + "; " + note
	}
	n := d.notice(w, req.Status, note, st)
	n.VisualEvidence = req.VisualEvidence
	n.Committed = committed
	n.HandbackCheck = handbackCheck
	n.WIPDetail = wipDetail
	n.Change, n.DeliveryError = changeID, deliveryProblem
	if changeID != "" {
		n.DeliveryBranch = d.deliveredBranch(ctx, w)
	}
	n.DefectHint = d.defectHint(ctx, w)
	if misplaced := d.misplacedCommitNote(w); misplaced != "" {
		note := strings.TrimSpace(n.Note)
		if note != "" && !strings.HasSuffix(note, ".") {
			note += "."
		}
		n.Note = strings.TrimSpace(note + " " + misplaced)
	}
	noticeKey := t.noticeKey
	if w.harness == "agy" {
		d.wmu.Lock()
		c := w.agy
		d.wmu.Unlock()
		if c != nil {
			c.mu.Lock()
			noticeKey = c.pendingKey
			c.mu.Unlock()
		}
	}
	// The notice goes out before the worker is ended, and its text says that
	// a message from the owner starts the worker again. Mark the hand-back
	// first, so a continue that arrives with the notice waits for the end
	// instead of being refused as "still running" (see continueWorker).
	d.wmu.Lock()
	w.handingBack = true
	d.wmu.Unlock()
	raw, err := d.sendNoticeToKeyWithFiles(ctx, w, n, req.Report, to, replyTo, noticeKey, true, files)
	if err != nil {
		d.wmu.Lock()
		w.handingBack = false
		d.wmu.Unlock()
		// A notice with files needs the link; one without goes to the outbox.
		if t.persisted && !d.Connected() {
			return nil, d.deferHandback(w, t, handbackCheck, err.Error())
		}
		if t.persisted {
			d.dropHandback(w.id, t.head)
		}
		return nil, err
	}
	if t.persisted {
		d.dropHandback(w.id, t.head)
	}
	d.wmu.Lock()
	var next *protocol.Message
	if (w.harness == "opencode" || w.harness == "agy") && len(w.turnQueue) > 0 {
		w.turnActive = true
		m := w.turnQueue[0]
		w.turnQueue = w.turnQueue[1:]
		w.turnHandbackIdle = w.harness == "opencode"
		w.turnAwaitingStart = true
		w.turnMessageID = m.ID
		next = &m
		w.handingBack = false
	} else {
		w.doneStatus = req.Status
		w.donePartial = !req.IsComplete()
		w.handbackReport = req.Report
		w.pendingHandback = true
		w.pendingPartial = !req.IsComplete()
		if !end {
			w.handingBack = false
		}
		d.saveWorkerLocked(w)
	}
	d.wmu.Unlock()
	d.Logf("worker.handback worker=%s session=%s status=%s complete=%t", w.id, sessionID, req.Status, req.IsComplete())
	// Like a Claude Code subagent, a worker ends when it hands back (W10). Its
	// harness session is kept, so the owner's next message starts it again.
	if next != nil {
		d.goBackground(func() { d.deliverQueuedWorkerTurn(*next) })
	} else if end {
		d.goBackground(func() { d.endWorker(w, protocol.StopHandback) })
	}
	out := &protocol.WorkerHandbackResult{Status: req.Status, Complete: req.IsComplete()}
	var sent protocol.MessageSent
	if json.Unmarshal(raw, &sent) == nil {
		out.Sent, out.Queued = sent.Message, sent.Queued
	}
	return out, nil
}

// commitAtHandback commits the worker's leftover changes at a hand-back, so
// the hand-back names a real commit (delivery pipeline, decision 8). It is the
// one path for every harness: a complete hand-back (status done, complete
// yes) is committed with the worker's title as the subject and the usual
// trailers. A partial one is handled by the ordered worker-end WIP commit.
// Files matched by .gitignore are not added, and nothing is forced. A merge or
// rebase in progress leaves the worktree as it is (saveWorkerChanges says so
// in the notice). It returns the short hash of the commit it made, or "".
func (d *Daemon) commitAtHandback(w *worker, req protocol.WorkerHandbackRequest) string {
	complete := req.Status == protocol.StatusDone && req.IsComplete()
	// The merge line is about this hand-back, not an earlier one.
	d.wmu.Lock()
	w.leftAsIs = false
	d.wmu.Unlock()
	if !complete && w.harness != "agy" {
		return ""
	}
	d.wmu.Lock()
	subject := strings.TrimSpace(strings.SplitN(w.title, "\n", 2)[0])
	d.wmu.Unlock()
	if subject == "" {
		subject = strings.TrimSpace(strings.SplitN(req.Report, "\n", 2)[0])
	}
	if !d.saveWorkerChanges(w, subject, "handback", !complete) || !complete {
		return ""
	}
	return d.worktreeHead(w)
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

// noteAsk records a message from a worker's owner and counts a follow-up after
// a hand-back. A hand-back answers the last request; messages from the owner
// after it are continues when it said complete:no, and fixes when it said
// complete:yes. A worker that went idle without a hand-back counts a continue
// too: its follow-up is the owner telling it to carry on. Only the owner
// counts: a notice, or another session, does not.
func (d *Daemon) noteAsk(m protocol.Message) {
	w := d.workerBySession(m.To)
	if w == nil || (m.From != w.owner && !(m.From == "agent-net" && protocol.IsWorkerTask(m))) {
		return
	}
	isAsk := m.Type == protocol.TypeRequest
	if !isAsk && m.Type != protocol.TypeReply {
		return
	}
	d.wmu.Lock()
	counted := false
	if w.pendingHandback || w.idleNoticeSent || w.idleFollowUp {
		// Every message from the owner after a hand-back counts until the
		// worker hands back again. An idle notice with no hand-back is the
		// same signal: the worker stopped early without saying so, so the
		// owner's follow-up is a continue. A notice or another session does
		// not count.
		cont, fix := protocol.ReworkCounts(w.pendingHandback, w.pendingPartial, w.idleNoticeSent || w.idleFollowUp)
		w.continueCount += cont
		w.continueRun += cont
		w.fixCount += fix
		w.fixRun += fix
		w.idleFollowUp = false
		counted = true
	}
	var updated *protocol.WorkerInfo
	if isAsk {
		w.taskMsg = m.ID
		// The task in a list is the title: the first line of the FIRST task. A
		// later request from the owner must not replace it. Only an older worker
		// with no title yet takes the line of its first message.
		if task := protocol.WorkerTaskSummary(m.Body); task != "" && w.taskText == "" {
			w.taskText = task
		}
	}
	if counted || (w.info != nil && isAsk) {
		if w.info == nil {
			w.info = &protocol.WorkerInfo{ID: w.id, Owner: w.owner, Project: w.project, StartedAt: w.startedAt.Unix(),
				Permissions: protocol.WorkerPermissionsOrDefault(w.permissions)}
		}
		// Replace, never change in place: a response on another goroutine may
		// still be writing the old value.
		cp := *w.info
		cp.Task = w.taskText
		cp.Continue, cp.Fix = w.continueCount, w.fixCount
		cp.AutoContinue = w.autoContinueCount
		cp.DifficultyAsked, cp.DifficultyUsed = w.difficultyAsked, w.difficultyUsed
		cp.Touches = splitTouches(w.touches)
		w.info = &cp
		// The session gets its own copy, so a later change to w.info never
		// touches a value that pushSession may be encoding.
		snap := cp
		updated = &snap
	}
	d.saveWorkerLocked(w)
	sessionID := w.sessionID
	d.wmu.Unlock()
	if updated != nil {
		if ls := d.setSessionWorker(sessionID, updated); ls != nil {
			// Tell the gateway now: the row name follows the task, and the
			// session may not report again soon.
			go d.pushSession(ls)
		}
	}
}

// noteMidrun counts on arrival, before an OpenCode prompt can wait in its
// turn queue. noteAsk still counts the existing rework when it is delivered.
func (d *Daemon) noteMidrun(w *worker, m protocol.Message) bool {
	d.wmu.Lock()
	defer d.wmu.Unlock()
	if d.workers[w.id] != w || w.stopping {
		return false
	}
	// Queued turns remain dispatched until the plugin confirms them. A
	// repeated gateway push must not count that same request twice, including
	// the gap between dequeueing it and claiming its harness write.
	if w.turnAwaitingStart && w.turnMessageID == m.ID {
		return true
	}
	for _, queued := range w.turnQueue {
		if queued.ID == m.ID {
			return true
		}
	}
	if m.Type == protocol.TypeRequest && m.From == w.owner {
		if w.midrunSeen && !w.noticeSinceRequest {
			w.midrunCount++
			w.midrunRun++
		}
		w.midrunSeen = true
		w.noticeSinceRequest = false
		d.saveWorkerLocked(w)
	}
	return true
}

// setSessionWorker gives the worker's local session a new worker value and
// returns it, or nil. The value is never changed in place, so a reader may
// keep the old one.
func (d *Daemon) setSessionWorker(sessionID string, info *protocol.WorkerInfo) *localSession {
	if sessionID == "" {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, ls := range d.sessions {
		if ls.sessionID == sessionID && ls.info.Worker != nil {
			ls.info.Worker = info
			return ls
		}
	}
	return nil
}

// ---- a worker that was lost while the daemon was down ----

// lostNotice sends the notice of a worker found dead at start, from its row.
func (d *Daemon) lostNotice(ctx context.Context, r workerRow) {
	if r.DoneStatus != "" || r.SessionID == "" {
		return
	}
	w := &worker{evidence: r.Evidence, id: r.ID, harness: r.Harness, project: r.Project, path: r.Path, model: r.Model, owner: r.Owner, ownerName: r.OwnerName,
		sessionID: r.SessionID, startedAt: r.StartedAt, taskMsg: r.TaskMsg, mainPath: r.MainPath, worktree: r.Worktree,
		branch: r.Branch, base: r.Base, title: r.Title}
	state, err := d.st.loadWorkerLostNotice(r.ID, r.RunGeneration)
	if err != nil {
		d.Logf("worker %s: lost notice state: %v", r.ID, err)
	}
	status, note := d.noticeFor(protocol.StopLost)
	d.noteWorkerStruggle(ctx, w)
	unlock := d.lockWorktree(w.worktree)
	result := d.commitWorkerEndWIP(ctx, w.worktree, w.branch, protocol.StopLost)
	unlock()
	if result.Failed != "" {
		d.Logf("worker %s: end WIP commit: %s", r.ID, result.Failed)
	}
	if result.SHA == "" && w.worktree != "" {
		if subject, err := gitOut(ctx, w.worktree, "log", "-1", "--format=%s"); err == nil {
			switch subject {
			case unfinishedSubject(w.id, protocol.StopLost):
				w.unfinished = true
			case "WIP: uncommitted changes at worker end (lost)":
				sha, _ := gitOut(ctx, w.worktree, "rev-parse", "--short", "HEAD")
				result = workerEndCommitResult{SHA: sha, Files: workerCommitFileCount(ctx, w.worktree, "HEAD")}
			default:
				if w.harness == "agy" && strings.TrimSpace(subject) == strings.TrimSpace(w.title) {
					w.unfinished = true
				}
			}
		}
	}
	// One lost run tells its caller once, however often the gateway report is
	// retried. A WIP save that succeeds after the notice is a short follow-up.
	if !state.Sent {
		n := d.notice(w, status, note, workerStats{})
		n.WIPDetail = result.noticeDetail()
		if _, err := d.sendNotice(ctx, w, n, ""); err != nil {
			d.Logf("worker %s: notice: %v", r.ID, err)
			d.finishWorktree(w)
			return
		}
		state.Sent = true
		if result.SHA != "" {
			state.WIPSHA = result.SHA
		}
		if err := d.st.saveWorkerLostNotice(r.ID, r.RunGeneration, state); err != nil {
			d.Logf("worker %s: save lost notice state: %v", r.ID, err)
		}
		d.finishWorktree(w)
		return
	}
	if result.SHA != "" && !state.WIPSent && result.SHA != state.WIPSHA {
		if _, err := d.sendWorkerNoticeBody(ctx, w, "WIP saved as "+result.SHA, "", "", ""); err != nil {
			d.Logf("worker %s: WIP saved notice: %v", r.ID, err)
		} else {
			state.WIPSHA, state.WIPSent = result.SHA, true
			if err := d.st.saveWorkerLostNotice(r.ID, r.RunGeneration, state); err != nil {
				d.Logf("worker %s: save lost notice state: %v", r.ID, err)
			}
		}
	}
	d.finishWorktree(w)
}

// worktreesDir is the folder where this daemon creates the git worktrees of
// its workers, as a cleaned absolute path.
func (d *Daemon) worktreesDir() string {
	root, err := filepath.Abs(filepath.Join(d.cfg.StateDir, "worktrees"))
	if err != nil {
		return ""
	}
	return root
}

// dropWorktreeSessions closes the plugin connections of live sessions that
// work inside the worktrees folder and are not a worker's own. They are ghosts
// of workers that a user's own service registered before the hello refused
// them. Closing the connection takes the session offline.
func (d *Daemon) dropWorktreeSessions() {
	var drop []*localConn
	d.mu.Lock()
	for _, ls := range d.sessions {
		if ls.conn != nil && !ls.owned && ls.info.Worker == nil && d.inWorktreesDir(ls.info.Cwd) {
			drop = append(drop, ls.conn)
		}
	}
	d.mu.Unlock()
	for _, c := range drop {
		c.close()
	}
}

// inWorktreesDir tells whether cwd is inside the folder where this daemon
// creates the git worktrees of its workers. It compares cleaned absolute paths
// and works for a folder that no longer exists.
func (d *Daemon) inWorktreesDir(cwd string) bool {
	root := d.worktreesDir()
	if cwd == "" || root == "" {
		return false
	}
	p, err := filepath.Abs(cwd)
	if err != nil {
		return false
	}
	return p == root || strings.HasPrefix(p, root+string(filepath.Separator))
}

// queueOpenCodeTurn keeps later prompts in the daemon until the current turn
// hands back or becomes idle. This keeps them out of a process being ended.
func (d *Daemon) queueOpenCodeTurn(w *worker, m protocol.Message) (queued, retry bool) {
	d.wmu.Lock()
	defer d.wmu.Unlock()
	if d.workers[w.id] != w || w.stopping || w.doneStatus != "" {
		return false, true
	}
	if w.harness != "opencode" && w.harness != "agy" {
		return false, false
	}
	if w.turnAwaitingStart && w.turnMessageID == m.ID {
		return true, false
	}
	if !w.turnActive {
		if w.idleNoticeCancel != nil {
			w.idleNoticeCancel()
			w.idleNoticeCancel = nil
		}
		w.idleFollowUp = w.idleNoticeSent && m.From == w.owner
		w.idleNoticeSince = time.Time{}
		w.idleNoticeSent = false
		w.idleNoticePending = false
		w.idleNoticeGeneration++
		w.idleNoticeReport = ""
		w.idleNoticeKey = ""
		if err := d.st.deleteWorkerIdleNotice(w.id); err != nil {
			d.Logf("worker %s: reset idle notice: %v", w.id, err)
		}
		w.turnActive = true
		return false, false
	}
	for _, queued := range w.turnQueue {
		if queued.ID == m.ID {
			return true, false
		}
	}
	w.turnQueue = append(w.turnQueue, m)
	return true, false
}

func (d *Daemon) deliverQueuedWorkerTurn(m protocol.Message) {
	ctx := context.Background()
	// A hand-back can drain the queue before onDeliver has saved dispatched.
	// Wait for that push to finish before claiming the actual harness write.
	for {
		d.mu.Lock()
		done := d.inflight[m.ID]
		d.mu.Unlock()
		if done == nil {
			break
		}
		<-done
	}
	if !d.startDelivery(ctx, m) {
		return
	}
	defer d.releaseDelivery(m.ID)
	ls := d.sessionByID(m.To)
	if ls == nil {
		d.finishDelivery(ctx, m, Result{Detail: "worker session is gone"})
		return
	}
	d.noteAsk(m)
	res := d.deliverTo(context.Background(), ls, m)
	d.finishDelivery(ctx, m, res)
}

// An OpenCode turn that did not hand back can finish normally. Let the next
// queued prompt run, or mark the worker ready for the next message.
func (d *Daemon) openCodeTurnIdle(session string) bool {
	d.wmu.Lock()
	var next *protocol.Message
	pending := false
	for _, w := range d.workers {
		if w.sessionID != session || (w.harness != "opencode" && w.harness != "agy") || w.stopping || w.doneStatus != "" {
			continue
		}
		if w.turnHandbackIdle {
			w.turnHandbackIdle = false
			pending = true
			break
		}
		if w.turnAwaitingStart {
			pending = true
			break
		}
		if len(w.turnQueue) == 0 {
			w.turnActive = false
		} else {
			m := w.turnQueue[0]
			w.turnQueue = w.turnQueue[1:]
			w.turnAwaitingStart = true
			w.turnMessageID = m.ID
			pending = true
			next = &m
		}
		break
	}
	d.wmu.Unlock()
	if next != nil {
		d.goBackground(func() { d.deliverQueuedWorkerTurn(*next) })
	}
	return pending
}

// stageWorkerLeftovers filters both staged and unstaged paths before adding them.
func stageWorkerLeftovers(ctx context.Context, dir string) (string, error) {
	paths, err := gitOut(ctx, dir, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return "", err
	}
	var excluded []string
	seen := map[string]bool{}
	for _, path := range strings.Split(paths, "\x00") {
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		info, err := os.Lstat(filepath.Join(dir, path))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		skip := path == "agent-net" || strings.HasSuffix(path, ".test") || strings.HasSuffix(path, ".exe") || info.Size() > 5_000_000
		if info.Mode().IsRegular() && !skip {
			f, err := os.Open(filepath.Join(dir, path))
			if err != nil {
				return "", err
			}
			buf := make([]byte, 8192)
			n, _ := f.Read(buf)
			f.Close()
			buf = buf[:n]
			magic := string(buf)
			skip = strings.HasPrefix(magic, "\x7fELF") || strings.HasPrefix(magic, "MZ")
			if len(buf) >= 4 {
				switch string(buf[:4]) {
				case "\xfe\xed\xfa\xce", "\xce\xfa\xed\xfe", "\xfe\xed\xfa\xcf", "\xcf\xfa\xed\xfe", "\xca\xfe\xba\xbe", "\xbe\xba\xfe\xca":
					skip = true
				}
			}
			skip = skip || bytes.IndexByte(buf, 0) >= 0
		}
		if skip {
			// Reset also removes a file the worker had already staged.
			if _, err := gitOut(ctx, dir, "reset", "-q", "HEAD", "--", path); err != nil {
				return "", err
			}
			excluded = append(excluded, fmt.Sprintf("%s %.0f MB", path, float64(info.Size())/1_000_000))
			continue
		}
		if _, err := gitOut(ctx, dir, "add", "-A", "--", path); err != nil {
			return "", err
		}
	}
	// Include deletions without staging excluded files again.
	if _, err := gitOut(ctx, dir, "add", "-u", "--"); err != nil {
		return "", err
	}
	// add -u can restage tracked excluded paths, so reset them again.
	for path := range seen {
		for _, entry := range excluded {
			if strings.HasPrefix(entry, path+" ") {
				if _, err := gitOut(ctx, dir, "reset", "-q", "HEAD", "--", path); err != nil {
					return "", err
				}
				break
			}
		}
	}
	return strings.Join(excluded, ", "), nil
}

// handbackPaths uses the same transfer and confinement rules for proof frames.
func handbackPaths(req protocol.WorkerHandbackRequest) []string {
	paths := append([]string(nil), req.Files...)
	for _, proof := range req.VisualProof {
		found := false
		for _, p := range paths {
			if p == proof.File {
				found = true
				break
			}
		}
		if !found {
			paths = append(paths, proof.File)
		}
	}
	return paths
}

// apiCostText is the run's cost at API prices for the notice, asked of the
// gateway, which keeps the price list (decision 0161). It is empty when the
// harness reported no usage, the gateway has no price list, or it cannot be
// reached in two seconds: the notice never waits longer for a cost.
func (d *Daemon) apiCostText(harness, model string, usage protocol.RunTokens) string {
	if usage.Zero() || model == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	raw, err := d.call(ctx, "worker.cost", protocol.WorkerCostQuery{Model: protocol.PickerModelKey(harness, model), Tokens: usage})
	if err != nil {
		return ""
	}
	var r protocol.WorkerCostReply
	if json.Unmarshal(raw, &r) != nil || !r.Known {
		return ""
	}
	return protocol.APICostText(r.Cost, r.PriceKnown)
}

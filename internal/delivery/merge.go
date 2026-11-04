package delivery

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// This file is the git side of the merge step. It merges exactly the commit
// whose evidence was checked, in a dedicated worktree, and refuses when
// anything moved. It does not decide whether a merge is allowed: the caller
// checks the evidence first.

// Merge modes.
const (
	MergeModeFFOnly = "ff-only"
	MergeModeCommit = "merge-commit"
)

// DefaultMergeTimeout bounds one Merge call when the request sets no Timeout.
const DefaultMergeTimeout = 5 * time.Minute

// Errors returned by Merge. Test them with errors.Is.
var (
	// ErrHeadMoved: the change branch no longer points at HeadSHA.
	ErrHeadMoved = errors.New("change branch moved since the evidence was checked")
	// ErrChangeBranchMissing: the change branch exists neither locally nor on the remote.
	ErrChangeBranchMissing = errors.New("change branch not found")
	// ErrDirtyWorktree: the merge worktree has local changes.
	ErrDirtyWorktree = errors.New("merge worktree has local changes")
	// ErrNotFastForward: ff-only mode, and HeadSHA does not contain the target tip.
	ErrNotFastForward = errors.New("head is not a fast-forward of the target branch")
	// ErrConflict: merge-commit mode, and the merge conflicts. The worktree is left clean.
	ErrConflict = errors.New("merge conflict")
	// ErrRemoteMoved: the remote target branch moved before the push. Nothing was forced.
	ErrRemoteMoved = errors.New("remote target branch moved before the push")
)

// MergeIdentity is the author and committer of a merge commit.
type MergeIdentity struct {
	Name  string
	Email string
}

// MergeRequest says what to merge and where.
type MergeRequest struct {
	// RepoDir is a checkout of the repository. Its branches and files are
	// never changed: the merge runs in WorktreeDir. Only remote-tracking refs
	// in RepoDir are updated by the fetch.
	RepoDir string
	// WorktreeDir is the dedicated merge worktree. It is created when missing
	// and reset to the remote tip when it exists and is clean. It is kept on a
	// detached HEAD, so it never clashes with a checkout of TargetBranch in
	// RepoDir.
	WorktreeDir string
	// Remote is the name of the remote, for example "origin".
	Remote string
	// TargetBranch is the branch to merge into, for example "main".
	TargetBranch string
	// ChangeBranch is the branch that holds the change. It must still point
	// at HeadSHA, locally or on the remote.
	ChangeBranch string
	// HeadSHA is the full commit ID whose evidence was checked.
	HeadSHA string
	// Mode is MergeModeFFOnly or MergeModeCommit.
	Mode string
	// Message is the merge commit message (merge-commit mode). When empty, a
	// default is used.
	Message string
	// Identity is required in merge-commit mode.
	Identity MergeIdentity
	// Push pushes TargetBranch to Remote, never with force.
	Push bool
	// Timeout bounds the whole call. Zero means DefaultMergeTimeout.
	Timeout time.Duration
}

// MergeResult says what Merge did.
type MergeResult struct {
	// MergedSHA is the commit that holds the change on the target branch:
	// HeadSHA for a fast-forward, the merge commit otherwise.
	MergedSHA string
	// TargetBefore is the remote target tip the merge started from.
	TargetBefore string
	// TargetAfter is the target tip after the merge (equal to TargetBefore
	// when AlreadyMerged).
	TargetAfter string
	// FastForward is true when the target was fast-forwarded to HeadSHA.
	FastForward bool
	// Pushed is true when this call pushed the target branch.
	Pushed bool
	// AlreadyMerged is true when HeadSHA was already part of the remote
	// target branch. Nothing was changed.
	AlreadyMerged bool
}

var mergeFullSHARe = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

// mergeRunner carries the test seam. The zero value is the real thing.
type mergeRunner struct {
	// beforePush runs after the local merge and before the push.
	beforePush func()
}

// Merge merges req.HeadSHA into req.TargetBranch in req.WorktreeDir.
func Merge(ctx context.Context, req MergeRequest) (MergeResult, error) {
	return (&mergeRunner{}).merge(ctx, req)
}

func (m *mergeRunner) merge(ctx context.Context, req MergeRequest) (MergeResult, error) {
	var res MergeResult
	req, err := checkMergeRequest(ctx, req)
	if err != nil {
		return res, err
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = DefaultMergeTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// 1. Fetch, then check that nothing moved.
	remoteRefs := "refs/remotes/" + req.Remote + "/"
	if _, err := mergeGitRun(ctx, req.RepoDir, "fetch", "--no-tags", "--no-recurse-submodules", "--quiet",
		req.Remote, "+refs/heads/*:"+remoteRefs+"*"); err != nil {
		return res, fmt.Errorf("fetch %s: %w", req.Remote, err)
	}
	if err := mergeCheckChangeHead(ctx, req); err != nil {
		return res, err
	}
	tip, err := mergeRevParse(ctx, req.RepoDir, remoteRefs+req.TargetBranch)
	if err != nil {
		return res, fmt.Errorf("target branch %s/%s: %w", req.Remote, req.TargetBranch, err)
	}
	res.TargetBefore = tip
	res.TargetAfter = tip

	// 2. Already part of the target: nothing to do.
	contained, err := mergeIsAncestor(ctx, req.RepoDir, req.HeadSHA, tip)
	if err != nil {
		return res, err
	}
	if contained {
		res.AlreadyMerged = true
		res.MergedSHA = findMergeOf(ctx, req, tip)
		return res, nil
	}

	// 3. Prepare the worktree at the remote tip.
	if err := prepareMergeWorktree(ctx, req, tip); err != nil {
		return res, err
	}

	// 4. Merge.
	switch req.Mode {
	case MergeModeFFOnly:
		ff, err := mergeIsAncestor(ctx, req.WorktreeDir, tip, req.HeadSHA)
		if err != nil {
			return res, err
		}
		if !ff {
			return res, fmt.Errorf("%w: %s does not contain %s/%s at %s",
				ErrNotFastForward, req.HeadSHA, req.Remote, req.TargetBranch, tip)
		}
		if _, err := mergeGitRun(ctx, req.WorktreeDir, "merge", "--ff-only", "--quiet", req.HeadSHA); err != nil {
			return res, fmt.Errorf("fast-forward: %w", err)
		}
		res.FastForward = true
	case MergeModeCommit:
		msg := req.Message
		if strings.TrimSpace(msg) == "" {
			msg = fmt.Sprintf("Merge %s into %s", req.ChangeBranch, req.TargetBranch)
		}
		_, err := mergeGitRun(ctx, req.WorktreeDir,
			"-c", "user.name="+req.Identity.Name, "-c", "user.email="+req.Identity.Email,
			"-c", "commit.gpgsign=false",
			"merge", "--no-ff", "--no-edit", "--quiet", "-m", msg, req.HeadSHA)
		if err != nil {
			// A merge in progress means a conflict. Anything else failed before
			// it touched the worktree.
			if _, perr := mergeGitRun(ctx, req.WorktreeDir, "rev-parse", "-q", "--verify", "MERGE_HEAD"); perr == nil {
				if _, aerr := mergeGitRun(ctx, req.WorktreeDir, "merge", "--abort"); aerr != nil {
					return res, fmt.Errorf("%w (and merge --abort failed: %v)", ErrConflict, aerr)
				}
				return res, fmt.Errorf("%w: merging %s into %s at %s", ErrConflict, req.HeadSHA, req.TargetBranch, tip)
			}
			return res, fmt.Errorf("merge: %w", err)
		}
	}
	after, err := mergeRevParse(ctx, req.WorktreeDir, "HEAD")
	if err != nil {
		return res, err
	}
	res.MergedSHA = after
	res.TargetAfter = after

	// 5. Push, never with force. A plain push of a commit ID to the branch
	// ref is refused by the remote unless it is a fast-forward.
	if req.Push {
		if m.beforePush != nil {
			m.beforePush()
		}
		_, perr := mergeGitRun(ctx, req.WorktreeDir, "push", "--quiet", "--no-verify",
			req.Remote, after+":refs/heads/"+req.TargetBranch)
		if perr != nil {
			out, lerr := mergeGitRun(ctx, req.WorktreeDir, "ls-remote", req.Remote, "refs/heads/"+req.TargetBranch)
			if lerr == nil {
				if f := strings.Fields(out); len(f) > 0 && f[0] != tip {
					return res, fmt.Errorf("%w: %s/%s is at %s, the merge started from %s",
						ErrRemoteMoved, req.Remote, req.TargetBranch, f[0], tip)
				}
			}
			return res, fmt.Errorf("push %s %s: %w", req.Remote, req.TargetBranch, perr)
		}
		res.Pushed = true
	}
	return res, nil
}

// checkMergeRequest validates req and makes the paths absolute.
func checkMergeRequest(ctx context.Context, req MergeRequest) (MergeRequest, error) {
	if req.RepoDir == "" || req.WorktreeDir == "" {
		return req, errors.New("merge: RepoDir and WorktreeDir are required")
	}
	var err error
	if req.RepoDir, err = filepath.Abs(req.RepoDir); err != nil {
		return req, err
	}
	if req.WorktreeDir, err = filepath.Abs(req.WorktreeDir); err != nil {
		return req, err
	}
	if req.Mode != MergeModeFFOnly && req.Mode != MergeModeCommit {
		return req, fmt.Errorf("merge: mode must be %q or %q, got %q", MergeModeFFOnly, MergeModeCommit, req.Mode)
	}
	if !mergeFullSHARe.MatchString(req.HeadSHA) {
		return req, fmt.Errorf("merge: HeadSHA must be a full lowercase commit ID, got %q", req.HeadSHA)
	}
	if req.Mode == MergeModeCommit && (strings.TrimSpace(req.Identity.Name) == "" || strings.TrimSpace(req.Identity.Email) == "") {
		return req, errors.New("merge: merge-commit mode needs an identity name and email")
	}
	for _, f := range []struct{ what, name string }{
		{"remote", req.Remote}, {"target branch", req.TargetBranch}, {"change branch", req.ChangeBranch},
	} {
		if f.name == "" || strings.HasPrefix(f.name, "-") {
			return req, fmt.Errorf("merge: bad %s %q", f.what, f.name)
		}
		if _, err := mergeGitRun(ctx, req.RepoDir, "check-ref-format", "refs/heads/"+f.name); err != nil {
			return req, fmt.Errorf("merge: bad %s %q", f.what, f.name)
		}
	}
	if same, _ := mergeSamePath(req.RepoDir, req.WorktreeDir); same {
		return req, errors.New("merge: WorktreeDir must not be RepoDir")
	}
	return req, nil
}

// mergeCheckChangeHead refuses unless the change branch, locally or on the remote,
// still points at HeadSHA. A branch that exists in both places and differs in
// one is fine as long as the other one still holds HeadSHA.
func mergeCheckChangeHead(ctx context.Context, req MergeRequest) error {
	refs := []string{
		"refs/heads/" + req.ChangeBranch,
		"refs/remotes/" + req.Remote + "/" + req.ChangeBranch,
	}
	var found []string
	for _, ref := range refs {
		sha, err := mergeRevParse(ctx, req.RepoDir, ref)
		if err != nil {
			var ge *mergeGitError
			if errors.As(err, &ge) && ge.code == 1 {
				continue // ref does not exist
			}
			return err
		}
		if sha == req.HeadSHA {
			return nil
		}
		found = append(found, fmt.Sprintf("%s is at %s", ref, sha))
	}
	if len(found) == 0 {
		return fmt.Errorf("%w: %s (expected %s)", ErrChangeBranchMissing, req.ChangeBranch, req.HeadSHA)
	}
	return fmt.Errorf("%w: expected %s, but %s", ErrHeadMoved, req.HeadSHA, strings.Join(found, " and "))
}

// prepareMergeWorktree makes WorktreeDir a clean detached worktree at tip.
func prepareMergeWorktree(ctx context.Context, req MergeRequest, tip string) error {
	entries, err := os.ReadDir(req.WorktreeDir)
	switch {
	case os.IsNotExist(err) || (err == nil && len(entries) == 0):
		if err := os.MkdirAll(filepath.Dir(req.WorktreeDir), 0o755); err != nil {
			return err
		}
		// Forget worktrees whose directory was deleted.
		if _, err := mergeGitRun(ctx, req.RepoDir, "worktree", "prune"); err != nil {
			return err
		}
		if _, err := mergeGitRun(ctx, req.RepoDir, "worktree", "add", "--detach", "--quiet", req.WorktreeDir, tip); err != nil {
			return fmt.Errorf("create merge worktree: %w", err)
		}
		return nil
	case err != nil:
		return err
	}

	// The directory exists and has files: it must be a worktree of this repo.
	top, err := mergeGitRun(ctx, req.WorktreeDir, "rev-parse", "--show-toplevel")
	if err != nil {
		return fmt.Errorf("%s is not a git worktree: %w", req.WorktreeDir, err)
	}
	if same, _ := mergeSamePath(top, req.WorktreeDir); !same {
		return fmt.Errorf("%s is not the top of a git worktree", req.WorktreeDir)
	}
	wtCommon, err := mergeGitRun(ctx, req.WorktreeDir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return err
	}
	repoCommon, err := mergeGitRun(ctx, req.RepoDir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return err
	}
	if same, _ := mergeSamePath(wtCommon, repoCommon); !same {
		return fmt.Errorf("%s is a worktree of another repository", req.WorktreeDir)
	}
	status, err := mergeGitRun(ctx, req.WorktreeDir, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return err
	}
	if strings.TrimSpace(status) != "" {
		return fmt.Errorf("%w: %s", ErrDirtyWorktree, req.WorktreeDir)
	}
	if _, err := mergeGitRun(ctx, req.WorktreeDir, "checkout", "--detach", "--quiet", tip); err != nil {
		return fmt.Errorf("reset merge worktree: %w", err)
	}
	return nil
}

// findMergeOf returns the commit on the target that holds head: the merge
// commit whose second parent is head, or head itself when it was
// fast-forwarded in.
func findMergeOf(ctx context.Context, req MergeRequest, tip string) string {
	out, err := mergeGitRun(ctx, req.RepoDir, "rev-list", "--merges", "--parents", "--ancestry-path", req.HeadSHA+".."+tip)
	if err != nil {
		return req.HeadSHA
	}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 3 && f[2] == req.HeadSHA {
			return f[0]
		}
	}
	return req.HeadSHA
}

func mergeSamePath(a, b string) (bool, error) {
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		return false, err
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		return false, err
	}
	return filepath.Clean(ra) == filepath.Clean(rb), nil
}

func mergeRevParse(ctx context.Context, dir, rev string) (string, error) {
	return mergeGitRun(ctx, dir, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
}

// mergeIsAncestor reports whether a is an ancestor of (or equal to) b.
func mergeIsAncestor(ctx context.Context, dir, a, b string) (bool, error) {
	_, err := mergeGitRun(ctx, dir, "merge-base", "--is-ancestor", a, b)
	if err == nil {
		return true, nil
	}
	var ge *mergeGitError
	if errors.As(err, &ge) && ge.code == 1 {
		return false, nil
	}
	return false, err
}

// mergeGitError is a failed git command.
type mergeGitError struct {
	args   []string
	code   int
	stderr string
	err    error
}

func (e *mergeGitError) Error() string {
	msg := strings.TrimSpace(e.stderr)
	if msg == "" {
		msg = e.err.Error()
	}
	return fmt.Sprintf("git %s: %s", strings.Join(e.args, " "), msg)
}

func (e *mergeGitError) Unwrap() error { return e.err }

// mergeGitEnvDrop lists variables that would point git at another repository.
var mergeGitEnvDrop = []string{
	"GIT_DIR=", "GIT_WORK_TREE=", "GIT_INDEX_FILE=", "GIT_OBJECT_DIRECTORY=",
	"GIT_COMMON_DIR=", "GIT_PREFIX=", "GIT_NAMESPACE=",
}

// mergeGitRun runs git in dir with an explicit argument list and returns trimmed
// stdout. It never runs hooks, never prompts, and never auto-collects garbage.
func mergeGitRun(ctx context.Context, dir string, args ...string) (string, error) {
	full := append([]string{
		"-c", "core.hooksPath=/dev/null",
		"-c", "gc.auto=0",
		"-c", "maintenance.auto=false",
	}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Dir = dir
	cmd.WaitDelay = 5 * time.Second
	var env []string
	for _, kv := range os.Environ() {
		drop := false
		for _, p := range mergeGitEnvDrop {
			if strings.HasPrefix(kv, p) {
				drop = true
				break
			}
		}
		if !drop {
			env = append(env, kv)
		}
	}
	cmd.Env = append(env, "LC_ALL=C", "GIT_TERMINAL_PROMPT=0", "GIT_EDITOR=true", "GIT_MERGE_AUTOEDIT=no")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		code := -1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
		if ctx.Err() != nil {
			err = fmt.Errorf("%w (%v)", ctx.Err(), err)
		}
		return strings.TrimSpace(stdout.String()), &mergeGitError{args: args, code: code, stderr: stderr.String(), err: err}
	}
	return strings.TrimSpace(stdout.String()), nil
}

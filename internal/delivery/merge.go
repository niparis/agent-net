package delivery

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// This file is the git side of the merge step. It merges exactly the commit
// whose evidence was checked, in a worktree it owns, and refuses when
// anything moved. It does not decide whether a merge is allowed: the caller
// checks the evidence first.
//
// Checkout, merge and push never run in a repository a worker can write. The
// config of such a repository is one file shared by all its worktrees, so no
// check of it can close the gap between the check and the git call that
// reads it. Merge works in a bare mirror it owns (MirrorDir). It fetches the
// target branch from the remote URL the caller pinned (RemoteURL) and the
// change branch from RepoDir by name, merges in a worktree of the mirror, and
// pushes to the pinned URL. The only git call that touches RepoDir is the
// fetch, which runs git upload-pack there; it runs no filter, merge driver or
// hook, and RepoDir's config is checked first anyway.

// Merge modes.
const (
	MergeModeFFOnly = "ff-only"
	MergeModeCommit = "merge-commit"
)

// DefaultMergeTimeout bounds one Merge call when the request sets no Timeout.
const DefaultMergeTimeout = 5 * time.Minute

const (
	// mergeMirrorMarker, in a mirror Merge created, marks it as Merge's own.
	mergeMirrorMarker = "agent-net-merge-mirror"
	// mergeSourceRef is where the change branch of RepoDir is fetched to
	// inside the mirror, one ref per branch name.
	mergeSourceRef = "refs/agent-net/source/"
	// mergeCleanupTimeout bounds the cleanup after a failed merge. The
	// cleanup runs even when the caller's context has ended.
	mergeCleanupTimeout = 30 * time.Second
	// mergeLockPoll is how often Merge tries the lock again.
	mergeLockPoll = 50 * time.Millisecond
	// mergeMarkerFile, in the git dir of a worktree Merge created, marks the
	// worktree as Merge's own. It holds the worktree's path.
	mergeMarkerFile = "agent-net-merge-worktree"
)

// Errors returned by Merge. Test them with errors.Is.
var (
	// ErrHeadMoved: the change branch no longer points at HeadSHA.
	ErrHeadMoved = errors.New("change branch moved since the evidence was checked")
	// ErrChangeBranchMissing: the change branch exists neither locally nor on the remote.
	ErrChangeBranchMissing = errors.New("change branch not found")
	// ErrEmptyChange: HeadSHA is BaseSHA, so there is nothing to merge.
	ErrEmptyChange = errors.New("empty change: its head is its base")
	// ErrTargetMissing: the target branch does not exist on the remote and
	// the request has no CreateFrom.
	ErrTargetMissing = errors.New("target branch does not exist on the remote")
	// ErrNotOwnedWorktree: WorktreeDir exists but is not a worktree Merge
	// created, is a symlink, or is not on a detached HEAD. Nothing in it was
	// changed.
	ErrNotOwnedWorktree = errors.New("merge worktree is not one that Merge created")
	// ErrMergeBusy: another Merge held the lock on WorktreeDir until the
	// timeout.
	ErrMergeBusy = errors.New("another merge holds the merge worktree")
	// ErrNotFastForward: ff-only mode, and HeadSHA does not contain the target tip.
	ErrNotFastForward = errors.New("head is not a fast-forward of the target branch")
	// ErrConflict: merge-commit mode, and the merge conflicts. The worktree is left clean.
	ErrConflict = errors.New("merge conflict")
	// ErrNotOwnedMirror: MirrorDir exists but is not a bare repository that
	// Merge created. Nothing in it was changed.
	ErrNotOwnedMirror = errors.New("merge mirror is not one that Merge created")
	// ErrRemoteMoved: the remote target branch moved during the call (or was
	// created by someone else). Nothing was forced.
	ErrRemoteMoved = errors.New("remote target branch moved during the merge")
)

// MergeIdentity is the author and committer of a merge commit.
type MergeIdentity struct {
	Name  string
	Email string
}

// MergeRequest says what to merge and where.
type MergeRequest struct {
	// RepoDir is a checkout of the repository, where the change branch may
	// live. Nothing in it is changed: not its branches, files, refs or
	// config. It is only read, by fetching the change branch from it.
	RepoDir string
	// MirrorDir is a bare repository owned by Merge, written only by the
	// daemon. When it is missing or an empty directory, Merge creates it and
	// marks it as its own. An existing directory must be such a marked bare
	// repository (ErrNotOwnedMirror). The merge worktree is a worktree of it.
	// The file MirrorDir+".lock" is locked for the whole call, after the
	// worktree lock.
	MirrorDir string
	// WorktreeDir is the merge worktree, owned by Merge. When it is missing
	// or an empty directory, Merge creates it as a worktree of MirrorDir on a
	// detached HEAD and marks it as its own. An existing directory must be
	// such a marked worktree, still on a detached HEAD, and not a symlink;
	// Merge refuses anything else (ErrNotOwnedWorktree). At the start of
	// every call Merge resets it and removes every file git does not track
	// (reset --hard, clean -ffdx). The file WorktreeDir+".lock" is locked for
	// the whole call.
	WorktreeDir string
	// Remote is the name of the remote, for example "origin". It names the
	// remote-tracking refs inside the mirror and the remote RepoDir must
	// have, with URL RemoteURL.
	Remote string
	// RemoteURL is required: the URL of Remote, from the caller's own record
	// (the project registry, or the first URL it saw), never read from the
	// repository. Merge fetches the target branch from it and pushes to it.
	// It refuses (ErrRemoteURL) when RepoDir's own fetch URL for
	// Remote is not exactly this string, which is a sign that someone
	// changed the config.
	RemoteURL string
	// TargetBranch is the branch to merge into, for example "main".
	TargetBranch string
	// ChangeBranch is the branch that holds the change. It must still point
	// at HeadSHA, in RepoDir or on the remote.
	ChangeBranch string
	// BaseSHA is the full commit ID the change started from, as recorded.
	// A change whose HeadSHA is BaseSHA is empty and is refused.
	BaseSHA string
	// HeadSHA is the full commit ID whose evidence was checked.
	HeadSHA string
	// CreateFrom is an optional full commit ID. When TargetBranch does not
	// exist on the remote, Merge creates it at this commit (a release
	// branch) and merges into it, pushing with a lease that the branch still
	// does not exist. It is not used when the branch exists.
	CreateFrom string
	// Mode is MergeModeFFOnly or MergeModeCommit.
	Mode string
	// Message is the merge commit message (merge-commit mode). When empty, a
	// default is used.
	Message string
	// Identity is required in merge-commit mode.
	Identity MergeIdentity
	// Push pushes TargetBranch to Remote with a lease on the tip the merge
	// started from, so it never overwrites a remote that moved.
	Push bool
	// Timeout bounds the whole call. Zero means DefaultMergeTimeout.
	Timeout time.Duration
	// GitPath is the PATH list git is found on and runs with, so git-lfs and
	// credential helpers are found too. The daemon passes its login PATH.
	// Empty means this process's PATH.
	GitPath string
}

// MergeResult says what Merge did.
type MergeResult struct {
	// MergedSHA is the commit that holds the change on the target branch:
	// HeadSHA for a fast-forward, the merge commit otherwise.
	MergedSHA string
	// TargetBefore is the remote target tip the merge started from: the tip
	// the remote reported, or CreateFrom when the branch was created.
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
	// Created is true when the target branch did not exist on the remote
	// and the merge started from CreateFrom.
	Created bool
}

// mergeRunner carries the test seams. The zero value is the real thing.
type mergeRunner struct {
	// afterFetch runs after the fetch.
	afterFetch func()
	// afterMerge runs after the merge command, before its result is read.
	afterMerge func()
	// beforePush runs after the local merge and before the push.
	beforePush func()
}

// Merge merges req.HeadSHA into req.TargetBranch in req.WorktreeDir.
func Merge(ctx context.Context, req MergeRequest) (MergeResult, error) {
	return (&mergeRunner{}).merge(ctx, req)
}

func (m *mergeRunner) merge(ctx context.Context, req MergeRequest) (MergeResult, error) {
	var res MergeResult
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = DefaultMergeTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := checkMergeRequest(ctx, req)
	if err != nil {
		return res, err
	}
	src := gitIn{gitPath: req.GitPath, dir: req.RepoDir}
	repo := gitIn{gitPath: req.GitPath, dir: req.MirrorDir} // the mirror
	wt := gitIn{gitPath: req.GitPath, dir: req.WorktreeDir}

	unlock, err := lockMergeWorktree(ctx, req.WorktreeDir)
	if err != nil {
		return res, err
	}
	defer unlock()
	unlockMirror, err := lockMergeWorktree(ctx, req.MirrorDir)
	if err != nil {
		return res, err
	}
	defer unlockMirror()

	// 1. Refuse a source repository that disagrees with the pinned URL or
	// names programs in its config (git upload-pack runs inside it). Then
	// get the mirror. Ask the remote itself where the target is, then fetch,
	// then check that nothing moved.
	if err := checkSource(ctx, src, req); err != nil {
		return res, err
	}
	if err := prepareMirror(ctx, req, repo); err != nil {
		return res, err
	}
	tip, exists, err := remoteBranchTip(ctx, repo, req.RemoteURL, req.TargetBranch)
	if err != nil {
		return res, err
	}
	if !exists {
		if req.CreateFrom == "" {
			return res, fmt.Errorf("%w: %s/%s", ErrTargetMissing, req.Remote, req.TargetBranch)
		}
		tip = req.CreateFrom
		res.Created = true
	}
	if err := fetchRefspec(ctx, repo, req.RemoteURL, "+refs/heads/*:refs/remotes/"+req.Remote+"/*"); err != nil {
		return res, err
	}
	if err := fetchSourceBranch(ctx, src, repo, req); err != nil {
		return res, err
	}
	if m.afterFetch != nil {
		m.afterFetch()
	}
	if _, err := revCommit(ctx, repo, tip); err != nil {
		if isExit(err, 1) {
			return res, fmt.Errorf("%w: %s/%s was at %s, which the fetch did not bring", ErrRemoteMoved, req.Remote, req.TargetBranch, tip)
		}
		return res, err
	}
	if err := mergeCheckChangeHead(ctx, repo, req); err != nil {
		return res, err
	}
	res.TargetBefore = tip
	res.TargetAfter = tip

	// 2. Already part of the target: nothing to do, unless the target must
	// still be created.
	contained, err := isAncestor(ctx, repo, req.HeadSHA, tip)
	if err != nil {
		return res, err
	}
	if contained && !res.Created {
		res.AlreadyMerged = true
		res.MergedSHA = findMergeOf(ctx, repo, req.HeadSHA, tip)
		return res, nil
	}

	// 3. Prepare the worktree at the tip.
	if err := prepareMergeWorktree(ctx, req, repo, wt, tip); err != nil {
		return res, err
	}

	// 4. Merge.
	after := tip
	if contained {
		res.MergedSHA = findMergeOf(ctx, repo, req.HeadSHA, tip)
	} else {
		if err := m.mergeIn(ctx, req, wt, tip); err != nil {
			return res, err
		}
		if after, err = revCommit(ctx, wt, "HEAD"); err != nil {
			return res, err
		}
		res.MergedSHA = after
		res.FastForward = req.Mode == MergeModeFFOnly
	}
	res.TargetAfter = after

	// 5. Push with a lease: the remote branch must still be at tip, or must
	// still not exist when it is created here.
	if req.Push {
		if m.beforePush != nil {
			m.beforePush()
		}
		if err := checkConfig(ctx, wt); err != nil {
			return res, err
		}
		if err := pushWithLease(ctx, wt, req, tip, res.Created, after); err != nil {
			return res, err
		}
		res.Pushed = true
	}
	return res, nil
}

// mergeIn merges HeadSHA into the worktree, which is at tip.
func (m *mergeRunner) mergeIn(ctx context.Context, req MergeRequest, wt gitIn, tip string) error {
	switch req.Mode {
	case MergeModeFFOnly:
		ff, err := isAncestor(ctx, wt, tip, req.HeadSHA)
		if err != nil {
			return err
		}
		if !ff {
			return fmt.Errorf("%w: %s does not contain %s/%s at %s",
				ErrNotFastForward, req.HeadSHA, req.Remote, req.TargetBranch, tip)
		}
		if _, err := wt.run(ctx, "merge", "--ff-only", "--quiet", req.HeadSHA); err != nil {
			return fmt.Errorf("fast-forward: %w", err)
		}
		return nil
	default: // MergeModeCommit
		msg := req.Message
		if strings.TrimSpace(msg) == "" {
			msg = fmt.Sprintf("Merge %s into %s", req.ChangeBranch, req.TargetBranch)
		}
		_, err := wt.run(ctx,
			"-c", "user.name="+req.Identity.Name, "-c", "user.email="+req.Identity.Email,
			"-c", "commit.gpgsign=false",
			"merge", "--no-ff", "--no-edit", "--quiet", "-m", msg, req.HeadSHA)
		if m.afterMerge != nil {
			m.afterMerge()
		}
		if err == nil {
			return nil
		}
		// A merge in progress means a conflict, or a merge stopped by the
		// context. Either way the worktree is put back.
		inProgress, cerr := abortMerge(ctx, wt)
		if cerr != nil {
			return fmt.Errorf("merge: %w (and the cleanup failed: %v)", err, cerr)
		}
		if ctx.Err() != nil {
			return fmt.Errorf("merge: %w", err)
		}
		if inProgress {
			return fmt.Errorf("%w: merging %s into %s at %s", ErrConflict, req.HeadSHA, req.TargetBranch, tip)
		}
		return fmt.Errorf("merge: %w", err)
	}
}

// abortMerge ends a merge in progress in wt. It runs even when ctx has ended,
// under its own short timeout. It reports whether a merge was in progress.
func abortMerge(ctx context.Context, wt gitIn) (bool, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), mergeCleanupTimeout)
	defer cancel()
	if _, err := wt.run(ctx, "rev-parse", "--quiet", "--verify", "MERGE_HEAD"); err != nil {
		if isExit(err, 1) {
			return false, nil
		}
		return false, err
	}
	if _, err := wt.run(ctx, "merge", "--abort"); err != nil {
		if _, rerr := wt.run(ctx, "reset", "--hard", "--quiet"); rerr != nil {
			return true, fmt.Errorf("merge --abort: %v; reset --hard: %w", err, rerr)
		}
	}
	return true, nil
}

// pushWithLease pushes after to the target branch of the pinned remote URL. The lease makes it a
// compare-and-swap: the remote takes it only while the branch is at tip, or
// does not exist when created is true.
func pushWithLease(ctx context.Context, wt gitIn, req MergeRequest, tip string, created bool, after string) error {
	ref := "refs/heads/" + req.TargetBranch
	expect := tip
	if created {
		expect = ""
	}
	_, perr := wt.run(ctx, "push", "--quiet", "--no-verify", "--force-with-lease="+ref+":"+expect,
		req.RemoteURL, after+":"+ref)
	if perr == nil {
		return nil
	}
	now, exists, lerr := remoteBranchTip(ctx, wt, req.RemoteURL, req.TargetBranch)
	if lerr == nil {
		switch {
		case created && exists:
			return fmt.Errorf("%w: %s/%s was created at %s by someone else", ErrRemoteMoved, req.Remote, req.TargetBranch, now)
		case !created && now != tip:
			if !exists {
				now = "nothing (deleted)"
			}
			return fmt.Errorf("%w: %s/%s is at %s, the merge started from %s",
				ErrRemoteMoved, req.Remote, req.TargetBranch, now, tip)
		}
	}
	return fmt.Errorf("push %s %s: %w", req.Remote, req.TargetBranch, perr)
}

// remoteBranchTip asks the remote itself where a branch points. exists is
// false when the remote has no such branch.
func remoteBranchTip(ctx context.Context, g gitIn, remote, branch string) (sha string, exists bool, err error) {
	ref := "refs/heads/" + branch
	out, err := g.run(ctx, "ls-remote", "--refs", remote, ref)
	if err != nil {
		return "", false, fmt.Errorf("ls-remote %s: %w", redactURL(remote), err)
	}
	for _, line := range strings.Split(out, "\n") {
		id, name, ok := strings.Cut(line, "\t")
		if !ok || name != ref {
			continue // ls-remote also matches refs that only end with ref
		}
		if !IsCommitID(id) {
			return "", false, fmt.Errorf("ls-remote %s: bad commit ID %q for %s", redactURL(remote), id, ref)
		}
		return id, true, nil
	}
	return "", false, nil
}

// lockMergeWorktree takes the lock file next to dir. It waits until ctx ends.
func lockMergeWorktree(ctx context.Context, dir string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return nil, err
	}
	path := dir + ".lock"
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open merge lock: %w", err)
	}
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				f.Close()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {
			f.Close()
			return nil, fmt.Errorf("merge lock %s: %w", path, err)
		}
		t := time.NewTimer(mergeLockPoll)
		select {
		case <-ctx.Done():
			t.Stop()
			f.Close()
			return nil, fmt.Errorf("%w: %s (%v)", ErrMergeBusy, path, ctx.Err())
		case <-t.C:
		}
	}
}

// checkMergeRequest validates req and makes the paths absolute.
func checkMergeRequest(ctx context.Context, req MergeRequest) (MergeRequest, error) {
	if req.RepoDir == "" || req.WorktreeDir == "" || req.MirrorDir == "" {
		return req, errors.New("merge: RepoDir, MirrorDir and WorktreeDir are required")
	}
	if err := checkRemoteURLValue(req.RemoteURL); err != nil {
		return req, fmt.Errorf("merge: %w", err)
	}
	var err error
	if req.RepoDir, err = filepath.Abs(req.RepoDir); err != nil {
		return req, err
	}
	if req.MirrorDir, err = filepath.Abs(req.MirrorDir); err != nil {
		return req, err
	}
	if req.WorktreeDir, err = filepath.Abs(req.WorktreeDir); err != nil {
		return req, err
	}
	if req.Mode != MergeModeFFOnly && req.Mode != MergeModeCommit {
		return req, fmt.Errorf("merge: mode must be %q or %q, got %q", MergeModeFFOnly, MergeModeCommit, req.Mode)
	}
	if err := checkCommitID("merge: HeadSHA", req.HeadSHA); err != nil {
		return req, err
	}
	if err := checkCommitID("merge: BaseSHA", req.BaseSHA); err != nil {
		return req, err
	}
	if req.CreateFrom != "" {
		if err := checkCommitID("merge: CreateFrom", req.CreateFrom); err != nil {
			return req, err
		}
	}
	if req.HeadSHA == req.BaseSHA {
		return req, fmt.Errorf("%w: %s", ErrEmptyChange, req.HeadSHA)
	}
	if req.Mode == MergeModeCommit && (strings.TrimSpace(req.Identity.Name) == "" || strings.TrimSpace(req.Identity.Email) == "") {
		return req, errors.New("merge: merge-commit mode needs an identity name and email")
	}
	// check-ref-format reads no repository, so it runs next to the mirror.
	if err := os.MkdirAll(filepath.Dir(req.MirrorDir), 0o755); err != nil {
		return req, err
	}
	repo := gitIn{gitPath: req.GitPath, dir: filepath.Dir(req.MirrorDir)}
	for _, f := range []struct{ what, name string }{
		{"remote", req.Remote}, {"target branch", req.TargetBranch}, {"change branch", req.ChangeBranch},
	} {
		if err := checkRefName(ctx, repo, f.what, f.name); err != nil {
			return req, fmt.Errorf("merge: %w", err)
		}
	}
	for _, pair := range [][2]string{
		{req.RepoDir, req.WorktreeDir}, {req.RepoDir, req.MirrorDir}, {req.MirrorDir, req.WorktreeDir},
	} {
		if same, _ := mergeSamePath(pair[0], pair[1]); same || pair[0] == pair[1] {
			return req, fmt.Errorf("merge: %s must not be the same directory as %s", pair[1], pair[0])
		}
	}
	return req, nil
}

// mergeCheckChangeHead refuses unless the change branch, in RepoDir (fetched
// to mergeSourceRef) or on the remote, still points at HeadSHA. A branch that exists in both places and differs in
// one is fine as long as the other one still holds HeadSHA.
func mergeCheckChangeHead(ctx context.Context, repo gitIn, req MergeRequest) error {
	refs := []string{
		mergeSourceRef + req.ChangeBranch,
		"refs/remotes/" + req.Remote + "/" + req.ChangeBranch,
	}
	var found []string
	for _, ref := range refs {
		sha, err := revCommit(ctx, repo, ref)
		if err != nil {
			if isExit(err, 1) {
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

// prepareMergeWorktree makes WorktreeDir a clean worktree on a detached HEAD
// at tip. It creates the worktree when the directory is missing or empty.
// Otherwise the directory must be a worktree Merge created; it is reset and
// cleaned, which also ends a merge a stopped run left in progress.
func prepareMergeWorktree(ctx context.Context, req MergeRequest, repo, wt gitIn, tip string) error {
	dir := req.WorktreeDir
	fi, err := os.Lstat(dir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return createMergeWorktree(ctx, repo, wt, dir, tip)
	case err != nil:
		return err
	case fi.Mode()&os.ModeSymlink != 0:
		return fmt.Errorf("%w: %s is a symlink", ErrNotOwnedWorktree, dir)
	case !fi.IsDir():
		return fmt.Errorf("%w: %s is not a directory", ErrNotOwnedWorktree, dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return createMergeWorktree(ctx, repo, wt, dir, tip)
	}
	if err := checkOwnedWorktree(ctx, repo, wt, dir); err != nil {
		return err
	}
	// reset and merge run the drivers of this worktree's own config too.
	if err := checkConfig(ctx, wt); err != nil {
		return err
	}
	// Abort first: reset alone would leave a half-done merge's state files.
	if _, err := abortMerge(ctx, wt); err != nil {
		return fmt.Errorf("reset merge worktree: %w", err)
	}
	if _, err := wt.run(ctx, "reset", "--hard", "--quiet", tip); err != nil {
		return fmt.Errorf("reset merge worktree: %w", err)
	}
	if _, err := wt.run(ctx, "clean", "-ffdxq"); err != nil {
		return fmt.Errorf("clean merge worktree: %w", err)
	}
	return nil
}

// createMergeWorktree adds dir as a detached worktree of the repository at
// tip and marks it as Merge's own.
func createMergeWorktree(ctx context.Context, repo, wt gitIn, dir, tip string) error {
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	// Forget worktrees whose directory was deleted.
	if _, err := repo.run(ctx, "worktree", "prune"); err != nil {
		return err
	}
	if _, err := repo.run(ctx, "worktree", "add", "--detach", "--quiet", dir, tip); err != nil {
		return fmt.Errorf("create merge worktree: %w", err)
	}
	gitDir, err := wt.run(ctx, "rev-parse", "--path-format=absolute", "--git-dir")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(gitDir, mergeMarkerFile), []byte(dir+"\n"), 0o600); err != nil {
		return fmt.Errorf("mark merge worktree: %w", err)
	}
	return nil
}

// checkOwnedWorktree refuses dir unless it is the top of a worktree of the
// same repository, marked by Merge for this path, on a detached HEAD.
func checkOwnedWorktree(ctx context.Context, repo, wt gitIn, dir string) error {
	refuse := func(why string, args ...any) error {
		return fmt.Errorf("%w: %s %s", ErrNotOwnedWorktree, dir, fmt.Sprintf(why, args...))
	}
	top, err := wt.run(ctx, "rev-parse", "--path-format=absolute", "--show-toplevel")
	if err != nil {
		return refuse("is not a git worktree (%v)", err)
	}
	if same, _ := mergeSamePath(top, dir); !same {
		return refuse("is not the top of a git worktree")
	}
	wtCommon, err := wt.run(ctx, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return err
	}
	repoCommon, err := repo.run(ctx, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return err
	}
	if same, _ := mergeSamePath(wtCommon, repoCommon); !same {
		return refuse("is a worktree of another repository")
	}
	gitDir, err := wt.run(ctx, "rev-parse", "--path-format=absolute", "--git-dir")
	if err != nil {
		return err
	}
	marked, err := os.ReadFile(filepath.Join(gitDir, mergeMarkerFile))
	if err != nil {
		return refuse("has no merge worktree mark")
	}
	if same, _ := mergeSamePath(strings.TrimSpace(string(marked)), dir); !same {
		return refuse("is marked for another path")
	}
	if _, err := wt.run(ctx, "symbolic-ref", "--quiet", "HEAD"); err == nil {
		return refuse("is on a branch, not a detached HEAD")
	} else if !isExit(err, 1) {
		return err
	}
	return nil
}

// findMergeOf returns the commit on the target that holds head: the merge
// commit whose second parent is head, or head itself when it was
// fast-forwarded in.
func findMergeOf(ctx context.Context, repo gitIn, head, tip string) string {
	out, err := repo.run(ctx, "rev-list", "--merges", "--parents", "--ancestry-path", head+".."+tip)
	if err != nil {
		return head
	}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 3 && f[2] == head {
			return f[0]
		}
	}
	return head
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

// checkSource refuses RepoDir when it has replace refs or a grafts file, names programs in its config (git
// upload-pack runs inside it for the fetch of the change branch) or when its
// own URL for the remote is not the pinned one.
func checkSource(ctx context.Context, src gitIn, req MergeRequest) error {
	if err := checkRepo(ctx, src); err != nil {
		return err
	}
	return checkRemoteURL(ctx, src, req.Remote, req.RemoteURL)
}

// fetchSourceBranch fetches the change branch of RepoDir into the mirror,
// under mergeSourceRef. A stale copy from an earlier call is removed first,
// so a branch deleted in RepoDir does not stay visible. A branch RepoDir does
// not have is not an error: the change may live on the remote only.
func fetchSourceBranch(ctx context.Context, src, mirror gitIn, req MergeRequest) error {
	ref := mergeSourceRef + req.ChangeBranch
	if _, err := mirror.run(ctx, "update-ref", "-d", ref); err != nil {
		return fmt.Errorf("remove stale %s: %w", ref, err)
	}
	_, exists, err := remoteBranchTip(ctx, mirror, req.RepoDir, req.ChangeBranch)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	return fetchRefspec(ctx, mirror, req.RepoDir, "+refs/heads/"+req.ChangeBranch+":"+ref)
}

// prepareMirror makes MirrorDir a bare repository that Merge owns. It creates
// it when the directory is missing or empty. Otherwise the directory must
// carry Merge's mark for this path and be a bare repository.
func prepareMirror(ctx context.Context, req MergeRequest, mirror gitIn) error {
	dir := req.MirrorDir
	refuse := func(why string, args ...any) error {
		return fmt.Errorf("%w: %s %s", ErrNotOwnedMirror, dir, fmt.Sprintf(why, args...))
	}
	marker := filepath.Join(dir, mergeMirrorMarker)
	fi, err := os.Lstat(dir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	case err != nil:
		return err
	case fi.Mode()&os.ModeSymlink != 0:
		return refuse("is a symlink")
	case !fi.IsDir():
		return refuse("is not a directory")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		if err := os.WriteFile(marker, []byte(dir+"\n"), 0o600); err != nil {
			return fmt.Errorf("mark merge mirror: %w", err)
		}
	} else {
		marked, err := os.ReadFile(marker)
		if err != nil {
			return refuse("has no merge mirror mark")
		}
		if same, _ := mergeSamePath(strings.TrimSpace(string(marked)), dir); !same {
			return refuse("is marked for another path")
		}
	}
	src := gitIn{gitPath: req.GitPath, dir: req.RepoDir}
	repoFormat, err := src.run(ctx, "rev-parse", "--show-object-format")
	if err != nil {
		return fmt.Errorf("read object format of %s: %w", req.RepoDir, err)
	}
	repoFormat = strings.TrimSpace(repoFormat)

	// A mirror whose creation stopped after the mark has no HEAD yet.
	if _, err := os.Lstat(filepath.Join(dir, "HEAD")); errors.Is(err, os.ErrNotExist) {
		if _, err := mirror.run(ctx, "init", "--bare", "--quiet", "--template=", "--object-format="+repoFormat); err != nil {
			return fmt.Errorf("create merge mirror: %w", err)
		}
		// Each worktree gets its own config.worktree, which checkConfig
		// reads with the worktree's scope. core.bare moves to the mirror's
		// own config.worktree, as git documents, so the worktrees are not
		// bare.
		for _, args := range [][]string{
			{"config", "extensions.worktreeConfig", "true"},
			{"config", "--worktree", "core.bare", "true"},
			{"config", "--unset", "core.bare"},
		} {
			if _, err := mirror.run(ctx, args...); err != nil {
				return fmt.Errorf("create merge mirror: %w", err)
			}
		}
	}
	if bare, err := mirror.run(ctx, "rev-parse", "--is-bare-repository"); err != nil || bare != "true" {
		return refuse("is not a bare repository")
	}
	gitDir, err := mirror.run(ctx, "rev-parse", "--path-format=absolute", "--git-dir")
	if err != nil {
		return refuse("is not a git repository (%v)", err)
	}
	if same, _ := mergeSamePath(gitDir, dir); !same {
		return refuse("is not the top of a git repository")
	}
	mirrorFormat, err := mirror.run(ctx, "rev-parse", "--show-object-format")
	if err != nil {
		return refuse("is not a git repository (%v)", err)
	}
	if strings.TrimSpace(mirrorFormat) != repoFormat {
		return refuse("object format %s does not match repository %s", strings.TrimSpace(mirrorFormat), repoFormat)
	}
	return checkRepo(ctx, mirror)
}

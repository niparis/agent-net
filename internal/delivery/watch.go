package delivery

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// This file is the git side of the default-branch watcher. It finds the
// commits that landed on a repository's default branch since the last check,
// so that the caller can flag the ones that skipped the delivery pipeline.
//
// Every call runs the git binary with an explicit argument list, a timeout,
// LC_ALL=C, and hooks switched off. Nothing here writes to the repository.

// watchGitTimeout bounds one git call when the caller's context has no earlier
// deadline.
const watchGitTimeout = 30 * time.Second

// Commit is one commit on the default branch.
type Commit struct {
	SHA     string
	Parents []string
	// Author and Committer are "Name <email>".
	Author    string
	Committer string
	Subject   string
	// Time is the committer time (when the commit landed), in UTC.
	Time time.Time
	// Trailers holds the git trailers of the message, keyed by trailer name
	// as written. The names agent-net uses are set by the delivery.trailer_*
	// settings, so no name is fixed here. If a name appears twice, the first
	// value wins.
	Trailers map[string]string
}

// IsMerge reports whether the commit has more than one parent.
func (c Commit) IsMerge() bool { return len(c.Parents) > 1 }

// MergedHeads returns the branch heads a merge commit brought in: its second
// and later parents. Evidence is keyed by the head commit of a change, not by
// the merge commit. It returns nil for a commit that is not a merge.
func MergedHeads(c Commit) []string {
	if len(c.Parents) < 2 {
		return nil
	}
	return append([]string(nil), c.Parents[1:]...)
}

// WatchResult is what NewCommits found.
type WatchResult struct {
	// Commits are the new commits on the branch's first-parent line, oldest
	// first.
	Commits []Commit
	// Rewritten is true when since is not an ancestor of the branch, which
	// means history was rewritten (or since no longer exists). Commits then
	// holds only the tip, because the real set of new commits is unknowable.
	Rewritten bool
}

// DefaultBranch finds the default branch of the repository in repoDir. It uses
// refs/remotes/origin/HEAD when that is set and points at a branch that
// exists, else main, else master, else the branch that is checked out. It
// returns an error when none of these gives a branch (for example a detached
// HEAD in a repository with no main or master).
func DefaultBranch(ctx context.Context, repoDir string) (string, error) {
	if _, err := git(ctx, repoDir, "rev-parse", "--git-dir"); err != nil {
		return "", fmt.Errorf("default branch: %s is not a usable git repository: %w", repoDir, err)
	}

	if out, err := git(ctx, repoDir, "symbolic-ref", "--quiet", "refs/remotes/origin/HEAD"); err == nil {
		if name := strings.TrimPrefix(strings.TrimSpace(out), "refs/remotes/origin/"); name != "" && name != strings.TrimSpace(out) {
			okLocal, err := refExists(ctx, repoDir, "refs/heads/"+name)
			if err != nil {
				return "", err
			}
			okRemote, err := refExists(ctx, repoDir, "refs/remotes/origin/"+name)
			if err != nil {
				return "", err
			}
			if okLocal || okRemote {
				return name, nil
			}
		}
	} else if !isExit(err, 1) {
		return "", fmt.Errorf("default branch: read origin/HEAD: %w", err)
	}

	for _, name := range []string{"main", "master"} {
		ok, err := refExists(ctx, repoDir, "refs/heads/"+name)
		if err != nil {
			return "", err
		}
		if ok {
			return name, nil
		}
	}

	out, err := git(ctx, repoDir, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err == nil {
		if name := strings.TrimSpace(out); name != "" {
			return name, nil
		}
	} else if !isExit(err, 1) {
		return "", fmt.Errorf("default branch: read HEAD: %w", err)
	}
	return "", fmt.Errorf("default branch: cannot find one in %s: origin/HEAD is not set, there is no main or master, and HEAD is detached", repoDir)
}

// NewCommits returns the commits reachable from branch and not from since,
// following first parents only, oldest first. A merge shows up as one commit,
// not as the commits it brought in (see MergedHeads).
//
// branch is any ref name git accepts (main, origin/main). since is a commit
// SHA, usually the tip the watcher saw last time.
//
// When since is empty (the first run), only the tip of the branch is returned,
// so that a first check does not flood the owner with old history. When since
// is not an ancestor of the branch, or does not exist any more, history was
// rewritten: only the tip is returned and WatchResult.Rewritten is true. When since
// is the tip, nothing is returned.
func NewCommits(ctx context.Context, repoDir, branch, since string) (WatchResult, error) {
	if err := checkRevArg("branch", branch); err != nil {
		return WatchResult{}, err
	}
	if err := checkRevArg("since", since); err != nil {
		return WatchResult{}, err
	}
	tip, err := git(ctx, repoDir, "rev-parse", "--verify", "--quiet", "--end-of-options", branch+"^{commit}")
	if err != nil {
		return WatchResult{}, fmt.Errorf("new commits: branch %q not found in %s: %w", branch, repoDir, err)
	}
	tip = strings.TrimSpace(tip)

	if since == "" {
		return tipOnly(ctx, repoDir, tip, false)
	}

	// Resolve since to a commit. A since that git cannot find is a rewrite
	// (the old commit was garbage collected), not a failure.
	sinceSHA, err := git(ctx, repoDir, "rev-parse", "--verify", "--quiet", "--end-of-options", since+"^{commit}")
	if err != nil {
		if isExit(err, 1) {
			return tipOnly(ctx, repoDir, tip, true)
		}
		return WatchResult{}, fmt.Errorf("new commits: resolve since %q: %w", since, err)
	}
	sinceSHA = strings.TrimSpace(sinceSHA)

	if _, err := git(ctx, repoDir, "merge-base", "--is-ancestor", sinceSHA, tip); err != nil {
		if isExit(err, 1) {
			return tipOnly(ctx, repoDir, tip, true)
		}
		return WatchResult{}, fmt.Errorf("new commits: check ancestry of %s: %w", sinceSHA, err)
	}

	out, err := git(ctx, repoDir, append(logArgs("--first-parent", "--reverse"), "--end-of-options", tip, "^"+sinceSHA)...)
	if err != nil {
		return WatchResult{}, fmt.Errorf("new commits: list commits: %w", err)
	}
	commits, err := parseLog(out)
	if err != nil {
		return WatchResult{}, err
	}
	return WatchResult{Commits: commits}, nil
}

func tipOnly(ctx context.Context, repoDir, tip string, rewritten bool) (WatchResult, error) {
	out, err := git(ctx, repoDir, append(logArgs("-1"), "--end-of-options", tip)...)
	if err != nil {
		return WatchResult{}, fmt.Errorf("new commits: read tip: %w", err)
	}
	commits, err := parseLog(out)
	if err != nil {
		return WatchResult{}, err
	}
	return WatchResult{Commits: commits, Rewritten: rewritten}, nil
}

// Record layout for git log: fields end with the unit separator, the
// trailers (which hold newlines) come last, and each record ends with the
// record separator.
const (
	fieldSep  = "\x1f"
	recordSep = "\x1e"
	// %ct is the committer time as a Unix timestamp. unfold joins wrapped
	// trailer values onto one line.
	logFormat = "%H%x1f%P%x1f%an <%ae>%x1f%cn <%ce>%x1f%ct%x1f%s%x1f%(trailers:only=true,unfold=true)%x1e"
)

func logArgs(extra ...string) []string {
	args := []string{"log", "--no-color", "--no-show-signature", "--format=" + logFormat}
	return append(args, extra...)
}

func parseLog(out string) ([]Commit, error) {
	var commits []Commit
	for _, rec := range strings.Split(out, recordSep) {
		rec = strings.TrimLeft(rec, "\r\n")
		if strings.TrimSpace(rec) == "" {
			continue
		}
		f := strings.SplitN(rec, fieldSep, 7)
		if len(f) != 7 {
			return nil, fmt.Errorf("new commits: cannot parse git log record %q", rec)
		}
		secs, err := strconv.ParseInt(f[4], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("new commits: bad commit time %q: %w", f[4], err)
		}
		commits = append(commits, Commit{
			SHA:       f[0],
			Parents:   strings.Fields(f[1]),
			Author:    f[2],
			Committer: f[3],
			Time:      time.Unix(secs, 0).UTC(),
			Subject:   f[5],
			Trailers:  parseTrailers(f[6]),
		})
	}
	return commits, nil
}

// parseTrailers reads the "Key: value" lines that git prints for the trailers
// of one commit. It returns an empty, non-nil map when there are none.
func parseTrailers(s string) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		key, value, ok := strings.Cut(line, ":")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			continue
		}
		if _, seen := m[key]; !seen {
			m[key] = strings.TrimSpace(value)
		}
	}
	return m
}

// checkRevArg refuses an argument git could read as an option. An empty value
// is allowed; the caller decides what empty means.
func checkRevArg(name, v string) error {
	if strings.HasPrefix(v, "-") {
		return fmt.Errorf("new commits: %s %q must not start with a dash", name, v)
	}
	if strings.ContainsAny(v, "\x00\n") {
		return fmt.Errorf("new commits: %s contains a control character", name)
	}
	return nil
}

func refExists(ctx context.Context, repoDir, ref string) (bool, error) {
	_, err := git(ctx, repoDir, "show-ref", "--verify", "--quiet", ref)
	if err == nil {
		return true, nil
	}
	if isExit(err, 1) {
		return false, nil
	}
	return false, fmt.Errorf("check ref %s: %w", ref, err)
}

// gitError carries the exit code of a failed git call so callers can tell
// "no" (exit 1) from "broken" (anything else).
type gitError struct {
	code   int
	stderr string
	err    error
}

func (e *gitError) Error() string {
	if e.stderr != "" {
		return fmt.Sprintf("git: %v: %s", e.err, e.stderr)
	}
	return fmt.Sprintf("git: %v", e.err)
}

func (e *gitError) Unwrap() error { return e.err }

func isExit(err error, code int) bool {
	var ge *gitError
	return errors.As(err, &ge) && ge.code == code
}

// git runs one git command in repoDir and returns its standard output.
// Hooks are switched off, the locale is fixed, and the call is bounded by
// watchGitTimeout or the context, whichever ends first.
func git(ctx context.Context, repoDir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, watchGitTimeout)
	defer cancel()

	full := append([]string{"--no-pager", "-C", repoDir, "-c", "core.hooksPath=/dev/null"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = append(os.Environ(),
		"LC_ALL=C",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		code := -1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			err = fmt.Errorf("%w (%v)", err, ctxErr)
			code = -1
		}
		return "", &gitError{code: code, stderr: strings.TrimSpace(stderr.String()), err: err}
	}
	return stdout.String(), nil
}

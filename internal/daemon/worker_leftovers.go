package daemon

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// harnessFolders are the folders where a coding harness keeps its files in the
// folder it runs in. An untracked empty file in one is the harness's own (the
// sandbox masks some paths there with empty files), not the worker's work.
var harnessFolders = []string{".claude", ".codex", ".opencode", ".pi", ".gemini"}

// harnessRuntimePaths are the files and folders Claude Code writes for itself
// in the folder it runs in. It lists most of them in a "claude-code-runtime"
// block of the repository's info/exclude, but a repository where that block is
// missing shows them as untracked, and an empty scheduled_tasks.json was once
// committed as a worker's leftover and recorded as its change's head.
var harnessRuntimePaths = []string{
	".claude/scheduled_tasks.json",
	".claude/scheduled_tasks.lock",
	".claude/settings.local.json",
	".claude/routines/.state",
	".claude/worktrees",
	".claude/checkpoints",
	".claude/mailbox",
	".claude/agent-registry.json",
	".claude/agent-memory-local",
	".claude/first-run",
	".claude/assistant-daemon-state.json",
	".claude/.cc-writes",
}

// pathIs tells whether a worktree-relative path is name, or is inside name, at
// the root or in a subfolder where the harness ran.
func pathIs(path, name string) bool {
	return path == name || strings.HasSuffix(path, "/"+name) ||
		strings.HasPrefix(path, name+"/") || strings.Contains(path, "/"+name+"/")
}

// inHarnessFolder tells whether a path is inside a harness folder.
func inHarnessFolder(path string) bool {
	for _, name := range harnessFolders {
		if strings.HasPrefix(path, name+"/") || strings.Contains(path, "/"+name+"/") {
			return true
		}
	}
	return false
}

// worktreeChange is one path that differs from HEAD in a worktree.
type worktreeChange struct {
	path      string
	untracked bool
	// staged is true when the index differs from HEAD at this path, so the
	// path is committed without an add.
	staged bool
}

// worktreeChanges lists the paths that differ from HEAD: staged, unstaged and
// untracked files, with untracked files .gitignore matches left out. A tracked
// file inside an ignored folder is still listed, so staging asks git which
// paths it will add (ignoredForAdd). Only these can be leftovers: a tracked
// file that is the same as in HEAD is not one, however large or binary it is.
func worktreeChanges(ctx context.Context, dir string) ([]worktreeChange, error) {
	out, err := gitOut(ctx, dir, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--no-renames")
	if err != nil {
		return nil, err
	}
	var changes []worktreeChange
	for _, entry := range strings.Split(out, "\x00") {
		if len(entry) < 4 {
			continue
		}
		changes = append(changes, worktreeChange{path: entry[3:], untracked: entry[:2] == "??", staged: entry[:2] != "??" && entry[0] != ' '})
	}
	return changes, nil
}

// notWorkerWork tells whether a changed path is a file the harness or its
// sandbox made for itself: an untracked Claude Code runtime file, an untracked
// empty file in a harness folder, or an untracked empty sandbox placeholder.
// Such a file is never committed and never named as the worker's leftover. A
// tracked or staged file is work, whatever its name.
func notWorkerWork(dir string, c worktreeChange) bool {
	if !c.untracked {
		return false
	}
	for _, name := range harnessRuntimePaths {
		if pathIs(c.path, name) {
			return true
		}
	}
	if !inHarnessFolder(c.path) && !isSandboxPlaceholderPath(c.path) {
		return false
	}
	info, err := os.Lstat(filepath.Join(dir, c.path))
	return err == nil && info.Mode().IsRegular() && info.Size() == 0
}

// workLeftovers is worktreeChanges without the files that are not the
// worker's work.
func workLeftovers(ctx context.Context, dir string) ([]worktreeChange, error) {
	changes, err := worktreeChanges(ctx, dir)
	if err != nil {
		return nil, err
	}
	kept := changes[:0]
	for _, c := range changes {
		if !notWorkerWork(dir, c) {
			kept = append(kept, c)
		}
	}
	return kept, nil
}

// hasWorkLeftovers tells whether the worktree has changes that are the
// worker's work.
func hasWorkLeftovers(ctx context.Context, dir string) (bool, error) {
	changes, err := workLeftovers(ctx, dir)
	return len(changes) > 0, err
}

// handbackHead is the branch and head a complete hand-back records.
type handbackHead struct {
	branch, head string
	// claimed says how the worker itself named this head: "pushed" when a
	// remote-tracking branch that the worker pushed points at it, "named"
	// when the report names it. A commit the daemon makes never replaces a
	// claimed head: the daemon keeps leftovers off the branch instead.
	claimed string
	// remote is the remote-tracking branch of a pushed head.
	remote string
}

// reportCommitPattern matches a word that can be an abbreviated or full commit
// hash.
var reportCommitPattern = regexp.MustCompile(`\b[0-9a-f]{7,64}\b`)

// pickHandbackHead finds the head a complete hand-back records. It is the head
// of the branch handbackBranch picks, as before, unless the worker claimed
// another head: one it pushed (a PR head) or one its report names. A claim
// counts for the branch head itself, or, when that branch holds no commit
// beyond the base, for a commit the worker had checked out in this worktree
// (its HEAD reflog) that is beyond the base and not on the default branch.
// That second case is a worker whose work is on another branch it pushed
// while its worktree went back to the start branch. A claimed head counts
// only when it holds commits beyond the base: a report or a push that names
// the base, or an ancestor of it, claims nothing.
func pickHandbackHead(ctx context.Context, dir, start, base, report string) (handbackHead, error) {
	branch := handbackBranch(ctx, dir, start, base)
	head, err := gitBranchHead(ctx, dir, branch)
	if err != nil {
		return handbackHead{branch: branch}, err
	}
	out := handbackHead{branch: branch, head: head}
	if !protocol.ValidCommit(base) {
		return out, nil
	}
	remotes := workerPushedHeads(ctx, dir)
	named := reportCommitPattern.FindAllString(report, -1)
	var defaults []string
	defaultsRead := false
	claim := func(sha string) (how, remote string) {
		if r := remotes[sha]; r != "" {
			how, remote = "pushed", r
		}
		for _, word := range named {
			if how == "" && strings.HasPrefix(sha, word) {
				how = "named"
			}
		}
		if how == "" {
			return "", ""
		}
		// Only a head with the worker's own commits is a claim. A report
		// often cites the commit it started from, or an earlier worker's
		// commit, and a push can name the base too: those hold no work.
		if !commitsBeyond(ctx, dir, base, sha) {
			return "", ""
		}
		if !defaultsRead {
			defaults, defaultsRead = defaultBranchRefs(ctx, dir), true
		}
		if commitOnAny(ctx, dir, sha, defaults) {
			return "", ""
		}
		return how, remote
	}
	if how, remote := claim(head); how != "" {
		out.claimed, out.remote = how, remote
		return out, nil
	}
	if commitsBeyond(ctx, dir, base, head) {
		return out, nil
	}
	reflog, err := gitOut(ctx, dir, "reflog", "show", "--format=%H", "HEAD")
	if err != nil {
		return out, nil
	}
	seen := map[string]bool{}
	for _, sha := range strings.Fields(reflog) {
		if seen[sha] || !protocol.ValidCommit(sha) {
			continue
		}
		seen[sha] = true
		how, remote := claim(sha)
		if how == "" {
			continue
		}
		if b := branchAt(ctx, dir, sha, remote); b != "" {
			return handbackHead{branch: b, head: sha, claimed: how, remote: remote}, nil
		}
	}
	return out, nil
}

// commitsBeyond tells whether head has commits that base does not.
func commitsBeyond(ctx context.Context, dir, base, head string) bool {
	n, err := gitOut(ctx, dir, "rev-list", "--count", base+".."+head, "--")
	return err == nil && n != "" && n != "0"
}

// defaultBranchRefs are the local and origin refs of the default branch that
// exist in the repository.
func defaultBranchRefs(ctx context.Context, dir string) []string {
	target := cleanupDefaultBranch(ctx, dir, "")
	if target == "" {
		return nil
	}
	var refs []string
	for _, ref := range []string{"refs/heads/" + target, "refs/remotes/origin/" + target} {
		if _, err := gitOut(ctx, dir, "rev-parse", "--verify", "--quiet", ref+"^{commit}"); err == nil {
			refs = append(refs, ref)
		}
	}
	return refs
}

// commitOnAny tells whether sha is in the history of one of refs. A commit
// already on the default branch is not the worker's work, even when a remote
// branch points at it (a pull of main).
func commitOnAny(ctx context.Context, dir, sha string, refs []string) bool {
	for _, ref := range refs {
		if _, err := gitOut(ctx, dir, "merge-base", "--is-ancestor", sha, ref); err == nil {
			return true
		}
	}
	return false
}

// agentNetPushedPrefix holds one ref for each branch the daemon pushed for a
// remote step owner, at the commit it pushed. Such a push is the daemon's, not
// a head the worker claimed by pushing it.
const agentNetPushedPrefix = "refs/agent-net/pushed/"

// workerPushedHeads maps each commit that a remote-tracking branch points at
// to that branch, leaving out the pushes the daemon made itself.
func workerPushedHeads(ctx context.Context, dir string) map[string]string {
	out, err := gitOut(ctx, dir, "for-each-ref", "--format=%(objectname) %(refname)", "refs/remotes/", agentNetPushedPrefix)
	if err != nil {
		return nil
	}
	daemon := map[string]string{}
	remotes := map[string]string{}
	var order []string
	for _, line := range strings.Split(out, "\n") {
		sha, ref, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		if branch, ok := strings.CutPrefix(ref, agentNetPushedPrefix); ok {
			daemon[branch] = sha
			continue
		}
		short := strings.TrimPrefix(ref, "refs/remotes/")
		if strings.HasSuffix(short, "/HEAD") {
			continue
		}
		remotes[short] = sha
		order = append(order, short)
	}
	pushed := map[string]string{}
	for _, short := range order {
		sha := remotes[short]
		_, branch, _ := strings.Cut(short, "/")
		if daemon[branch] == sha {
			continue
		}
		if _, ok := pushed[sha]; !ok {
			pushed[sha] = short
		}
	}
	return pushed
}

// markDaemonPush records that the daemon pushed branch at head, so a later
// hand-back does not take that push for one the worker made.
func markDaemonPush(ctx context.Context, dir, branch, head string) {
	_, _ = gitOut(ctx, dir, "update-ref", agentNetPushedPrefix+branch, head)
}

// branchAt names the branch a claimed head is on: a local branch that points
// at it, or else the branch name of the remote-tracking branch the worker
// pushed it to. "" when neither exists.
func branchAt(ctx context.Context, dir, sha, remote string) string {
	if out, err := gitOut(ctx, dir, "for-each-ref", "--points-at="+sha, "--format=%(refname:short)", "refs/heads/"); err == nil {
		if b, _, _ := strings.Cut(out, "\n"); b != "" {
			return b
		}
	}
	if _, b, ok := strings.Cut(remote, "/"); ok {
		return b
	}
	return ""
}

// leftoversRef is where the daemon keeps a worker's leftover changes when they
// must not go on the branch: the worker claimed its head.
func leftoversRef(workerID string) string {
	return "refs/agent-net/leftovers/" + workerID
}

// aside is the ref a worker's leftovers go to instead of its branch, or "" to
// commit them on the branch as usual. They go aside when the worker claimed a
// head (see pickHandbackHead), so the branch and the recorded change stay at
// the head the worker handed back.
func (d *Daemon) leftoversAside(ctx context.Context, w *worker, report string) string {
	d.wmu.Lock()
	dir, start, base, id := w.worktree, w.branch, w.base, w.id
	d.wmu.Unlock()
	if dir == "" || start == "" {
		return ""
	}
	pick, err := pickHandbackHead(ctx, dir, start, base, report)
	if err != nil || pick.claimed == "" {
		return ""
	}
	return leftoversRef(id)
}

// commitStagedAside makes a commit of the staged changes on top of HEAD and
// points ref at it. HEAD, the branch and the worktree do not move. It returns
// the short hash, or "" when nothing is staged.
func commitStagedAside(ctx context.Context, dir, ref, subject string) (string, error) {
	tree, err := gitOut(ctx, dir, "write-tree")
	if err != nil {
		return "", err
	}
	if headTree, err := gitOut(ctx, dir, "rev-parse", "HEAD^{tree}"); err == nil && headTree == tree {
		return "", nil
	}
	sha, err := gitOut(ctx, dir, "-c", "user.name=agent-net", "-c", "user.email=agent-net@localhost", "-c", "commit.gpgsign=false", "commit-tree", tree, "-p", "HEAD", "-m", subject)
	if err != nil {
		return "", err
	}
	if _, err := gitOut(ctx, dir, "update-ref", ref, sha); err != nil {
		return "", err
	}
	return gitOut(ctx, dir, "rev-parse", "--short", sha)
}

// withSavedIndex runs fn and then puts the index back as it was, so staging
// for a commit aside leaves the worker's own staging untouched.
func withSavedIndex(ctx context.Context, dir string, fn func() (string, error)) (string, error) {
	indexPath, err := gitOut(ctx, dir, "rev-parse", "--git-path", "index")
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(indexPath) {
		indexPath = filepath.Join(dir, indexPath)
	}
	before, readErr := os.ReadFile(indexPath)
	info, statErr := os.Stat(indexPath)
	out, err := fn()
	switch {
	case readErr == nil && statErr == nil:
		_ = os.WriteFile(indexPath, before, info.Mode().Perm())
	case os.IsNotExist(readErr):
		_ = os.Remove(indexPath)
	}
	return out, err
}

// claimText says how the worker claimed the head, for the notice: "" when it
// did not.
func (h handbackHead) claimText() string {
	switch h.claimed {
	case "pushed":
		return "the head you pushed (" + h.remote + ")"
	case "named":
		return "the head your report names"
	}
	return ""
}

// shortHash is the first 7 characters of a full commit hash.
func shortHash(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

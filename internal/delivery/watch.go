package delivery

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// This file is the git side of the default-branch watcher. It finds the
// commits that landed on a repository's default branch since the last check,
// so that the caller can flag the ones that skipped the delivery pipeline.
//
// It fetches the branch from the remote into its remote-tracking ref and
// reads that ref by its full name. It reads commits as raw objects (git
// rev-list, then git cat-file --batch), so nothing in a message or a name can
// shift a field. The fetch is the only write to the repository.

// Commit is one commit on the default branch.
type Commit struct {
	SHA     string
	Parents []string
	// Author and Committer are "Name <email>".
	Author    string
	Committer string
	// Subject is the first paragraph of the message on one line, as git's
	// %s prints it.
	Subject string
	// Time is the committer time (when the commit landed), in UTC.
	Time time.Time
	// Trailers holds the git trailers of the message, keyed by trailer name
	// as written. The names agent-net uses are set by the delivery.trailer_*
	// settings, so no name is fixed here. If a name appears twice, the first
	// value wins. Look names up with Trailer, which ignores case.
	Trailers map[string]string
	// ParseError is set when the commit object could not be read. Such a
	// commit has no parents and no trailers, so it carries no evidence: the
	// caller counts it as a skip of the pipeline.
	ParseError string
}

// IsMerge reports whether the commit has more than one parent.
func (c Commit) IsMerge() bool { return len(c.Parents) > 1 }

// Trailer returns the value of the trailer name, ignoring case as git does,
// or "" when the commit has no such trailer.
func (c Commit) Trailer(name string) string {
	if v, ok := c.Trailers[name]; ok {
		return v
	}
	for k, v := range c.Trailers {
		if strings.EqualFold(k, name) {
			return v
		}
	}
	return ""
}

// MergedHeads returns the branch heads a merge commit brought in: its second
// and later parents. Evidence is keyed by the head commit of a change, not by
// the merge commit. It returns nil for a commit that is not a merge.
func MergedHeads(c Commit) []string {
	if len(c.Parents) < 2 {
		return nil
	}
	return append([]string(nil), c.Parents[1:]...)
}

// WatchRequest says which branch to watch.
type WatchRequest struct {
	// RepoDir is a repository that has Remote. It should be a clone the
	// daemon owns for watching, not a checkout a worker writes: the fetch
	// reads its config, and the check of that config cannot close a race
	// with a writer.
	RepoDir string
	// Remote is the name of the remote, for example "origin". It names the
	// remote-tracking ref.
	Remote string
	// RemoteURL is required: the URL of Remote from the caller's own record.
	// The fetch uses it and not the URL in the repository's config, and
	// NewCommits refuses (ErrRemoteURL) when the repository's fetch or push
	// URL for Remote differs.
	RemoteURL string
	// Branch is the default branch, as the gateway gives it (the same one
	// cleanup uses). The watcher does not guess it.
	Branch string
	// Since is the full commit ID of the tip the watcher saw last time, or
	// empty on the first run.
	Since string
	// GitPath is the PATH list git is found on and runs with (see
	// MergeRequest.GitPath). Empty means this process's PATH.
	GitPath string
}

// WatchResult is what NewCommits found.
type WatchResult struct {
	// Commits are the new commits on the branch's first-parent line, oldest
	// first.
	Commits []Commit
	// Rewritten is true when Since is not an ancestor of the branch, which
	// means history was rewritten (or Since no longer exists). Commits then
	// holds only the tip, because the real set of new commits is unknowable.
	Rewritten bool
}

// NewCommits fetches req.Branch from req.Remote and returns the commits
// reachable from refs/remotes/<Remote>/<Branch> and not from req.Since,
// following first parents only, oldest first. A merge shows up as one commit,
// not as the commits it brought in (see MergedHeads).
//
// When Since is empty (the first run), only the tip of the branch is
// returned, so that a first check does not flood the owner with old history.
// When Since is not an ancestor of the branch, or does not exist any more,
// history was rewritten: only the tip is returned and WatchResult.Rewritten
// is true. When Since is the tip, nothing is returned.
//
// A commit that cannot be parsed is returned with ParseError set; it never
// stops the watcher. A repository with replace refs or a grafts file is
// refused with ErrAlteredHistory.
func NewCommits(ctx context.Context, req WatchRequest) (WatchResult, error) {
	if req.RepoDir == "" {
		return WatchResult{}, errors.New("new commits: RepoDir is required")
	}
	if req.Since != "" {
		if err := checkCommitID("new commits: since", req.Since); err != nil {
			return WatchResult{}, err
		}
	}
	g := gitIn{gitPath: req.GitPath, dir: req.RepoDir}
	if err := checkRefName(ctx, g, "remote", req.Remote); err != nil {
		return WatchResult{}, fmt.Errorf("new commits: %w", err)
	}
	if err := checkRefName(ctx, g, "branch", req.Branch); err != nil {
		return WatchResult{}, fmt.Errorf("new commits: %w", err)
	}
	if err := checkRepo(ctx, g); err != nil {
		return WatchResult{}, err
	}
	if err := checkRemoteURL(ctx, g, req.Remote, req.RemoteURL); err != nil {
		return WatchResult{}, fmt.Errorf("new commits: %w", err)
	}

	ref := "refs/remotes/" + req.Remote + "/" + req.Branch
	if err := fetchRefspec(ctx, g, req.RemoteURL, "+refs/heads/"+req.Branch+":"+ref); err != nil {
		return WatchResult{}, fmt.Errorf("new commits: %w", err)
	}
	tip, err := revCommit(ctx, g, ref)
	if err != nil {
		return WatchResult{}, fmt.Errorf("new commits: read %s: %w", ref, err)
	}

	if req.Since == "" {
		return tipOnly(ctx, g, tip, false)
	}
	// A since that git cannot find is a rewrite (the old commit was garbage
	// collected), not a failure.
	if _, err := revCommit(ctx, g, req.Since); err != nil {
		if isExit(err, 1) {
			return tipOnly(ctx, g, tip, true)
		}
		return WatchResult{}, fmt.Errorf("new commits: resolve since %s: %w", req.Since, err)
	}
	ok, err := isAncestor(ctx, g, req.Since, tip)
	if err != nil {
		return WatchResult{}, fmt.Errorf("new commits: check ancestry of %s: %w", req.Since, err)
	}
	if !ok {
		return tipOnly(ctx, g, tip, true)
	}

	out, err := g.run(ctx, "rev-list", "--first-parent", "--reverse", tip, "^"+req.Since)
	if err != nil {
		return WatchResult{}, fmt.Errorf("new commits: list commits: %w", err)
	}
	commits, err := readCommits(ctx, g, strings.Fields(out))
	if err != nil {
		return WatchResult{}, err
	}
	return WatchResult{Commits: commits}, nil
}

func tipOnly(ctx context.Context, g gitIn, tip string, rewritten bool) (WatchResult, error) {
	commits, err := readCommits(ctx, g, []string{tip})
	if err != nil {
		return WatchResult{}, err
	}
	return WatchResult{Commits: commits, Rewritten: rewritten}, nil
}

// readCommits reads the commits as raw objects with one git cat-file --batch.
// Its output is length-prefixed, so no content can be mistaken for a
// separator.
func readCommits(ctx context.Context, g gitIn, ids []string) ([]Commit, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	for _, id := range ids {
		if !IsCommitID(id) {
			return nil, fmt.Errorf("new commits: git rev-list printed %q", id)
		}
	}
	out, err := g.bytes(ctx, []byte(strings.Join(ids, "\n")+"\n"), "cat-file", "--batch")
	if err != nil {
		return nil, fmt.Errorf("new commits: read commits: %w", err)
	}
	commits := make([]Commit, 0, len(ids))
	for _, id := range ids {
		nl := bytes.IndexByte(out, '\n')
		if nl < 0 {
			return nil, fmt.Errorf("new commits: git cat-file output ends before %s", id)
		}
		head := strings.Fields(string(out[:nl]))
		out = out[nl+1:]
		switch {
		case len(head) == 2 && head[0] == id && head[1] == "missing":
			commits = append(commits, skipCommit(id, "commit object is missing"))
		case len(head) == 3 && head[0] == id:
			size, err := strconv.Atoi(head[2])
			if err != nil || size < 0 || size+1 > len(out) {
				return nil, fmt.Errorf("new commits: bad git cat-file header %q", head)
			}
			raw := out[:size]
			out = out[size+1:] // the object is followed by a newline
			if head[1] != "commit" {
				commits = append(commits, skipCommit(id, "object is a "+head[1]+", not a commit"))
				continue
			}
			commits = append(commits, parseCommitObject(id, raw))
		default:
			return nil, fmt.Errorf("new commits: unexpected git cat-file header %q for %s", head, id)
		}
	}
	return commits, nil
}

func skipCommit(id, why string) Commit {
	return Commit{SHA: id, Trailers: map[string]string{}, ParseError: why}
}

// parseCommitObject reads a raw commit object: header lines ("tree",
// "parent", "author", "committer", and others such as "gpgsig" whose
// continuation lines start with a space), an empty line, then the message.
func parseCommitObject(id string, raw []byte) Commit {
	header, msg, _ := strings.Cut(string(raw), "\n\n")
	c := Commit{SHA: id}
	var tree, author, committer bool
	for _, line := range strings.Split(header, "\n") {
		key, val, _ := strings.Cut(line, " ")
		switch key {
		case "tree":
			tree = IsCommitID(val) // a tree ID has the same form
		case "parent":
			if !IsCommitID(val) {
				return skipCommit(id, "bad parent line")
			}
			c.Parents = append(c.Parents, val)
		case "author":
			if author {
				continue
			}
			ident, _, ok := parseIdent(val)
			if !ok {
				return skipCommit(id, "bad author line")
			}
			c.Author, author = ident, true
		case "committer":
			if committer {
				continue
			}
			ident, when, ok := parseIdent(val)
			if !ok {
				return skipCommit(id, "bad committer line")
			}
			c.Committer, c.Time, committer = ident, when, true
		}
	}
	switch {
	case !tree:
		return skipCommit(id, "no tree line")
	case !author:
		return skipCommit(id, "no author line")
	case !committer:
		return skipCommit(id, "no committer line")
	}
	msg = strings.ToValidUTF8(msg, "�")
	c.Subject = commitSubject(msg)
	c.Trailers = parseTrailers(msg)
	return c
}

// parseIdent reads "Name <email> 1700000000 +0000" into "Name <email>" and
// the time in UTC.
func parseIdent(v string) (string, time.Time, bool) {
	end := strings.LastIndexByte(v, '>')
	if end < 0 || !strings.Contains(v[:end], "<") {
		return "", time.Time{}, false
	}
	f := strings.Fields(v[end+1:])
	if len(f) != 2 {
		return "", time.Time{}, false
	}
	secs, err := strconv.ParseInt(f[0], 10, 64)
	if err != nil {
		return "", time.Time{}, false
	}
	if len(f[1]) != 5 || (f[1][0] != '+' && f[1][0] != '-') {
		return "", time.Time{}, false
	}
	if _, err := strconv.Atoi(f[1][1:]); err != nil {
		return "", time.Time{}, false
	}
	return strings.ToValidUTF8(v[:end+1], "�"), time.Unix(secs, 0).UTC(), true
}

// commitSubject returns the first paragraph of msg on one line.
func commitSubject(msg string) string {
	lines := splitLines(msg)
	for len(lines) > 0 && isBlank(lines[0]) {
		lines = lines[1:]
	}
	var parts []string
	for _, l := range lines {
		if isBlank(l) {
			break
		}
		parts = append(parts, strings.TrimSpace(l))
	}
	return strings.Join(parts, " ")
}

// gitGeneratedTrailers start lines that git itself writes as trailers.
var gitGeneratedTrailers = []string{"Signed-off-by: ", "(cherry picked from commit "}

// parseTrailers reads the trailer block of a commit message the way git
// interpret-trailers finds it. The block is the last paragraph, never the
// subject, and ends at a "---" line. Every line in it must be a trailer
// ("Key: value", the key made of letters, digits and dashes) or the
// continuation of one (a line that starts with white space, folded into the
// value). A block that also holds other lines still counts when at least a
// quarter of its lines are trailers and one of them is a line git writes
// itself (Signed-off-by, cherry picked from); its other lines are left out.
// Comment lines (#) are skipped. It returns an empty, non-nil map when there
// are no trailers.
func parseTrailers(msg string) map[string]string {
	m := map[string]string{}
	lines := splitLines(msg)
	for i, l := range lines {
		if l == "---" || strings.HasPrefix(l, "--- ") || strings.HasPrefix(l, "---\t") {
			lines = lines[:i]
			break
		}
	}
	for len(lines) > 0 && isBlank(lines[len(lines)-1]) {
		lines = lines[:len(lines)-1]
	}
	// The subject paragraph never holds trailers.
	title := 0
	for title < len(lines) && (isBlank(lines[title]) || strings.HasPrefix(lines[title], "#")) {
		title++
	}
	for title < len(lines) && !isBlank(lines[title]) {
		title++
	}
	start := -1
	for i := len(lines) - 1; i >= title; i-- {
		if isBlank(lines[i]) {
			start = i + 1
			break
		}
	}
	if start < 0 || start >= len(lines) {
		return m
	}
	block := lines[start:]

	trailers, others, recognized := 0, 0, false
	for _, l := range block {
		switch {
		case strings.HasPrefix(l, "#"):
		case startsWithAny(l, gitGeneratedTrailers):
			trailers++
			recognized = true
		case trailerKeyEnd(l) > 0:
			trailers++
		case l[0] == ' ' || l[0] == '\t':
		default:
			others++
		}
	}
	if trailers == 0 || (others > 0 && !(recognized && trailers*3 >= others)) {
		return m
	}

	last := ""
	for _, l := range block {
		switch {
		case strings.HasPrefix(l, "#"):
			continue
		case l[0] == ' ' || l[0] == '\t':
			if last != "" {
				m[last] = strings.TrimSpace(m[last] + " " + strings.TrimSpace(l))
			}
			continue
		}
		last = ""
		end := trailerKeyEnd(l)
		if end <= 0 {
			continue
		}
		key := strings.TrimSpace(l[:end])
		if _, seen := m[key]; seen {
			continue
		}
		m[key] = strings.TrimSpace(l[end+1:])
		last = key
	}
	return m
}

// trailerKeyEnd returns the index of the ':' that ends a trailer key at the
// start of line, or -1. The key is letters, digits and dashes, and may be
// followed by white space before the colon.
func trailerKeyEnd(line string) int {
	space := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case c == ':':
			if i == 0 {
				return -1
			}
			return i
		case !space && (c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-'):
		case i > 0 && (c == ' ' || c == '\t'):
			space = true
		default:
			return -1
		}
	}
	return -1
}

func splitLines(s string) []string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSuffix(l, "\r")
	}
	return lines
}

func isBlank(s string) bool { return strings.TrimSpace(s) == "" }

func startsWithAny(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

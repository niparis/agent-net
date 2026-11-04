package delivery

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// This file guards the package's git calls against repository config that
// runs a program or sends git somewhere else.
//
// The threat is a repository whose own config (.git/config, a config.worktree
// file, or a file they include) was written by someone who is not the owner,
// for example a worker that edits the config of its worktree: the config of
// all worktrees of a repository is one file. Git runs programs named in that
// config: filter drivers on checkout, reset and status, merge drivers during
// a merge, ssh and askpass and credential helpers during fetch and push. It
// also lets config rewrite the remote URL.
//
// Design: refuse, do not neutralise. Before any step that checks out, merges,
// fetches or pushes, checkConfig lists every config value git would read,
// with its scope, and refuses the whole call when a repository-level value
// (scope local or worktree) is a key that can run a program or redirect the
// remote. Overriding keys with -c instead would have to name every key, and a
// filter or merge driver can have any name. A refusal names the key, so the
// owner can fix the repository. The one override that stays is
// protocol.ext.allow=never in gitFixedArgs, because it has one fixed name.
//
// The system and global config are the owner's and stay trusted: a worker
// cannot write them, and a push over https needs the owner's credential
// helper, which lives there. The "command" scope is the -c flags of this
// package. Anything with another scope is treated as repository level.
//
// What the check cannot cover: a config written between the check and the git
// call that reads it. Merge checks again just before the worktree is changed
// and just before the push, so the window is the time of one git call.
// .gitattributes in the tree can only select a driver by name, and a driver
// with that name exists only if the owner's global config defines it.

// ErrUnsafeConfig: the repository config sets a key that can run a program or
// redirect the remote. Nothing was run.
var ErrUnsafeConfig = errors.New("repository config can run programs or redirect the remote")

// checkRepo is the check every entry point makes first: no altered history,
// and no repository config that runs programs.
func checkRepo(ctx context.Context, g gitIn) error {
	if err := checkHistory(ctx, g); err != nil {
		return err
	}
	return checkConfig(ctx, g)
}

// checkConfig refuses when the repository config in g.dir holds an unsafe key.
func checkConfig(ctx context.Context, g gitIn) error {
	out, err := g.bytes(ctx, nil, "config", "--list", "--show-scope", "--includes", "-z")
	if err != nil {
		return fmt.Errorf("read git config of %s: %w", g.dir, err)
	}
	// The output is scope NUL entry NUL scope NUL entry NUL ..., and an
	// entry is "key\nvalue", or only "key" for a key without a value.
	parts := strings.Split(string(out), "\x00")
	var bad []string
	for i := 0; i+1 < len(parts); i += 2 {
		scope := parts[i]
		switch scope {
		case "system", "global", "command":
			continue
		}
		key, value, _ := strings.Cut(parts[i+1], "\n")
		if why := unsafeConfigKey(key, value); why != "" {
			bad = append(bad, fmt.Sprintf("%s (%s config: %s)", key, scope, why))
		}
	}
	if len(bad) == 0 {
		return nil
	}
	if len(bad) > 5 {
		bad = append(bad[:5], fmt.Sprintf("and %d more", len(bad)-5))
	}
	return fmt.Errorf("%w: %s: %s. Remove them from the repository config; keep what the owner needs in the global git config",
		ErrUnsafeConfig, g.dir, strings.Join(bad, "; "))
}

// unsafeConfigKey says why a repository-level key is refused, or returns ""
// when it is fine. key is as git prints it: the section and the variable name
// are lower case, the subsection keeps its case and may hold dots.
func unsafeConfigKey(key, value string) string {
	section, rest, _ := strings.Cut(key, ".")
	section = strings.ToLower(section)
	name := strings.ToLower(rest)
	if i := strings.LastIndexByte(rest, '.'); i >= 0 {
		name = strings.ToLower(rest[i+1:])
	}
	hasSub := strings.Contains(rest, ".")
	switch section {
	case "filter":
		if name == "smudge" || name == "clean" || name == "process" {
			return "filter driver runs a program on checkout"
		}
	case "merge":
		if name == "driver" {
			return "merge driver runs a program"
		}
	case "diff":
		if name == "external" || name == "command" || name == "textconv" {
			return "diff driver runs a program"
		}
	case "core":
		switch name {
		case "sshcommand", "askpass", "gitproxy":
			return "runs a program"
		}
	case "url":
		if hasSub && (name == "insteadof" || name == "pushinsteadof") {
			return "rewrites the remote URL"
		}
	case "credential":
		return "credential helper or credential redirect"
	case "gpg":
		if name == "program" {
			return "signing program"
		}
	case "protocol":
		return "changes which transports git may run"
	case "http":
		return "changes proxy, certificates or headers of http remotes"
	case "lfs":
		return "redirects LFS transfers"
	case "remote":
		switch name {
		case "vcs", "uploadpack", "receivepack":
			return "runs a program for the remote"
		case "proxy":
			return "sends the remote through a proxy"
		}
	case "submodule":
		if name == "update" && strings.HasPrefix(value, "!") {
			return "runs a command"
		}
	}
	return ""
}

// ErrRemoteURL: the remote's URL is not the one the caller expects.
var ErrRemoteURL = errors.New("remote URL is not the expected one")

// checkRemoteURL refuses unless every fetch URL and every push URL of the
// remote, as git resolves them, equals want. A caller that knows the URL of
// its remote (from the project record, not from the repository) passes it so
// a changed remote.<name>.url or pushurl cannot send the push elsewhere.
func checkRemoteURL(ctx context.Context, g gitIn, remote, want string) error {
	if want == "" {
		return nil
	}
	for _, push := range []bool{false, true} {
		args := []string{"remote", "get-url", "--all"}
		what := "fetch"
		if push {
			args = append(args, "--push")
			what = "push"
		}
		out, err := g.run(ctx, append(args, remote)...)
		if err != nil {
			return fmt.Errorf("read the %s URL of %s: %w", what, remote, err)
		}
		for _, u := range strings.Split(out, "\n") {
			if u != want {
				return fmt.Errorf("%w: %s URL of %s is %q, want %q", ErrRemoteURL, what, remote, redactURL(u), redactURL(want))
			}
		}
	}
	return nil
}

// redactURL drops the user info of a URL, so a password in a remote does not
// reach an error message.
func redactURL(u string) string {
	if i := strings.Index(u, "://"); i >= 0 {
		rest := u[i+3:]
		host, _, _ := strings.Cut(rest, "/")
		if at := strings.LastIndexByte(host, '@'); at >= 0 {
			return u[:i+3] + rest[at+1:]
		}
	}
	return u
}

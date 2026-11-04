package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/niparis/agent-net/internal/claudetrust"
	"github.com/niparis/agent-net/internal/protocol"
)

// Paths typed in the Project field of the New session form
// (docs/design/start-sessions.md, build plan part 10). A path is checked on
// the machine that runs the start, including a start relayed from another
// machine. It is cleaned, must be absolute or start with ~, must hold no
// control characters, and must stay inside the home folder of the user of
// that machine. A folder is created only after the owner said yes.

// cleanOwnerPath cleans a typed path and applies the home-folder rule.
func cleanOwnerPath(raw string) (string, error) {
	for _, r := range raw {
		if unicode.IsControl(r) {
			return "", errors.New("the path has a control character")
		}
	}
	path, err := expandPath(raw)
	if err != nil {
		return "", err
	}
	if err := checkOwnerHome(path); err != nil {
		return "", err
	}
	return path, nil
}

// ownerHome is the real home folder of the user the daemon runs as.
func ownerHome() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", errors.New("cannot find the home folder")
	}
	if real, err := filepath.EvalSymlinks(home); err == nil {
		home = real
	}
	return filepath.Clean(home), nil
}

// insideFolder reports whether path is root or below it.
func insideFolder(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// checkOwnerHome refuses a path that is not inside the user's home folder,
// including one that reaches outside it through a symbolic link. A missing
// path is checked through its nearest existing parent, so a folder cannot be
// created through a link that leaves home.
func checkOwnerHome(path string) error {
	home, err := ownerHome()
	if err != nil {
		return err
	}
	outside := fmt.Errorf("the path is outside your home folder: %s", path)
	if !insideFolder(home, path) {
		return outside
	}
	if _, err := os.Lstat(path); err == nil {
		real, err := filepath.EvalSymlinks(path)
		if err != nil {
			return err
		}
		if !insideFolder(home, real) {
			return outside
		}
		return nil
	}
	parent := filepath.Dir(path)
	for {
		if _, err := os.Lstat(parent); err == nil {
			real, err := filepath.EvalSymlinks(parent)
			if err != nil {
				return err
			}
			if !insideFolder(home, real) {
				return outside
			}
			return nil
		}
		next := filepath.Dir(parent)
		if next == parent {
			return outside
		}
		parent = next
	}
}

// createOwnerFolder makes the owner's new folder and runs git init in it. It
// never uses a shell: mkdir and git get plain arguments.
func createOwnerFolder(ctx context.Context, path string) error {
	if err := checkOwnerHome(path); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("cannot create parent of %s: %w", path, err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%s already exists; choose another name or ask to register it", path)
		}
		return fmt.Errorf("cannot create %s: %w", path, err)
	}
	if _, err := gitOut(ctx, path, "init", "-q"); err != nil {
		return fmt.Errorf("cannot run git init in %s: %w", path, err)
	}
	return nil
}

// ownerPathProject registers a typed folder that is a git working tree and
// returns its main tree and name. A folder that is not a repository starts
// where it is; only a folder agent-net created must be a repository.
func (d *Daemon) ownerPathProject(ctx context.Context, path, harness string, created bool) (string, string, error) {
	entry, err := d.repo.Inspect(path)
	if err != nil {
		if created {
			return "", "", fmt.Errorf("cannot register %s: %w", path, err)
		}
		return path, filepath.Base(path), nil
	}
	_, err = d.call(ctx, "project.add", protocol.ProjectAdd{
		Projects:       []protocol.ProjectEntry{entry},
		AddedBySession: d.ownerSessionID(),
		AddedByHarness: harness,
	})
	if err != nil {
		return "", "", oldGateway(err)
	}
	return entry.Path, entry.Name, nil
}

// ownerClaudeTrust applies the trust rule for a typed path. A folder agent-net
// created is trusted at once. An existing folder needs the owner's yes: the
// caller gets needs=true and asks, then sends the request again with
// TrustClaude. A new worktree was created by agent-net, so it is trusted.
func ownerClaudeTrust(path string, created, worktree bool, confirmed bool) (bool, error) {
	if created || worktree {
		if err := claudetrust.Set(path); err != nil {
			return false, fmt.Errorf("cannot trust %s for Claude Code: %w", path, err)
		}
		return false, nil
	}
	trusted, err := claudetrust.Trusted(path)
	if err != nil {
		return false, fmt.Errorf("cannot read Claude Code trust for %s: %w", path, err)
	}
	if trusted {
		return false, nil
	}
	if !confirmed {
		return true, nil
	}
	if err := claudetrust.Set(path); err != nil {
		return false, fmt.Errorf("cannot trust %s for Claude Code: %w", path, err)
	}
	return false, nil
}

var projectRemotePattern = regexp.MustCompile(`^(?:git@[A-Za-z0-9][A-Za-z0-9.-]*:|https://[A-Za-z0-9][A-Za-z0-9.-]*(?::[0-9]{1,5})?/)[A-Za-z0-9_-][A-Za-z0-9_.-]*/[A-Za-z0-9_-][A-Za-z0-9_.-]*[.]git$`)

func validateProjectRemote(remote string) error {
	if len(remote) > 2048 || !projectRemotePattern.MatchString(remote) {
		return errors.New("remote must be git@host:owner/repo.git or https://host/owner/repo.git")
	}
	return nil
}

func (d *Daemon) cloneOwnerProject(ctx context.Context, remote, path string) error {
	if err := validateProjectRemote(remote); err != nil {
		return err
	}
	if err := checkOwnerHome(path); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("cannot create parent of %s: %w", path, err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%s already exists; choose another name or ask to register it", path)
		}
		return fmt.Errorf("cannot create %s: %w", path, err)
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if d.cloneProject != nil {
		return d.cloneProject(ctx, remote, path)
	}
	_, err := gitOutWithEnv(ctx, filepath.Dir(path), []string{"GIT_ALLOW_PROTOCOL=ssh:https"}, "-c", "protocol.file.allow=never", "-c", "protocol.ext.allow=never", "clone", "--", remote, path)
	if err != nil {
		return fmt.Errorf("cannot clone %s: %w", remote, err)
	}
	return nil
}

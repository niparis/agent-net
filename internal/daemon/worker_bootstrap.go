package daemon

import (
	"context"
)

// setWorktreeUpstream records origin/<branch> as the upstream of a worker's
// branch, so a plain `git push` works. The worker's sandbox keeps the shared
// .git read-only, and `git push -u` writes the upstream into its config, so
// the daemon, which can write it, does it at the start. Nothing is written
// when the project has no origin.
func setWorktreeUpstream(ctx context.Context, main, branch string) error {
	if _, err := gitOut(ctx, main, "config", "--get", "remote.origin.url"); err != nil {
		return nil
	}
	if _, err := gitOut(ctx, main, "config", "branch."+branch+".remote", "origin"); err != nil {
		return err
	}
	_, err := gitOut(ctx, main, "config", "branch."+branch+".merge", "refs/heads/"+branch)
	return err
}

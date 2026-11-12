package daemon

import (
	"context"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// workerBriefRepo reads the same base tree used by createWorktree. If it cannot
// be read, the gateway skips overlap checks rather than adding a start refusal.
func (d *Daemon) workerBriefRepo(ctx context.Context, p protocol.WorkerBriefRepoRequest) (protocol.WorkerBriefRepo, error) {
	var repo protocol.WorkerBriefRepo
	root, err := gitOut(ctx, p.Path, "rev-parse", "--show-toplevel")
	if err != nil {
		return repo, err
	}
	repo.Root = strings.TrimSpace(root)
	ref := "HEAD"
	if p.Base != "" {
		if _, err := gitOut(ctx, repo.Root, "check-ref-format", "--branch", p.Base); err != nil {
			return repo, err
		}
		ref = "refs/heads/" + p.Base
	}
	base, err := gitOut(ctx, repo.Root, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return repo, err
	}
	if p.Base == "" {
		base = d.freshUpstreamBase(ctx, repo.Root, base)
	}
	files, err := gitOut(ctx, repo.Root, "ls-tree", "-r", "--name-only", "-z", base)
	if err != nil {
		return repo, err
	}
	repo.Files = strings.Split(strings.TrimSuffix(files, "\x00"), "\x00")
	return repo, nil
}

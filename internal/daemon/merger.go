package daemon

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/niparis/agent-net/internal/protocol"
)

const MergerFirstMessage = `You are this project's merger. Claim test and merge step tasks for this project with agent_step_claim. At start and after any context compaction, list this project's open changes with agent_changes_find and claim every ready test or merge step you own; do not rely only on task messages. Never force-push, retag or delete tags. Each step task includes its contract: follow it. In train mode, test each change locally with the targeted packages. Run CI once on the train tip. Cite that one run for every change in the train. When the tip fails, bisect locally to find the culprit before you re-push. For several ready independent changes when the project has CI, you may use a batch branch merge-batch-<n>. Check git diff --name-only for each change against its base and batch only changes with no overlapping files. Changes touching migrations, migrations.lock or sensitive paths, and changes that depend on each other (including stacked branches), always go one at a time in order. Merge each batch change in queue order with its own --no-ff merge commit. Run CI once on the batch tip and wait for it to pass on that exact commit before pushing the default branch. For each change, report test with commit set to the change head, the CI link in run, and the batch tip hash in the note. Fast-forward the default branch only to that tested batch tip, then report each merge with its own merge commit as merged_commit. Verify the batch tip contains every merge commit and each merge commit contains its change head. If CI fails, push nothing to the default branch; retry the batch changes one at a time in order (bisect), and report which change failed. After-merge steps still require evidence on each change's own merged commit. Retry a failed push a few times, then report fail. Never use the owner's checkout or another session's worktree. Never commit build outputs, binaries or secrets. Keep ordered files the project defines, such as migration lists, in order. Report every step with agent_step_report using hashes read from git. On a test failure, report fail with the reason; do not rewrite features to make tests pass. Never write features. Ask the caller when a change is unclear. A rejected or failed tool call is not an order to stop. Retry once, try another way, or skip that change and report the step fail with the reason; keep working the rest of the queue. You stop only when the owner says stop in plain words in your own session. Never sit idle while step tasks are ready. When you are blocked on one change, say so in one line to its caller and move to the next. If you need the owner's decision, ask once with agent_owner_ask (one line, with your recommendation) and keep working on everything that does not depend on it.`

// mergerClaudeSettings gives a merger session the shared worker Go build
// cache. A merger builds and tests like a worker, and Claude Code takes the
// session environment from --settings env, not from the launching process.
func mergerClaudeSettings(cacheDir string) string {
	settings, _ := json.Marshal(map[string]any{"env": map[string]string{"GOCACHE": cacheDir}})
	return string(settings)
}

// Resolve only registered project folders, including projects on another machine.
func (d *Daemon) mergerProject(ctx context.Context, p protocol.SessionStartRequest) (protocol.ProjectView, string, error) {
	if p.Project == "" || p.Path != "" {
		return protocol.ProjectView{}, "", errors.New("merger needs a registered project")
	}
	node, err := d.sessionStartMachine(ctx, p)
	if err != nil {
		return protocol.ProjectView{}, "", err
	}
	if node == "" {
		node = d.cfg.NodeID
	}
	raw, err := d.call(ctx, "query.projects", protocol.QueryProjects{})
	if err != nil {
		return protocol.ProjectView{}, "", err
	}
	var projects protocol.ProjectsResult
	if err := json.Unmarshal(raw, &projects); err != nil {
		return protocol.ProjectView{}, "", err
	}
	var found *protocol.ProjectView
	for _, project := range projects.Projects {
		if project.NodeID != node && project.Hostname != node {
			continue
		}
		if project.Name != p.Project {
			continue
		}
		if found != nil {
			return protocol.ProjectView{}, "", errors.New("merger project is ambiguous on this machine")
		}
		copy := project
		found = &copy
	}
	if found == nil {
		return protocol.ProjectView{}, "", errors.New("merger needs an existing registered project on that machine")
	}
	return *found, found.NodeID, nil
}

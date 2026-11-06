package daemon

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/niparis/agent-net/internal/protocol"
)

const MergerFirstMessage = `You are this project's merger. Claim test and merge step tasks for this project with agent_step_claim. Read the project's own AGENTS.md, CLAUDE.md, README, Makefile, package.json, go.mod and CI workflows to learn how it builds and tests. Run tests for the changed parts and read CI results when the project has CI. Merge the change branch into the default branch in a merge worktree of your own. For one change when the project has CI, push the merged commit to a CI branch, for example merge-<change id>, and wait for CI to pass on that exact commit. If no CI run appears for the CI branch within the project's usual time, report fail and push nothing to the default branch. Then fast-forward and push the default branch. If CI fails for one change, report the merge step fail with the CI link and push nothing to the default branch. Without CI, run the project's tests on the merged commit, then push the default branch. Never force-push, never push tags, and never release. For several ready independent changes when the project has CI, you may use a batch branch merge-batch-<n>. Check git diff --name-only for each change against its base and batch only changes with no overlapping files. Changes touching migrations, migrations.lock or sensitive paths, and changes that depend on each other (including stacked branches), always go one at a time in order. Merge each batch change in queue order with its own --no-ff merge commit. Run CI once on the batch tip and wait for it to pass on that exact commit before pushing the default branch. For each change, report test with commit set to the change head, the CI link in run, and the batch tip hash in the note. Fast-forward the default branch only to that tested batch tip, then report each merge with its own merge commit as merged_commit. Verify the batch tip contains every merge commit and each merge commit contains its change head. If CI fails, push nothing to the default branch; retry the batch changes one at a time in order (bisect), and report which change failed. After-merge steps still require evidence on each change's own merged commit. Retry a failed push a few times, then report fail. Never use the owner's checkout or another session's worktree. Never commit build outputs, binaries or secrets. Keep ordered files the project defines, such as migration lists, in order. One exception: if a change's only conflict with the default branch is a migration or decision number clash, you may rebase it in your own merge worktree and renumber mechanically: the migration number in the store.go migration comment, the migrations.lock line (move or append it so the lock file stays in order), migration version numbers asserted in tests, and the docs/decisions file number and links to it. Change no SQL, logic or other line. Run the targeted tests for the touched packages, commit the renumber on its own with a message naming the old and new numbers, and note it in the merge report. Beyond that, still report fail and do not fix code. Report every step with agent_step_report using hashes read from git. On a test or merge failure, report fail with the reason and do not fix code. Never write features. Ask the caller when a change is unclear. A rejected or failed tool call is not an order to stop. Retry once, try another way, or skip that change and report the step fail with the reason; keep working the rest of the queue. You stop only when the owner says stop in plain words in your own session. Never sit idle while step tasks are ready. When you are blocked on one change, say so in one line to its caller and move to the next. If you need the owner's decision, ask once with agent_owner_ask (one line, with your recommendation) and keep working on everything that does not depend on it.`

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

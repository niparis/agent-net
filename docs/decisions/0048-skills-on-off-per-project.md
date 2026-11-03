# 0048 - Skills: on or off per project

**Date:** 2026-11-03
**Status:** Accepted by the owner; build started
**Replaces:** the skill parts of [0027](0027-skills-and-mcp-management.md) listed under "What changes". MCP management in 0027 is unchanged.

## Problem

The owner wants to choose which skills each project gets, and see what they cost. Today that does not work:

- An owner who switched 8 skills off for one project got "project is not registered" 8 times. Only 2 of 22 projects are registered.
- `ProjectSkillRoot` reports a conflict whenever OpenCode, Claude, and Codex or Pi are installed together, which is true on every machine. So no skill can be placed in a project at all.
- Skills live in user folders, so every session on the machine loads them. "Off for one project" is not something a harness offers.
- Import is one skill at a time and fails above 2 MB.

## Harness test (2026-11-03)

Throwaway repo with marker skills in each folder, a subfolder, a worktree outside the repo and one inside it (where Claude puts its own). Claude Code 2.1.317, Codex 0.166.0, OpenCode 2.2.0, Pi 1.4.0 on Linux; Antigravity 1.4.12 on macOS.

| Harness | Project folders read | From a subfolder | Worktree outside | Worktree inside | Same name in two folders | User folders read |
|---|---|---|---|---|---|---|
| Claude Code | `.claude/skills` | yes | yes (reads the main checkout) | yes | - | `~/.claude/skills` |
| Codex | `.agents/skills`, `.codex/skills` | yes | no | no | - | `~/.agents/skills`, `~/.codex/skills` |
| OpenCode | `.agents/skills`, `.claude/skills`, `.opencode/skills` | yes | no | yes (parent folders) | loaded once | `~/.claude/skills`, `~/.agents/skills`, `~/.config/opencode/skills` |
| Pi | `.agents/skills` (up to repo root), `.pi/skills` (current folder) | `.agents` only | no | no | - | `~/.agents/skills`, `~/.pi/agent/skills` |
| Antigravity | `.agents/skills`, `.agent/skills` (up to repo root) | yes | no | no | loaded once | `~/.gemini/config/skills` only |

Also found:

- Claude picks up a skill added while it runs. Pi needs `/reload`. Codex and OpenCode were not tested; assume a new session is needed.
- Pi loads project skills only after project trust. Without it, in RPC or print mode, it skips them without a message.
- Antigravity does not read `~/.agents/skills`.

## Decision

### What the owner sees

- **A table:** skills by projects, each cell on or off.
- **One setting per skill:** "new projects: on or off".
- **The cost:** tokens each project loads in every session.
- **Copy from another project:** copies one project's choices to another.
- **No kinds, folders, machines or harnesses.** agent-net decides where files go.

### Where agent-net puts a skill

- **On in every project, and on for new projects:** user folders, so it works everywhere, even outside a project.
  - `~/.claude/skills` (Claude, OpenCode).
  - `~/.agents/skills` (Codex, Pi, OpenCode).
  - `~/.gemini/config/skills` (Antigravity, only if `~/.gemini` exists).
  - OpenCode reads the first two and loads a skill once.
- **Any other skill:** one copy in each project where it is on, in **two** folders:
  - `.claude/skills` for Claude;
  - `.agents/skills` for Codex, Pi, OpenCode and Antigravity.

  No user-folder copy. OpenCode reads both folders and loads the skill once, so this is no longer a conflict.
- **Switching an every-project skill off in one project** moves it from the user folders to copies in every other project. This is one plan.
- **Project copies are kept out of Git** with `info/exclude`, as built for 0027. agent-net never edits a tracked file, `.gitignore`, or a harness config file.

### Projects

- **No registration step.** Any project agent-net knows is a target if its folder is a Git repository on that machine.
- **Never a project:**
  - the home folder;
  - a folder that is not a repository;
  - a folder that is a worktree of another repository, which belongs to its main checkout.
- **New projects** get the skills whose "new projects" setting is on, when agent-net first sees them. This applies the owner's standing choice; it needs no new plan.

### Worktrees

- **Only Claude finds a project's skills from a worktree.** So agent-net copies the project's skills into every worktree it creates for a worker, before the worker starts.
- **Worktrees agent-net does not create** (Codex, Pi or OpenCode sessions in a worktree the owner made) miss them. Accepted for now. A Git `post-checkout` hook could cover them later, but it can clash with existing hooks.
- **Pi workers** start with project trust for their own worktree, so project skills load.

### Library

- **One library on the gateway:** every skill, one current version, in the existing private Git store.
- **Automatic intake:** a skill the daemon finds is taken into the library without an import step. This changes 0027, which required an owner request per import.
  - The store is the owner's own private gateway.
  - The secret checks and size limits of the import path still apply.
  - A skill that fails them stays local and shows why.
- **An edit made in place** on a machine shows as a new version. The owner keeps it or restores the library version.
- **Same name, different content on two machines:** the owner chooses once.
- **Never managed:** plugin skills, skills synced from claude.ai, harness system skills (`~/.codex/skills/.system`), and anything inside a package. They are shown read-only.
- **Large skills:** today's 2 MB limit comes from the 4 MB WebSocket frame. Skills above it are sent in pieces. This is needed only to copy a skill to another machine; placing it on the same machine needs no transfer, so it comes last.

### Migration

- **Day one, nothing changes.** Every skill found in a user folder becomes "on in every project" **on the machines where it is today**; it is not spread to other machines.
- **The owner reviews the list once** and switches skills off where they do not belong. Each change is one plan with backups and undo, as built for 0027.

### Kept from 0027

Plans the owner confirms, the journal, backups, undo, compare-and-swap removal, Git exclude handling, owner-only authority enforced at the gateway and daemon, and all MCP management.

## Left out on purpose

- **Hide switches in harness configs** (Claude `skillOverrides`, Codex `skills.config`, OpenCode `permission.skill`):
  - three formats;
  - not in Pi or Antigravity;
  - they write into harness config files.

  They may come back later as a token saving.
- **Sets of skills.** "Copy from another project" covers the need for now.
- **Agents asking for a skill** through the CTO. Later, if needed.
- **Antigravity as a full harness** (sessions, messages, workers). Separate work; this decision only delivers skills to it.

## Build order

Each step is one worker with failing tests first.

1. **Placement:**
   - project copies in `.claude/skills` and `.agents/skills`, with the conflict rule removed;
   - the Antigravity user folder;
   - read-only sources left alone.
2. **Projects:** any known Git repository is a target; home, non-repository and worktree folders are refused with a clear reason. The registration check in assignments is removed.
3. **On/off model:**
   - the table maps to "global" or "projects" assignments;
   - switching off one project of a global skill makes one plan;
   - "new projects" setting;
   - migration keeps today's machine scope.
4. **Worker worktrees:** project skills copied in before start; Pi workers trusted.
5. **Automatic intake** and version choice.
6. **Screen:** the table, costs, copy from a project, and the one-time review. Reuse the Tools tab parts that fit.
7. **Large skills** sent in pieces.

Docs (`reference.md`, `security.md`, uninstall) change with each step that changes what is written to a machine or a security rule.

## Correction: versions across machines (2026-11-03)

A project switch covers a skill name, including every content identity of that
name. The owner never chooses a machine. One plan group contains the existing
per-identity plans, each with its own snapshot, targets and daemon revision.
Apply validates all members before recording their revisions together; undo
reverses each member. A stale member refuses the group before any change.
For placement, prefer the checkout's version, then the machine's user version,
then the newest library version. The Skills row checks all identities across
the project's checkouts, and the information box names differing copies.

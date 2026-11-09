# 0124 - Slash commands and subagents are managed items

Date: 2026-11-09

Status: Accepted. The owner approved this on 2026-11-09 ("do it").

## Context

Claude Code keeps slash commands in `.claude/commands/*.md` and subagents in
`.claude/agents/*.md`, in the user folder and in each repository. People copy
them between repositories by hand and the copies drift. The Tools tab managed
only skills and MCP servers.

## Decision

Add two item kinds, `prompt` (a command) and `agent` (a subagent), and run them
through the machinery that skills use. There is no second system.

- A prompt or agent is one Markdown file. Its bundle is a tar of one file named
  `<name>.md`. The item hash is the strict hash of a folder that holds only that
  file, so hash checks, the skill store, numbered versions and the manifest need
  no new code path.
- `protocol.IsBundleKind` selects the three kinds that are stored and switched
  on per project. `protocol.IsFileKind` selects the two that are files. The
  tools filter value `bundles` means all three.
- An item identity is `[kind, name, hash]`, so a skill, a command and an agent
  with the same name are three items. Code that grouped by name now groups by
  kind and name.
- The destination folder comes from the kind: `.claude/commands` or
  `.claude/agents`, claude harness only. `protocol.FileKindRoots` is where other
  harnesses are added.
- A project copy writes the file and a Git exclude line that names the file
  with no trailing slash (a slash would match only a folder).

## Limits kept on purpose

- Only the files directly in the folder are read. Namespace folders below
  `.claude/commands` are not inventoried.
- Differences, the compare box and repo removal through a branch stay
  skill-only. A tracked command or agent is removed in its repository.
- Token counts and costs per project count skills only.

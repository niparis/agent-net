# 0169 Skill archive

Decision, 2026-11-16. The owner approved it in person. It replaces decision
0048 for skills. MCP servers are out of scope.

## The archive

The archive is a set of rows in the gateway database, never a Git repository
and never committed into a repo. Each row is one skill version: name, version
number, content hash, the bundle bytes, the description from the `SKILL.md`
front matter, the size in tokens, the source, who added it and when.

The size in tokens is an estimate: the total bytes of the skill files divided
by four, rounded up. The model sees the numbers and there is no cap.

A skill gets in only by being added, never by being found. There are three
sources:

- a folder on an online machine, read through the existing
  `node.skill_bundle` frame;
- a Git URL the owner or the CTO gives, with no approval step. Each folder
  that holds a `SKILL.md` becomes a skill. The URL, commit and folder are
  recorded;
- an agent session that writes the skill.

Same name and same content is one version, and adding it again changes
nothing. Same name and different content is a new version.

Every add and remove is recorded with an id, so the owner can undo it. Undo of
an add removes that version; undo of a remove restores it.

## The rest of the design

- Each node (global, team, repo, agent) has an inherited skills add and remove
  list, set by the CTO. Task 3 builds it.
- Sessions get their skills from an agent-net folder at start. Task 4 builds
  it.
- The owner's user-level skill folders end up holding only the skills set for
  everyone. Session folders add skills; they cannot hide the user folders.
- agy gets only the everyone skills, because it has no skills-only switch.
- Skills committed inside a repo stay the repo's own and read only. A repo
  skill wins a name clash with an archive skill.
- No context cap. The token numbers are shown.

## The switch

Task 2 stores the archive and fills it once. At gateway start, after the
migration, every version already in `skills.git` is copied into the archive by
name and hash. The copy is safe to repeat. A CTO action, `skill_archive`
intake, also pulls every distinct skill from the online machines' user-level
and project skill folders.

The intake covers every enrolled machine: the user skill folders of every
harness, the skill folders of every registered project checkout, Claude Code
plugin skills (read only, source "plugin <name>") and the CTO's own skill
(source "agent-net"). Its report gives one result per machine (scanned,
offline or skipped) and each skill it could not store, with the reason.

Nothing is delivered from the archive in task 2. The Tools tab and the current
skill code keep working until task 5 retires them.

## Access

Only the owner and the CTO reach the archive operations, through the CTO tool
or the owner's undo in the Desk. The gateway records the actor on every row,
as it does for settings writes. The daemon refuses a caller that is not the
CTO or the owner.
## Task 3: choosing skills

Task 3 stores the lists and computes them. Nothing is delivered to a session
yet.

- Each node (global, team, repo, agent) has an add list and a remove list. The
  effective list of a node merges the rows along its path: global, the team
  the repo or agent is in, the repo, the agent. A remove at one level takes
  away a higher add, and a lower add wins over a higher remove.
- A row can pin a version. Without a pin the node follows the latest live
  version. When a set touches a repo at mature or critical maturity, the names
  it adds are pinned to the version they get then, unless the caller pinned
  them already.
- An Agent's repo context is its sole owned repo, else the repo of its latest
  session. An Agent in several repos uses that one context for its path.
- Every set is recorded with the list before and after, so the owner can undo
  it from the Desk. A set also answers the token total before and after and
  the live sessions on that node and below that still run the old set, with
  the restart call. Nothing restarts by itself.
- `skills` suggest is read only and makes no model call. It reads the node's
  effective charter, the project bar drafted from AGENTS.md, the session's own
  repo description, the project remote and branch, the harnesses seen there,
  and an Agent's profile. It ranks the archive's descriptions by shared words
  and gives the matched words plus the description as the reason. It flags
  each skill whose text names an `mcp__server__tool` that the node's shared
  MCP servers do not have. A short list is returned (8 by default) and its id
  can be given to set later.
- A new repo gets one notice to the CTO: "new project X: run skills suggest
  and set, then tell the owner". There is no hold and no approval step.
- There is no token cap; the numbers are shown.

## Task 4: the skills folder of a session

Task 4 hands each session and worker the effective skills of its node. The node
of a session is its Agent, resolved along agent, repo, team and global; a
session without an Agent uses its project's Repo. A worker has no Agent of its
own, so its node is its project's Repo.

The daemon writes one folder per set under its own state folder,
`<state>/skills/<hash>/`, from the archive bundles the gateway holds. The
gateway answers the set in one read (`skills.folder`) and each bundle in its own
(`skills.version`), because a whole set can be larger than one frame may carry.
The folder's name is the hash of its skills at their resolved versions: an
unchanged set is reused, a changed set gets a new folder.
Each bundle is checked by its content hash before a session sees it. The folder
is in Claude Code's plugin form, so it holds `.claude-plugin/plugin.json` and
`skills/<name>/`. Nothing is written into a project, a worktree or a user skill
folder.

Each harness takes the set its own way:

- Claude Code: `--plugin-dir` to the folder.
- Pi: `--skill` for each `skills/<name>` folder.
- OpenCode: the `skills` key of its per-process config, which adds to the
  owner's own folders and to any config agent-net already passes. This applies
  to the processes agent-net starts: workers and the CTO. An OpenCode session
  that runs in the owner's shared service is not agent-net's process and gets
  no folder here.
- Codex: `skills/extraRoots/set` with the folder's `skills` root on a worker's
  own app server, after the thread starts. The owner's shared app server gets
  no call: the method has no thread argument, so one session's roots would
  replace every other thread's. Owner Codex sessions keep the everyone skills
  from the user folder, as a hand-started session does.
- agy: nothing, as the owner chose; agy gets only the everyone skills.

A node with an empty effective list gets no flag and no folder. A failure to
build never blocks the start: the session starts without the folder and one
daemon log line says why. Nothing restarts a running session; the set result
already lists the sessions that still run an old set.

## Latest version

Each name has one latest version, and a node following latest gets it. The
latest is the version that the `skill_archive_latest` row names, while that
version is live. Without a live row, it is the highest live version.

- An explicit add, a new version from a Git URL, or a copy of the old store
  makes the new version the latest.
- An intake copy of a name that already has a live version is a candidate. It
  is stored and listed, but the latest does not change. An intake copy of a name
  with no live version is the latest.
- `skill_archive` `promote` (name and version) makes a live version the latest.
  Its change records the earlier latest, so the undo in the Desk puts it back.
- `skill_archive` `list` shows each name's latest version and its candidates.
  The intake report names each candidate with the version that stays the latest,
  and it names each skill that has no description.
- The migration that adds the table backfills it from the old rule (the highest
  live version). It makes the project, plugin and agent-net copies that intake
  added after a name's first version into candidates, so a project copy such as
  graphify version 3 no longer takes over the latest. A machine copy looks like
  an explicit machine add, so the migration keeps the old rule for machine
  copies. The CTO can promote a version if that is wrong.

## Size: the archive limit and parts

The archive takes a skill of up to 64 MiB packed and 10,000 entries. The
scan already reads 64 MiB of file data and 10,000 files, so a skill the scan
shows is one the archive can take. The packed size counts the tar headers too.

The import limit stays at 2 MiB and 2,048 entries. It still applies to the
copies into session folders on the Tools tab and to the `tools_import` path
(decision 0048). A skill over 2 MiB stays too large there.

A frame of the link is 4 MB at most. A bundle over 1 MiB travels in parts of
1 MiB, each about 1.4 MB in its frame:

- Archive add and intake. The machine's first answer holds part 0, a snapshot
  ID, the part count, the size and the SHA-256 of the whole bundle. The gateway
  asks for the other parts by snapshot ID and part number. The machine packs
  the folder once and serves every part from those bytes, so a folder that
  changes while it is read fails the hash check and is not stored. The machine
  drops the snapshot after its last part, or after five minutes.
- Delivery. `skills.version` takes a part number. The gateway reads one part
  from the stored row with SQLite's `substr`, so one part does not load the
  whole version. The daemon joins the parts and checks the hash before it
  unpacks.

Decision 0054's transfer does not fit this. That transfer is a sender pushing
an upload to the gateway, to be fetched by a receiving machine. Here the
gateway pulls from a machine and keeps the bytes in its own database.

Older daemons still work. They ignore the archive flag, so they answer up to
2 MiB and refuse a larger skill with the import limit text. A daemon older than
this change gets only the first part of a version over 1 MiB. Its hash check
fails, and the session starts without that folder, with one log line.

Measured on the development machine, the gateway reads a stored version in
parts in 13 ms for 6 MB (7 parts) and 0.9 s for 60 MB (61 parts). One whole
read of the same 60 MB row takes 33 ms. Storing the 60 MB row takes 0.25 s. The
daemon gives a session's folder build 10 seconds (`skillBuildTimeout`). A
64 MB skill over a slow link can pass that limit, and then its session starts
without the folder.

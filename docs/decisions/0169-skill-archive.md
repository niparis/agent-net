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

Nothing is delivered from the archive in task 2. The Tools tab and the current
skill code keep working until task 5 retires them.

## Access

Only the owner and the CTO reach the archive operations, through the CTO tool
or the owner's undo in the Desk. The gateway records the actor on every row,
as it does for settings writes. The daemon refuses a caller that is not the
CTO or the owner.
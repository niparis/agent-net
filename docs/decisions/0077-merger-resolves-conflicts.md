# 0077 - The merger resolves conflicts

On 2026-11-07 the owner asked: "thats not expected at all. why isnt it fixing these conflicts?"

The merger now resolves conflicts with the default branch or its merge batch
in its own merge worktree. It reads both sides against the change's base,
keeps both intents, and commits the resolution separately. The commit message
names the conflicting files and what it kept. Its merge report note names
each resolved file and gives a one-line summary.

Mechanical migration and decision renumbers remain a special case. After
resolution it runs targeted tests and CI. If those fail, it reports test fail
with the reason without rewriting features to make tests pass. Never write
features remains in the brief.

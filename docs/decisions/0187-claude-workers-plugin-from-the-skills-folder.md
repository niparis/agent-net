# 0187 - The Claude workers plugin loads from the skills folder

Status: accepted by the owner's instruction on 2026-11-25 ("agent net has to be
independant from dotfiles", then "let agent net fix this by itself").

## Problem

The Claude Code worker window is a plugin. An older agent-net registered it from
a local marketplace and enabled it in `~/.claude/settings.json`. That file can
belong to another tool: on the owner's Mac it is a link into his dotfiles
repository. A `git stash` there removed `enabledPlugins` and the marketplace
entry, the window disappeared, and `claude-workers-mod.json` still said the
plugin was installed. The window is agent-net's own function, so it must not
depend on a file another tool owns.

## Decision

- Claude Code loads a plugin from `skills/<name>` in its config directory with
  no marketplace and no `enabledPlugins` entry. agent-net keeps the plugin files
  in its own folder, `~/.local/share/agent-net/claude-plugins/agent-net-workers/`,
  and puts one link, `agent-net-workers`, to that folder in
  `$CLAUDE_CONFIG_DIR/skills` (else `~/.claude/skills`). This was proven with
  real Claude Code 2.1.330 and 2.1.332 in a throwaway setup: the plugin is
  listed as `agent-net-workers@skills-dir`, enabled, and the band renders at
  120x34 and 80x24.
- Setup writes the link on the owner's yes. The daemon puts a missing link back
  at start and on its existing sync, only while the stored answer is yes. It
  never replaces a foreign entry with the link's name; it reports it.
- The answer in `claude-workers-mod.json` stays the owner's yes. Installed or
  not is read from the live state with file reads only: the link, Claude Code's
  `enabledPlugins` value, its installed plugin list, and the old marketplace
  entry. No `claude` process runs on the start path. A missing or disabled
  window is logged once with the one command that fixes it.
- A disable the owner chooses in Claude Code (`claude plugin disable`) is kept.
  Turning the window off in agent-net, with the setup answer or uninstall,
  removes agent-net's own link and nothing else. A foreign entry is left alone.
- An older agent-net registered the same plugin from a local marketplace. That
  copy shadows the skills copy while it is enabled. When the settings file is a
  regular file that agent-net may write, setup and the daemon remove the old
  marketplace entry after Claude Code lists the skills copy. When the settings
  file is a link or not writable, agent-net never writes it and the old entries
  stay. While the old copy is enabled and installed, agent-net keeps its own
  link out of the skills folder, so a lost marketplace registration cannot load
  both copies at once. When the old entry is lost, the next daemon start puts
  the link back, so the window returns without the settings file.

## Why

One link in Claude Code's own config folder, and no entry in a file another tool
manages. The plugin files stay in agent-net's folder and follow the binary. The
old marketplace copy is only a fallback for a machine whose settings file
agent-net may not write, and it stops being used when its entry is gone.

## Not decided here

The five Claude hooks keep living in the settings file. Moving them into the
plugin needs the owner's consent, and a different event set to keep their
current behavior.

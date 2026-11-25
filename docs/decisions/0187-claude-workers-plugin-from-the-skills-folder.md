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
- The link stays in place all the time, also while an old marketplace copy
  runs. Real Claude Code on both versions shows exactly one window in each
  state, with the old copy in its installed list: both settings entries
  present, both lost, only the marketplace entry lost, and only the enabled
  entry lost. It enables only one copy at a time; two copies never show two
  bands.
- Setup writes the link on the owner's yes. The daemon puts a missing link back
  at start and on its existing sync, only while the stored answer is yes. It
  never replaces a foreign entry with the link's name; a link to somewhere else
  is foreign too. It reports it.
- The answer in `claude-workers-mod.json` stays the owner's yes. Installed or
  not is read from the live state with file reads only: the link, Claude Code's
  `enabledPlugins` values, its installed plugin list, its own marketplace list,
  and the old marketplace entry. No `claude` process runs on the start path.
- An explicit false for either plugin id is the owner's disable and is kept. A
  missing entry is not a disable: it is the loss the owner's settings file
  suffers, and the daemon repairs it.
- While the old marketplace copy is the one that runs, the daemon refreshes it
  after a panel change. The two commands, `claude plugin marketplace update` and
  `claude plugin update`, write Claude Code's own plugin state and not
  `settings.json`, proved with real Claude Code on a regular and on a linked
  settings file. They are safe on the owner's Mac.
- When the enabled entry alone is lost, the installed old copy keeps both
  copies off. The daemon removes that installed copy with `claude plugin
  uninstall`, which has no settings key to remove in that state and writes no
  settings file, also when it is a link. This is the owner's loss: the settings
  file lost the entries while Claude Code's own marketplace list kept them.
- If Claude Code already wrote an explicit false into a linked settings file
  after that loss, agent-net does not write the link. It reports the one command
  that removes the installed copy, for the owner to run.
- A missing or blocked window is logged once per state change, not at every
  sync, with the one step that works for that state. The same line is shown on
  the machine row in the Machines screen and by `agent-net nodes`, so the owner
  sees it outside the daemon log.
- The Claude commands that move or refresh the old copy run in the background
  at daemon start and on the integrations tick, never on the start path or the
  sync of the other files. A failed run is recorded against the binary version
  and tried again only with a new version.
- Turning the window off in agent-net, with the setup answer or uninstall,
  removes agent-net's own link and nothing else. A disable the owner chooses in
  Claude Code (`claude plugin disable`) is kept. A foreign entry is left alone.

## Why

One link in Claude Code's own config folder, and no entry in a file another tool
manages. The plugin files stay in agent-net's folder and follow the binary. The
old marketplace copy is refreshed while it runs, and the link is already there
when its entries are lost. The repair writes no settings file, so the Mac's
linked file is never touched.

## Not decided here

The five Claude hooks keep living in the settings file. Moving them into the
plugin needs the owner's consent, and a different event set to keep their
current behavior.

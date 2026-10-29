# OpenCode

OpenCode v2 sessions join the network through a plugin, `plugins/opencode/agent-net.ts`. The spike that led here is in [spike/opencode.md](spike/opencode.md).

## How it works

OpenCode runs one background service (`opencode serve --service`) that hosts every session. Plugins load inside that service. MCP servers also run under the service, so a stdio MCP server cannot tell which session is calling. That is why the adapter is a plugin that registers the `agent_*` tools itself: each tool call carries the session ID.

- **Where v2 loads plugins:** every file in `~/.config/opencode/plugins/` (or `$XDG_CONFIG_HOME/opencode/plugins/`), `.js` or `.ts`, plus entries in the `plugins` list of `opencode.jsonc`. The file must `export default { id, setup }`; the old v1 style (`export const Plugin = ...`) fails with "Plugin must export a default definition with an id". OpenCode also watches the file and reloads it when it changes.
- **Where v2 reads global instructions:** `~/.config/opencode/AGENTS.md`. Setup adds the same marked guidance block as for Pi ("other agents" variant).
- **Plugin instances:** OpenCode creates one instance per project directory, and every instance receives the events of every session. A shared per-process hub decides which instance owns a session, so one session never gets two daemon connections.
- **Connection model:** one daemon connection per OpenCode session, all from the service process. The first request is `hello` with `{client:"opencode", harness:"opencode", native_id:<session ID>, cwd}`. This needs no change to the local protocol: a connection is still bound to exactly one session, the sender identity is the bound session, and `deliver` pushes arrive on that session's connection. When a session goes away its connection closes and the daemon marks it offline.
- **Peer check:** the daemon reads the peer PID (`SO_PEERCRED` / `LOCAL_PEERPID`) and requires the peer's executable file name to start with `opencode` (`/proc/<pid>/exe` on Linux, `ps -o comm` on macOS). This is stronger than the Pi check, which only looks for `pi` in the command line. The native ID is taken as given, since the plugin runs inside the service.
- **Presence:** a session registers on `session.created`, before any prompt, and on any later activity (`session.viewed`, `session.inbox.*`, `session.execution.*`). The plugin retries for a few seconds if the new session cannot be read back at once. At plugin load, the newest 50 top-level sessions of all directories that were updated in the last 60 minutes are registered too, used or not. If a plugin instance goes away, another one registers its sessions again at once. `session.execution.started` sends `busy`; `succeeded`, `failed` and `interrupted` send `idle`. `session.deleted` and `session.archived` close the connection. A link whose plugin instance is gone (a reload that did not release it) is stopped and replaced, never shared, so one session never has two connections that replace each other at the daemon. Sub-agent sessions (with a `parentID`) use their root session's connection.
- **Dormant:** OpenCode keeps every session inside its service, so "online" only means "reachable", not "in use". A session with no activity for 60 minutes (no prompt, step, message, tool, view or agent-net tool call) is reported with status `dormant`. It stays online and a message to it still delivers; the delivery makes it active again, and so does any other activity. A busy session is never dormant; its clock starts when the turn ends. A session found at discovery starts its clock at its last update. The 60 minutes are the constant `DORMANT_AFTER_MS` in the plugin (the environment variable `AGENT_NET_OPENCODE_DORMANT_MS` overrides it, for tests). `agent-net list` hides dormant sessions unless you add `--dormant`, and the TUI hides them with the offline ones (`o`). Hello always says `idle` or `busy`; `dormant` follows as a `status` call, so a daemon that does not know the value keeps showing `idle`. Gateways store the status as given, so older gateways accept it.
- **Name:** the name is `<folder>: <title>`, cut to 60 characters with `...`. The title is `Session.Info.title` (optional, `@opencode/schema` `session.d.ts`); the plugin follows the `session.renamed` event (`data: {sessionID, title}`, `@opencode/schema` `event-manifest.d.ts`) and sends the new name with a `status` call. Without a title the name is the folder name. A name set with `agent_announce` still wins. A sub-agent session never renames its root session.
- **Delivery:** `ctx.session.prompt({sessionID, text, delivery: "queue"})` wakes an idle session and waits behind a busy turn. The text has the Pi footers: a request invites a direct answer with `agent_reply`, a reply says no answer is needed. The plugin confirms with `delivered`.
- **Tools:** `agent_list`, `agent_nodes`, `agent_get`, `agent_announce`, `agent_send`, `agent_reply`, `agent_messages`, `agent_message_status`, `agent_project_add`, `agent_project_list`, `agent_start`, `agent_stop`, `agent_handback`, with the Pi parameters and the same short text output. They are registered with `ctx.tool.transform` and a JSON Schema input, with `codemode: false` so the model sees them as normal tools.

## Workers

`agent-net start --harness opencode` (or `agent_start`) runs `opencode serve` as a child of the daemon, with the plugin loaded, so the worker is a normal agent-net session. The plugin does two things differently in a worker, which the daemon marks with `AGENT_NET_WORKER`: it skips the start-up discovery of your other sessions, and it registers only the one session whose ID the daemon writes to `AGENT_NET_WORKER_SESSION_FILE`. The daemon also accepts only the worker's own process as that session. To make OpenCode load the plugin in the project folder, the daemon asks for `GET /api/location` with the folder (as OpenCode's own clients do); creating a session over the API does not load plugins. The tools `agent_start`, `agent_stop` and `agent_handback` are registered like the others (`agent_handback` fails for a session that is not a worker). The worker uses your OpenCode data folder (it needs your credentials) but has a private state folder and its own port. See [decision 0008](decisions/0008-operator-interface-and-worker-build.md) for why, and what clashes are left. The worker's session stays in your OpenCode session list after the worker ends.

## Install

`agent-net setup` does both steps when `~/.config/opencode` exists, and prints a hint about restarting the OpenCode service. It never restarts it, since the service hosts all your sessions. `agent-net uninstall` removes the plugin file and the guidance block. A symlinked plugin file is left alone.

## How to test

Run everything in isolation, so the real OpenCode service and the real agent-net daemon stay untouched.

1. A throwaway gateway and daemon, with short socket paths (Unix socket paths are limited to about 108 bytes):

   ```sh
   export AGENT_NET_SOCKET=/tmp/ane/d.sock AGENT_NET_CONFIG_DIR=$T/nodecfg AGENT_NET_STATE_DIR=$T/nodestate
   export AGENT_NET_CLAUDE_SESSIONS_DIR=$T/empty        # keep real Claude sessions out
   agent-net gateway --listen 127.0.0.1:17421 --data $T/gw &
   agent-net join "$(agent-net enroll -data $T/gw -host 127.0.0.1 -port 17421 | head -1)"
   agent-net daemon &
   ```

2. An isolated OpenCode service with its own config, data and port. `XDG_*` moves all four directories. The default service port is taken by your real service, so set another one:

   ```sh
   export XDG_CONFIG_HOME=$T/cfg XDG_DATA_HOME=$T/data XDG_STATE_HOME=$T/state XDG_CACHE_HOME=$T/cache
   mkdir -p $T/cfg/opencode/plugins
   cp plugins/opencode/agent-net.ts $T/cfg/opencode/plugins/
   opencode service set port 49377
   opencode service start        # inherits AGENT_NET_SOCKET
   ```

3. Drive sessions without a TUI. The free model works without login:

   ```sh
   cd $T/proj1 && opencode run -m opencode/beam-free --title one "Reply with the word PONG."
   agent-net list                              # the session shows with harness opencode
   opencode run -m opencode/beam-free -s <session id> "Use agent_send to send ... to pipeer"
   opencode session export <session id>        # transcript as JSON
   opencode session delete <session id>        # agent-net list: the session goes offline
   ```

4. A Pi peer: `AGENT_NET_SOCKET=/tmp/ane/d.sock pi --model <model> -ne -e plugins/pi/agent-net.ts` in tmux (`-ne` keeps your installed extension out).

`plugins/opencode/typecheck.sh` type-checks the plugin. `plugins/opencode/test.sh` runs the plugin against a fake OpenCode context and a fake daemon (needs network the first time, to install TypeScript). Go tests: `go test -race ./...` (see `internal/daemon/opencode_test.go` for the peer check and the two-session case).

## Notes and limits

- MODE is `unknown` for OpenCode sessions. OpenCode 2.2.0 has no signal for "a TUI or app is attached to this session". The plugin context has no list of clients. The only viewer signal is the `session.viewed` event (`POST /api/session/:id/view`, "mark the idle transition observed by the viewer as viewed"), which fires once per idle transition and says nothing about a viewer that stays attached and quiet. `Session.Info.time.viewed` has the same limit. The MODEL is reported (`Session.Info.model`, then `session.model.selected` and `session.step.started`).
- Not tested on macOS. The macOS peer check (`ps -o comm` for the executable) is written but unrun.
- OpenCode creates a plugin instance for a project directory only when a client first uses that directory, not when the service starts. While no instance is loaded at all, no plugin code runs, so a session created through the API then registers when the first instance loads (the 60-minute discovery covers it, for sessions updated in the last hour). Once any instance is loaded, it sees the created event of every directory. In the TUI a new session is created when you send the first prompt, not when you open the empty home screen, so there is nothing to register before that.
- Sessions not updated in the last 60 minutes, or beyond the newest 50, register when something happens in them (a view, a prompt, a message to it).
- A session without a title yet is named after its folder, so two such sessions in one project share a name. Use the session ID to tell them apart, or let each one call `agent_announce`.
- OpenCode 2.2.0 hot-loads new and changed files in `plugins/` within seconds, so a restart should not be needed. A reload drops and re-creates the session connections. If the tools are missing in a session, restart the service (`opencode service restart`) when no session is busy. Setup prints this hint and never restarts the service itself.

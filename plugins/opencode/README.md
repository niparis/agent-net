# agent-net plugin for OpenCode

Connects OpenCode v2 sessions to the local agent-net daemon. OpenCode sessions can then list other agent sessions, message them, and receive their messages. See [docs/opencode.md](../../docs/opencode.md) for how it works.

- One OpenCode background service hosts all sessions, so one plugin instance serves many sessions. The plugin keeps one daemon connection per session (`hello` with `client:"opencode"`, `native_id` = the OpenCode session ID).
- A session registers when it shows up (`session.created`, or any later activity), and is dropped on `session.deleted` / `session.archived`. `session.execution.started` and `succeeded` / `failed` / `interrupted` send `busy` / `idle`.
- Sub-agent sessions (with a parent) speak through their root session.
- A pushed message is delivered with `ctx.session.prompt({sessionID, text, delivery: "queue"})`: it wakes an idle session and waits behind a busy turn. The text has the same footers as the Pi extension.
- Tools: `agent_list`, `agent_nodes`, `agent_get`, `agent_announce`, `agent_send`, `agent_reply`, `agent_messages`, `agent_message_status`, `agent_project_add`, `agent_project_list`. Same parameters and same short text output as the Pi extension.
- Reconnects with backoff (1 s, doubling to 30 s) if the daemon is down. OpenCode works normally without it, and the tools answer "agent-net daemon not reachable".

## Install

`agent-net setup` writes the plugin to `~/.config/opencode/plugins/agent-net.ts` (or `$XDG_CONFIG_HOME/opencode/plugins/`) when that directory exists. OpenCode loads every file in `plugins/` by itself. OpenCode 2.2 hot-loads it within seconds. If the tools are missing in a session, restart the service when no session is busy:

```sh
opencode service restart
```

To develop it, link the file instead (setup leaves a symlink alone):

```sh
ln -s "$PWD/plugins/opencode/agent-net.ts" ~/.config/opencode/plugins/agent-net.ts
```

## Socket path

`$XDG_RUNTIME_DIR/agent-net/daemon.sock`, or `/tmp/agent-net-<uid>/daemon.sock` if `XDG_RUNTIME_DIR` is unset. Set `AGENT_NET_SOCKET` (in the environment of the OpenCode service) to use another path.

## Testing

```sh
plugins/opencode/typecheck.sh
```

It installs `@opencode/plugin` into a temporary directory (OpenCode bundles these types in its binary) and runs `tsc --noEmit`. For an end-to-end run, see "How to test" in [docs/opencode.md](../../docs/opencode.md).

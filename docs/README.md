# Documentation

## Use it

- [setup.md](setup.md): install, add a machine, update, remove. The details behind the README quickstart.
- [reference.md](reference.md): ports, files, environment variables, flags, and what setup changes on your machine.
- [security.md](security.md): the trust model, and what agent-net does not protect against.
- [troubleshooting.md](troubleshooting.md): first checks and fixes for common problems.
- [agent-guidance.md](agent-guidance.md): the text agent-net adds to your agents' instruction files.
- [../plugins/pi/README.md](../plugins/pi/README.md): the Pi extension.
- [codex.md](codex.md): Codex threads as peers: how they are found, how messages reach them, setup and limits.

## Work on it

- [../AGENTS.md](../AGENTS.md): build, test, layout and conventions.
- [../RELEASING.md](../RELEASING.md): cut a release and update machines.

## Design history

These files record how the project was planned and built. They are kept for context. They are not kept up to date, and parts of them are out of date (names, ports, plans that changed). For how things work now, use the pages above and the code.

- [brief.md](brief.md): the original brief.
- [review.md](review.md): review of the brief and the build order.
- [decisions/](decisions/): decisions taken, one file each. [0003](decisions/0003-gateway-record-and-inventory.md) covers the gateway log, repo info, harness inventory and retention. [0005](decisions/0005-registered-projects.md) covers registered projects. [0004](decisions/0004-tui-and-event-feed.md) covers the TUI, session mode and model, and the event feed.
- [0002](decisions/0002-workers.md): workers, accepted. [0008](decisions/0008-operator-interface-and-worker-build.md): the operator interface, the owner as a sender, and how workers were built (the OpenCode command, isolation from the user's service, hand-back, notices, worktrees).
- [0009](decisions/0009-function-from-agents-md.md): FUNCTION is the project description read from AGENTS.md (the extraction rule), and announcing the task is required.
- [0007](decisions/0007-usage-and-session-text.md): CPU and memory per machine and session, the text of a session (owner only), and session counts.
- [0010](decisions/0010-tui-shows-usage-and-conversations.md): the TUI shows CPU, memory and conversations and is the only reader; `top` and `show` are removed.
- [0011](decisions/0011-opencode-sessions-stay-visible.md): OpenCode sessions survive a plugin reload; discovery looks back 7 days; dormant after 24 hours.
- [0012](decisions/0012-codex-peers.md): interactive Codex threads as peers (shared app server, queue, per-thread MCP server), and why Codex workers stay private.
- [0013](decisions/0013-peer-trust-and-hints.md): peer trust (a per-session switch, set by the owner, that lets a session act on peer requests without asking), the sender-relays rule, and hints about which session can help after a failed tool call.
- [0006](decisions/0006-phone-app.md): phone app, planned: same data and actions as the TUI, through one shared operator interface.
- [spike/](spike/): what we measured about each agent before building.
- [step2-design.md](step2-design.md): protocol and message design for the gateway, daemon and Claude and Pi adapters. Still a good description of the wire protocol.
- [step2-run.md](step2-run.md): how to run the pieces by hand, one by one.
- [step2-e2e.md](step2-e2e.md): results of the first end-to-end test.

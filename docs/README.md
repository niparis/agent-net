# Documentation

[Decision 0036](decisions/0036-primitives.md) defines the network primitives, including agents, sessions, tasks, edges, authority and autonomy.

## Use it

- [tour.md](tour.md): a tour of every TUI screen and what you can do on each.
- [principles.md](principles.md): the product principles that decide unclear design choices.
- [setup.md](setup.md): install, add a machine, update, remove. The details behind the README quickstart.
- [reference.md](reference.md): ports, files, environment variables, flags, and what setup changes on your machine.
- [security.md](security.md): the trust model, and what agent-net does not protect against.
- [troubleshooting.md](troubleshooting.md): first checks and fixes for common problems.
- [test-speed.md](test-speed.md): what made the slowest tests slow and how each was fixed (2026-11-12).
- [agent-guidance.md](agent-guidance.md): the text agent-net adds to your agents' instruction files.
- [../plugins/pi/README.md](../plugins/pi/README.md): the Pi extension.
- [mcp-registry.md](mcp-registry.md): the shared MCP server registry (tools of one machine's servers, used by agents on all machines).
- [upgrade.md](upgrade.md): how machines upgrade, and what happens to running sessions.
- [opencode.md](opencode.md): OpenCode sessions and the plugin.
- [codex.md](codex.md): Codex threads as peers: how they are found, how messages reach them, setup and limits.
- [antigravity.md](antigravity.md): Antigravity (`agy`): what agent-net does with it so far.

## Work on it

- [../AGENTS.md](../AGENTS.md): build, test, layout and conventions.
- [../RELEASING.md](../RELEASING.md): cut a release and update machines.
- [design/onboarding.md](design/onboarding.md): onboarding design.
- [design/start-sessions.md](design/start-sessions.md): proposed owner session starts.
- [design/tool-redesign.md](design/tool-redesign.md): the agent and CTO tool redesign: contracts, owner decisions and the task list.
- [design/repo-agent-split.md](design/repo-agent-split.md): design for separating repo and agent identity, ownership and free-text roles.
- [design/keys-audit.md](design/keys-audit.md): every TUI key, where the meanings collide, and a proposed consistent key map.
- [design/model-capability.md](design/model-capability.md): plan, on hold: the picker matches model capability C to task difficulty D, and review time is measured.

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
- [0014](decisions/0014-delegation-policy.md): delegation policy (the owner sets, per session or project, whether a session does the coding itself or hands it to a native subagent or a worker; applied on every turn).
- [0020](decisions/0020-worker-sandbox.md): unattended workers get a sandbox, not approval prompts (a level per project, Codex first). Amends 0002 W6.
- [0059](decisions/0059-worker-os-sandbox.md): an optional OS sandbox (bubblewrap on Linux) for Pi and OpenCode workers, per project in node.json; Claude and no-network starts are refused while it is on. Amends 0020.
- [0021](decisions/0021-network-integrations.md): one setup integrations question, answered once for the whole network.
- [0022](decisions/0022-component-graph-and-cto.md): built. A graph of components with charters (maturity included), edges with modes, and a CTO agent in the TUI that changes it by conversation.
- [0023](decisions/0023-model-lists-for-every-harness.md): model lists for every harness, loose names resolved for all of them, recent success ranking, and the start gap counting only starts that ran.
- [0024](decisions/0024-session-names.md): a session's name is its project name, duplicates get ` 2`/` 3`, workers are `<project> worker <n>`, and a harness title goes to TASK.
- [0032](decisions/0032-enter-enters-the-session.md): Enter opens the selected session (like `e`); the read-only conversation view is removed.
- [0033](decisions/0033-session-names-are-owner-only.md): only the owner renames a session; an announce sets no name, and the upgrade clears names that announce had set. Replaces rule 5 of 0024.
- [0034](decisions/0034-cto-box-is-read-only.md): the CTO box is read-only and Enter opens the full CTO; the box input, send path and `o` key are removed.
- [0035](decisions/0035-cto-is-a-standard-agent.md): the CTO becomes a standard agent with a session; every owner-only change is a proposal the owner approves, and it reads only public data. Design: [design/cto-as-agent.md](design/cto-as-agent.md).
- [0025](decisions/0025-install-quota-axi.md): agent-net installs a pinned quota-axi in its own data folder; an owner-installed copy always wins.
- [0042](decisions/0042-graph-pixel-renderer.md): the Graph tab draws a kitty pixel image when the terminal supports it, with Braille as the fallback; `AGENT_NET_GRAPH` selects `auto`, `pixel` or `braille`. Pixel mode adds message comets, a busy pulse, an eased camera and zoom-in charter cards, inside a frame budget.
- [0027](decisions/0027-skills-and-mcp-management.md): skills and MCP servers across machines: inventory, a private skill store, owner-gated assignments with undo, scoped MCP federation, and guarded removal of native MCP entries. Steps 1 to 7 and 9 are built; the Tools tab (step 8) is pending. Current behavior: [reference](reference.md#tools-inventory), [security](security.md#skills-and-mcp-management), [troubleshooting](troubleshooting.md#skills-and-mcp-management).
- [0028](decisions/0028-tui-hidden-items.md): shared hide preferences for TUI sessions and project nodes, stored in the gateway's owner settings.
- [0015](decisions/0015-machine-display-name.md): machine display name (the owner names a machine once, from the TUI; every machine shows it).
- [0018](decisions/0018-admit-workers-by-machine-load.md): workers start by recent CPU and memory load, with a queue and a choice of machine by headroom.
- [0019](decisions/0019-opencode-worker-logins.md): OpenCode workers get a private data folder and a copy of all the owner's OpenCode logins; the OAuth sign-out risk and when to revisit.
- [0006](decisions/0006-phone-app.md): phone app, planned: same data and actions as the TUI, through one shared operator interface.
- [0122](decisions/0122-web-console.md): the web console replaces the phone app: Desk and Sessions first, read and answer only. The design mockup is [design/web-console-mockup.html](design/web-console-mockup.html).
- [0141](decisions/0141-picker-override-needs-a-human.md): an agent cannot override the worker picker without a human: the owner's words or his confirmation on the Desk.
- [spike/](spike/): what we measured about each agent before building.
- [step2-design.md](step2-design.md): protocol and message design for the gateway, daemon and Claude and Pi adapters. Still a good description of the wire protocol.
- [step2-run.md](step2-run.md): how to run the pieces by hand, one by one.
- [step2-e2e.md](step2-e2e.md): results of the first end-to-end test.

# 0041 - Charters and rules are public to every agent, read-only

**Date:** 2026-11-01
**Status:** Accepted. Builds on [0022](0022-component-graph-and-cto.md),
[0014](0014-delegation-policy.md) and [0020](0020-worker-sandbox.md).

The owner decided, twice, that every charter and every rule is readable,
read-only, by the whole team: every agent on the network. The public graph read
`agent_graph` already returned charters, maturity and edges, but it hid all
node settings. That was too narrow. Rules like `deploy_rule` are settings, and
an agent must be able to check them. A real case: homelab-agent could not
confirm its own deploy authority.

So the public graph now returns, for every node:

- the charter fields and maturity, as before;
- the six node settings resolved against the maturity, with name and value:
  `tests_before_merge`, `merge_authority`, `worker_sandbox`,
  `judge_consumers`, `deploy_rule` and `autonomy`;
- the delegation policies the owner set for the node's project on each
  machine (`delegate`, `coder_model`, `main_model`, `workers`).

`agent_get` puts the same node rules on a session, so a session can check its
own rules directly. The CTO's `graph` and `session` views use the same reads,
so they match.

Only changes stay owner-gated: `node_settings_set` and `session_policy` are
still behind the owner gate. Read-only means an agent can see the rule and
cannot change it.

The parts that stay hidden are not rules:

- secrets and tokens;
- machine paths and locations;
- the learned-change history with its evidence excerpts;
- the owner's hidden-items preferences.

The public projection reads only the six known setting names. A setting name
that is not one of them, or a value the validator would refuse, is never
returned. That keeps a path or a secret that an old or broken writer stored in
the settings table out of every agent's answer. See
[security.md](../security.md).

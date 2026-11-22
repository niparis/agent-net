# Product principles

The rules agent-net is built on. When a design choice is unclear, these decide
it. They are kept up to date; the early brief ([brief.md](brief.md)) is history.
Where a principle is a goal that the product does not fully meet yet, it says so.

## The network

1. **Connect agents, do not replace them.** Every agent keeps its own harness,
   model, permissions and context. agent-net carries messages, wakes sessions
   and records work; it is not one big agent. Harness-specific code lives in
   adapters.
2. **Each agent owns a whole piece.** One agent per product, library or
   service, which it can change end to end. Never split agents by layer
   (a backend agent and a frontend agent): that shares one product's context
   across a line that should not exist.
3. **Specialize, do not centralize.** Many agents with narrow charters do
   repeated work better than one agent that does everything. A charter says
   what the agent does, accepts and refuses, and when it must ask.

## The owner's place in the loop

4. **Near the loop is the default, and the target.** Agents work on their own
   and stop only at a decision that needs you, with a result and a
   recommendation: workers, peer messages and delivery steps all work this way.
   Two other ways exist, each for a reason:
   - *Out of the loop,* to scale a mature project: an agent works toward its
     charter's purpose on a schedule and nobody prompts it. Standing loops
     ([decision 0094](decisions/0094-standing-loops.md)) do this today for any
     project you give a loop.
   - *In the loop,* for micromanagement or a crisis: you prompt an agent in a
     terminal and read every turn. It is the exception, not the way to work.
5. **Your attention is the scarce resource.** An ask reaches you only when it
   saves you time compared with opening the session yourself, and it comes
   with a recommendation. So you keep only what is truly yours: deleting data,
   spending money, secrets that can spend money, approving a new connected app
   or device, and choices about how you work. Everything reversible is decided
   by the agents and logged. The chief of staff sorts the asks for you.
6. **Agents can do what you can do.** Every action you can take in the TUI
   should have an equivalent the CTO can take for you, except the decisions in
   principle 5, which it can prepare but not make. Most have one today; the
   gaps are listed in [reference.md](reference.md) and are closed as they are
   found.
7. **No limits you did not ask for.** agent-net adds no approval gate, cap or
   refusal unless you asked for it. Agents may propose one; proposing is not
   adding.

## Trust and evidence

8. **Work leaves evidence.** A change moves through delivery steps (accept,
   test, merge, release and verify, plus review for riskier changes). Each step
   has an owner and records what it checked. A fix starts with a reproduction
   and a failing test. How independent a review must be is your setting; by
   default any review counts ([decision 0083](decisions/0083-review-independence-default-none.md)).
9. **The network learns.** Your corrections and decisions go into one memory
   that agents search before they ask. Each worker model builds a track
   record, and the picker uses it.

## Seeing what runs

10. **Measure everything that runs, and watch it.** The gateway counts every
    protocol request by origin and method. A number nobody reads finds nothing: a sharp
    rise in one origin's load should reach the agent that can fix it, without
    waiting for someone to look.
11. **Show what agents do in plain words.** A long run of tool calls should
    read as a few sentences, with the raw calls one step away. A summary helps
    you watch; it never replaces review of the result.

12. **Know exactly what your agents run.** Every skill, prompt and agent
    definition a session runs can be read in agent-net and traced to its
    source and version. Imported skills can today; prompts and agent
    definitions are on the roadmap.
13. **One source for each capability.** A skill, prompt or agent definition
    has one stored source. Copies can be rebuilt from it, and a copy that
    changes is either brought back or saved as a new version, never left as a
    silent fork. Today imported skills work this way; prompts and agent
    definitions are on the roadmap.

## Building it

14. **Performance first.** A feature that loads the gateway or the machine
    heavily is not done. Load data for what is on screen, push changes instead
    of polling, and measure before and after.
15. **Verify in the real setup.** A change is done when it works the way you
    will use it, measured on the real machines, not only in tests.
16. **Smart defaults, everything changeable.** Every role, model, rule and
    screen setting has a working default and can be changed by you, and most
    of them by the CTO for you.
17. **Plain words.** Docs, messages and screens use short sentences and
    concrete words.
18. **Each decision is made in one place.** Which model runs a worker, who
    leads a repo, whether a tool may run, where a message goes: for each such
    question, one place in the code decides. When two places decide the same
    thing, they drift apart, and two agents get two different answers. That is
    a bug, found and fixed like any other
    ([decision 0163](decisions/0163-split-decisions-are-defects.md)).

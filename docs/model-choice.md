# How agent-net chooses a model

Every time agent-net starts a session that runs a model, it asks one question:
which harness, provider, model and effort should this session run on? This page
explains how that answer is made, what you can rely on, and how to read the note
that comes with every choice.

It applies to every session start: workers (new, continued or resumed), the
difficulty judge, the CTO, the chief of staff, the mergers, and sessions you
start yourself. It does not run inside a running session, and it does not route
messages. The design is [decision 0162](decisions/0162-one-model-choice-path.md).

## The three steps

1. **Rank.** A list of candidates is made, best first. Which list depends on who
   starts the session:
   - **By cost.** For an automatic worker start, agent-net ranks the models that
     have a track record at this task difficulty by expected cost per accepted
     result (decision [0161](decisions/0161-pick-by-cost-per-accepted-result.md)).
   - **A fixed list.** A model you name, or an ordered list: the CTO, the chief
     of staff and the mergers each have one, and the difficulty judge uses the
     registered worker profiles.
   - **The same model.** A continued or resumed worker keeps the model it ran on,
     so its conversation carries on.
2. **Reorder.** Optional steps may move candidates up or down. They never remove
   one.
   - **Pace:** a provider behind its quota pace moves up; one ahead of it moves
     down.
   - **Protected providers last:** providers you keep for yourself go to the end.
   - **Rotation:** equal candidates take turns.
3. **Check.** The first candidate on the list that can run now is used. For each
   candidate it skips, agent-net writes one line saying why.

If the chosen candidate fails at launch, a worker start takes the next one on
the same list and records the failure. A start that waits in a machine's queue
is checked again when it leaves the queue, because quota can run out while it
waits.

A role session (the CTO, the chief of staff, a merger) resumes on the model it
already runs. Only if a locked check refuses that model does it fall to the next
entry of its list, with a new conversation. A refused role start is never
silent: the reason is one line on your screen.

## Checks that always apply

These checks have no switch. No caller, agent or setting can turn them off:

- the harness is installed and signed in on the machine;
- the provider's sign-in works;
- the provider has quota left (it is not at 0%, and it does not refuse starts);
- no running session has reported that the provider's limit is reached.

When none of the candidates passes them, the start is refused with the reasons,
before any model is called. A model you name yourself goes through them too, so
you see "no quota left" at once instead of a session that dies on its first turn.

## Checks each kind of start chooses

These checks protect your quota. Each kind of start has a fixed setting for
them, listed in one table:

- **Worker floor:** keep a share of each subscription for you (set in the
  Settings tab).
- **90% block:** avoid a provider that has used 90% of a window.
- **Burn hold:** avoid a provider that the running workers would empty before
  its reset.
- **Recent failures:** avoid a model that failed to start a moment ago, unless
  nothing else is left.

| Start | List | Floor | 90% | Burn hold | Recent failures | Pace | Protected last | Rotation |
|---|---|---|---|---|---|---|---|---|
| Worker, chosen by agent-net | by cost | yes | yes | yes | yes | yes | yes | yes |
| Worker, model named by an agent | fixed | yes | yes | yes | yes | no | no | no |
| Worker or session, model named by you | fixed | no | no | no | no | no | no | no |
| Continue or resume a worker | same model | no | no | no | no | no | no | no |
| Difficulty judge | fixed list | yes | yes | yes | yes | yes | yes | no |
| CTO, chief of staff, merger | fixed list | no | no | no | yes | no | no | no |

Notes on the table:

- **A model you name** gets only the checks that always apply. Your own words
  win over the quota protections; they exist to protect your quota from agents,
  not from you.
- **A continued or resumed worker** keeps its model. If that provider has run
  out, the continue is refused and you, or the agent that started it, start a
  new worker on the same branch. A different model would start without the old
  conversation.
- **The CTO, the chief of staff and the mergers** run the system, so they do not
  stop at the quota protections. They skip a model that just failed, and move to
  the next model in their list.
- **Previews,** such as the picker preview in the TUI and the picker backtest,
  use the same steps without starting anything, so a preview shows the choice a
  real start would make.

## Reading the note

Every start carries a note made of the same parts, for every kind of start:

```
<chosen candidate> (<why it ranked first>);
skipped <candidate>: <reason>; skipped <candidate>: <reason>; ...
```

Examples of skip reasons:

- `opencode-go is exhausted (monthly; it resets at 2026-11-20 00:00 UTC)` (always applies)
- `harness claude is not signed in on research` (always applies)
- `openai has 14% left in its week window, under your 20% worker floor` (floor)
- `held back for quota: the running workers would empty codex before its reset`
  (burn hold)

You find the note in the worker notice, in `agent_workers_find` with
`detail: full`, and in the TUI worker log.

## What you can change

- **The role model lists** are settings. Each is an ordered list, one full
  candidate per line as harness, provider, model and effort. The first entry
  that passes the checks is used, and the harness changes with the entry:
  - the CTO: `cto.models`, and its current choice with `agent-net cto` or the
    CTO's own settings screen;
  - the chief of staff: `chief.model`;
  - the mergers: `merger.models`.

  An install with a saved model starts with that model as a one-entry list, so
  nothing changes until you edit it. Ask the CTO to change a list with its
  settings tools; it shows the change and can undo it.
- **The difficulty judge** uses the registered worker profiles, not a separate
  list. Add or remove a profile with `agent-net worker profile`.
- **The worker floor** per provider, in the Settings tab.
- **Protected providers,** in the Settings tab.

Which optional checks each kind of start uses is fixed in code, in one table,
and a test keeps it as written here. A change to it is a design decision for
you, not a setting an agent can change.

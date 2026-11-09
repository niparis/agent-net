# Typed decision models for bounded choices

Status: idea, on the roadmap. Nothing is built. Written 2026-11-09 after the
owner noticed that worker choice fits a decision model.

## What a decision model is

A decision model returns a typed answer in one pass instead of generating
text: one choice from a fixed set, a number, or yes or no, each with a
calibrated probability. It cannot write a reply, code or an explanation.

The example that prompted this note is **Jev** from TypeSafe AI (San
Francisco), a "System One" decision model in limited early access since
September 15, 2026. It is proprietary, reached through TypeSafe's API,
OpenRouter and Vercel AI Gateway. TypeSafe has published no weights,
architecture description or paper.

Caveats from the launch coverage:

- "Cannot hallucinate" covers the format of the answer, not whether the
  answer is right.
- Price and benchmark claims come from promotional sources; check them
  against TypeSafe's own material before relying on them.
- Every call costs money, so trying one is the owner's decision.

## Where agent-net makes bounded choices

Each of these picks from a fixed set today, by rules, scores or an LLM that
writes text:

| Decision | Answer shape | Today |
|---|---|---|
| Which worker profile takes a brief | one of the registered profiles | the picker: track record per model and difficulty level, quota pace, rotation, 10% exploration |
| How hard a brief is | a level from trivial to very complex | an LLM judge rates six points from 1 to 5 |
| What the chief of staff does with an ask | close, merge, send on, or owner with a priority | an LLM agent with a charter |
| Whether a message is a request or a notification | one of two | the sender chooses; senders often get it wrong |
| Whether to compact an idle session | yes or no | fixed rules: idle time, token count, not being viewed |
| Whether a change needs review | yes or no | risk score, difficulty level and sensitive paths |

Worker choice is the best fit: the answer is literally one item from a list,
the outcome of every pick is already recorded (done or not, rework, defects
found later), and a calibrated probability per profile would replace the
hand-tuned scores and the fixed exploration share.

## How to try it

1. **Shadow first.** Call the decision model next to the current picker for
   every worker start, store its pick and probability, and change nothing.
2. **Compare with outcomes.** After a few weeks, compare its picks with the
   recorded outcomes of the picks the picker made, per difficulty level.
3. **Decide with the owner** whether it may make the pick, and for which
   decisions. It costs money, so the owner decides.

The same shadow run can cover the difficulty judge and the request or
notification choice, where wrong answers are cheap to measure.

## Sources

- [DataNorth: Jev, what it is and why this new model matters](https://datanorth.ai/blog/jev-what-it-is-and-why-this-new-model-matters)
- [Thoughtworks: Jev decision models](https://www.thoughtworks.com/insights/blog/generative-ai/jev-decision-models-what-need-know-why)
- [HackerNoon: Inside Jev](https://hackernoon.com/inside-jev-how-decision-models-trade-text-generation-for-typed-calibrated-answers)
- [Agence Scroll: Jev, TypeSafe's System One model](https://agence-scroll.com/en/blog/jev-typesafe-system-one-ai-model)

# 0026: Worker difficulty and model scores

A worker start may name a difficulty. Automatic selection compares each registered worker profile available on the target machine's capability score with the minimum for that difficulty, then uses quota room and recent model failures. The default minimums for trivial, simple, normal, hard and very_complex are 0, 20, 30, 40 and 50. A difference of at least 10 is excellent, 0 to under 10 is good, -10 to under 0 is poor, and less than -10 is denied. An unscored model is not eligible.

The embedded table records scores from [Artificial Analysis](https://artificialanalysis.ai/), including effort levels. The gateway keeps owner overrides, which take precedence. Only the owner can change overrides or difficulty minimums. A model with no explicit effort uses its harness default when known, otherwise medium, then a default entry.

The chooser skips exhausted windows and windows above 90 percent used. It compares elapsed share minus used share and subtracts five points for each running worker on the provider. Stale readings do not decide a choice. Equal choices rotate when usable readings are absent. The start result explains the selection. An empty roster uses the harness default and the result tells the caller how to register a worker. An explicit model keeps the existing resolution rules.

The gateway stores a roster of named harness, model and optional effort profiles with creator and creation time. The CTO, the owner CLI, and non-worker agent sessions can manage it; the model must be listed by a machine. The Quotas view shows registered profiles, scores and fit, recent use and current picks. It credits Artificial Analysis when its scores appear.

Automatic choices use the owner's worker provider allowlist, or subscription and quota-reading defaults when unset; equal-fit and equal-room choices prefer a recent successful worker.

## Protected providers

The owner may protect providers used by the orchestrator, because workers on the same subscription spend the limit the orchestrator needs to keep working. When protection is set, automatic selection first considers unprotected profiles at or above the difficulty minimum, then protected profiles if none qualify or all are out of quota. The result explains the choice. Explicit model requests remain available and carry a note. The owner can change the list and reason; setup only suggests it.

## Good enough and spread the load

Automatic selection requires a score at or above the difficulty minimum, treating good and excellent as adequate. Unprotected providers still come first. A model with fewer than half complete among at least five complete, partial and failed runs in the recent track record ranks behind other adequate candidates. Stopped and timed-out runs do not count in this ratio.

Next, the chooser prefers the provider furthest behind its quota pace, using the most constrained measured window and subtracting five points for each running worker. A missing or stale reading is on pace. Exhausted windows and windows above 90 percent remain ineligible. Equal pace favors the lowest adequate score, keeping stronger models available for harder work. A strong track record and recent success break remaining ties, then equal choices rotate. The start note explains the score, quota pace and any protected-provider fallback.

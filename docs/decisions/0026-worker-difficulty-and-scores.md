# 0026: Worker difficulty and model scores

Worker starts may name a difficulty. Automatic model selection uses an owner-controlled capability score and minimum score for each difficulty, then subscription quota room and recent failures. Unknown scores are not eligible for automatic selection. Explicit model requests retain their existing behavior.

The embedded score table records the source of each score. Gateway owner overrides take precedence. Only the owner may change overrides and difficulty thresholds. Quota readings that are stale or unavailable cannot decide a choice. When no quota readings are available, eligible models rotate.

The Quotas view shows scores, fit at each difficulty, and the current choices. Attribution appears when Artificial Analysis scores are present.

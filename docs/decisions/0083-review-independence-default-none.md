# 0083. Review independence is not required by default

Date: 2026-11-08
Status: Built.

The owner, on 2026-11-08, about the rule that a review step only counts when
the reviewer is a different model family from the author: "that's not a bug,
that's a limit you have invented". He never asked for it.

`delivery.pipeline.review.independent` stays, so the owner can turn the limit
back on, but its default is now `none`. Any review pass counts, including the
caller's review of its own change. `session` requires another session and
`family` keeps the old rule of another model maker. The guidance no longer
tells callers to use `not_family` for an independent review.

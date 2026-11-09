# See a change merged through a GitHub pull request

Leads review and merge worker changes in GitHub, including squash and rebase
merges. Those merges create new commits, so the change head is not an ancestor
of the remote default branch. The git-only check in decision 0085 never fires,
and the change stays open as "not accepted yet".

The daemon keeps the ancestry check. When that check finds nothing and a change
is open with a branch, it asks GitHub once per repository per ten-minute pass:

    gh pr list --state merged --limit 100 \
      --json number,headRefName,headRefOid,mergeCommit,mergedAt,url

A PR matches only when its head branch equals the change branch and its head
commit equals the change head exactly. A PR merged from an older head is not
this revision. When one merged PR matches, the daemon calls
`gh pr view <number> --json statusCheckRollup` once and reports the PR number,
URL, merge commit and whether every check passed in `change.reconcile`.

The gateway records the merge evidence as before. For a PR merge it also
records accept with the note "merged on GitHub by PR #N (<url>); the lead's
review there", and records the before-merge test with the same note only when
the checks passed. It never records a test pass without that evidence. The
change stays Merged, so the after-merge steps run and verify stays for the
caller; it becomes Done only when every after-merge step passed or was skipped.

A missing gh, a signed-out gh, a non-GitHub origin or any gh error leaves the
git observations of that pass in place and is logged. The loop never fails for
it. There are no new settings and no new limits.

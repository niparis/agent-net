package protocol

// FrameChangeReconcile is sent only by the daemon holding the change's repo.
// Its git observation closes work finished outside the delivery pipeline.
const FrameChangeReconcile = "change.reconcile"

// ChangeReconcileRequest binds the observation to the exact revision checked.
// Kind is external (the work is on origin's default branch), abandoned (branch
// absent locally and remotely) or no_work (head equals base, so nothing shows
// whether the work was merged). The gateway checks age and evidence. A no_work
// observation changes nothing on the change; it tells the caller once.
//
// Proof says how an external merge was shown: ancestor (head is reachable from
// the default branch), cherry (every commit has a patch-equivalent on it) or
// squash (one commit on it has the tree the head would give). A PR merge sets
// PRNumber instead. An empty Proof is an older daemon.
type ChangeReconcileRequest struct {
	ChangeID     string `json:"change_id"`
	Revision     int    `json:"revision"`
	HeadCommit   string `json:"head_commit"`
	Kind         string `json:"kind"`
	MergedCommit string `json:"merged_commit,omitempty"`
	Proof        string `json:"proof,omitempty"`
	// PRNumber, PRURL and PRChecksPassed accompany an external merge made
	// through a GitHub pull request. A zero PRNumber is a plain git merge.
	PRNumber       int    `json:"pr_number,omitempty"`
	PRURL          string `json:"pr_url,omitempty"`
	PRChecksPassed bool   `json:"pr_checks_passed,omitempty"`
}

// Reconcile kinds and external merge proofs.
const (
	ReconcileExternal  = "external"
	ReconcileAbandoned = "abandoned"
	ReconcileNoWork    = "no_work"

	MergeProofAncestor = "ancestor"
	MergeProofCherry   = "cherry"
	MergeProofSquash   = "squash"
)

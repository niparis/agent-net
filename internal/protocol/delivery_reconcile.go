package protocol

// FrameChangeReconcile is sent only by the daemon holding the change's repo.
// Its git observation closes work finished outside the delivery pipeline.
const FrameChangeReconcile = "change.reconcile"

// ChangeReconcileRequest binds the observation to the exact revision checked.
// Kind is external (head reachable from origin's default branch) or abandoned
// (branch absent locally and remotely). The gateway checks age and evidence.
type ChangeReconcileRequest struct {
	ChangeID     string `json:"change_id"`
	Revision     int    `json:"revision"`
	HeadCommit   string `json:"head_commit"`
	Kind         string `json:"kind"`
	MergedCommit string `json:"merged_commit,omitempty"`
	// PRNumber, PRURL and PRChecksPassed accompany an external merge made
	// through a GitHub pull request. A zero PRNumber is a plain git merge.
	PRNumber       int    `json:"pr_number,omitempty"`
	PRURL          string `json:"pr_url,omitempty"`
	PRChecksPassed bool   `json:"pr_checks_passed,omitempty"`
}

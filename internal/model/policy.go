package model

// Purpose names one kind of caller. The names are stable so a preview can ask
// for the row of the caller it previews.
type Purpose string

const (
	// PurposeWorkerAutomatic is an automatic worker start: every optional step.
	PurposeWorkerAutomatic Purpose = "worker-automatic"
	// PurposeWorkerNamed is an agent naming the model for its worker.
	PurposeWorkerNamed Purpose = "worker-named"
	// PurposeOwnerNamed is the owner naming the model for a worker or session.
	PurposeOwnerNamed Purpose = "owner-named"
	// PurposeContinueOrResume continues or resumes a task on its own model.
	PurposeContinueOrResume Purpose = "continue-or-resume"
	// PurposeDifficultyJudge is the difficulty judge start.
	PurposeDifficultyJudge Purpose = "difficulty-judge"
	// PurposeRole is the CTO, the chief of staff and the merger.
	PurposeRole Purpose = "cto-chief-merger"
	// PurposePreview is a preview or a backtest: decide, do not start.
	PurposePreview Purpose = "preview-backtest"
)

// Ranker names the rule that orders a caller's candidates.
type Ranker string

const (
	// RankCost is the cost ranker of decision 0161.
	RankCost Ranker = "cost"
	// RankFixed is an ordered list from a setting or from the caller.
	RankFixed Ranker = "fixed"
	// RankSame is a list of one, for continue and resume.
	RankSame Ranker = "same"
)

// Policy is one row of the table in decision 0162. A caller does not change
// its row at run time; the owner's own start is the one exception, and the
// start turns the floor off before it asks for the row.
type Policy struct {
	Ranker        Ranker
	Checks        Checks
	Pace          bool
	ProtectedLast bool
	Rotation      bool
	// DecideOnly is set for a preview or backtest: the path returns the
	// candidate but starts nothing.
	DecideOnly bool
}

// PolicyFor is the one table that says what each caller gets. A purpose with
// no row gets no checks, which keeps a new route from silently inheriting the
// automatic worker's policy.
func PolicyFor(purpose Purpose) Policy {
	switch purpose {
	case PurposeWorkerAutomatic:
		return Policy{
			Ranker:        RankCost,
			Checks:        Checks{Floor: true, Block90: true, Burn: true, Cooling: true},
			Pace:          true,
			ProtectedLast: true,
			Rotation:      true,
		}
	case PurposeWorkerNamed:
		return Policy{
			Ranker: RankFixed,
			Checks: Checks{Floor: true, Block90: true, Burn: true, Cooling: true},
		}
	case PurposeOwnerNamed:
		return Policy{Ranker: RankFixed}
	case PurposeContinueOrResume:
		return Policy{Ranker: RankSame}
	case PurposeDifficultyJudge:
		return Policy{
			Ranker:        RankFixed,
			Checks:        Checks{Floor: true, Block90: true, Burn: true, Cooling: true},
			Pace:          true,
			ProtectedLast: true,
		}
	case PurposeRole:
		return Policy{
			Ranker: RankFixed,
			Checks: Checks{Cooling: true},
		}
	case PurposePreview:
		policy := PolicyFor(PurposeWorkerAutomatic)
		policy.DecideOnly = true
		return policy
	}
	return Policy{}
}

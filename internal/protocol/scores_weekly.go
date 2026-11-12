package protocol

const OpScoresWeekly = "scores_weekly"

// CauseCounts is the weekly split of the recorded causes of rework. Machine
// covers quota_auth, sandbox and harness; Work covers test_failure and
// accept_finding; Brief covers brief_defect. Caught and Escaped split the
// defects found later by DefectPhase.
type CauseCounts struct {
	Machine int `json:"machine,omitempty"`
	Work    int `json:"work,omitempty"`
	Brief   int `json:"brief,omitempty"`
	Caught  int `json:"caught,omitempty"`
	Escaped int `json:"escaped,omitempty"`
}

// ScoresWeeklyRow is one ISO week's summary in the weekly scores table.
type ScoresWeeklyRow struct {
	ProofUnavailable map[string]int `json:"proof_unavailable,omitempty"`
	Week             string         `json:"week"`
	Runs             int            `json:"runs"`
	MeanScore        float64        `json:"mean_score"`
	EarlyQuitRate    float64        `json:"early_quit_rate"`
	MeanFixRounds    float64        `json:"mean_fix_rounds"`
	BriefDefects     int            `json:"brief_defects"`
	Defects          int            `json:"defects"`
	MeanTokens       int64          `json:"mean_tokens"`
	Causes           CauseCounts    `json:"causes,omitempty"`
	Releases         []string       `json:"releases,omitempty"`
}

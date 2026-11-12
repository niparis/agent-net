package protocol

const OpScoresWeekly = "scores_weekly"

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
	Releases         []string       `json:"releases,omitempty"`
}

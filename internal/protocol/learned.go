package protocol

// LearnedEvidence identifies a signal and retains the excerpt used by the rule.
type LearnedEvidence struct {
	ID      string `json:"id"`
	Excerpt string `json:"excerpt"`
}

// LearnedSuggestion is a candidate graph change. A pending candidate is ready
// only after ReadyAt; reads do not mutate it.
type LearnedSuggestion struct {
	ID        int64             `json:"id"`
	Node      string            `json:"node"`
	Field     string            `json:"field"`
	EdgeKind  string            `json:"edge_kind,omitempty"`
	Kind      string            `json:"kind"`
	Phrase    string            `json:"phrase"`
	Evidence  []LearnedEvidence `json:"evidence"`
	Status    string            `json:"status"`
	Severity  string            `json:"severity"`
	Origin    string            `json:"origin,omitempty"`
	HistoryID int64             `json:"history_id,omitempty"`
	RawPhrase string            `json:"raw_phrase,omitempty"`
	CreatedAt int64             `json:"created_at"`
	ReadyAt   int64             `json:"ready_at"`
	DecidedAt int64             `json:"decided_at,omitempty"`
	Unseen    bool              `json:"unseen,omitempty"`
	OldValue  string            `json:"old_value,omitempty"`
	NewValue  string            `json:"new_value,omitempty"`
	By        string            `json:"by,omitempty"`
}

package protocol

// Team is an additive grouping identity. It grants no authority or settings.
type Team struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Purpose   string    `json:"purpose"`
	Owner     *GraphRef `json:"owner,omitempty"`
	Active    bool      `json:"active"`
	CreatedAt int64     `json:"created_at"`
	UpdatedAt int64     `json:"updated_at"`
}

// TeamWrite is owner-only. Membership ID is the new Team, empty to remove.
// ExpectTeam is the old Team ID, empty for an unassigned member. Moves are
// atomic. Rename requires ExpectName. Ownership is set at creation only.
type TeamWrite struct {
	Action       string    `json:"action"`
	ID           string    `json:"team_id"`
	Name         string    `json:"name,omitempty"`
	Purpose      string    `json:"purpose,omitempty"`
	Owner        *GraphRef `json:"owner,omitempty"`
	Member       *GraphRef `json:"member,omitempty"`
	ExpectTeam   string    `json:"expect_team,omitempty"`
	ExpectName   string    `json:"expect_name,omitempty"`
	OwnerSession string    `json:"owner_session,omitempty"`
}

// TeamChange retains immutable before/after JSON snapshots or Team IDs for a
// membership change. A move is one row, visible in both Teams' histories.
type TeamChange struct {
	ID        int64     `json:"id"`
	Action    string    `json:"action"`
	TeamID    string    `json:"team_id"`
	OldTeamID string    `json:"old_team_id,omitempty"`
	Member    *GraphRef `json:"member,omitempty"`
	OldValue  string    `json:"old_value"`
	NewValue  string    `json:"new_value"`
	At        int64     `json:"at"`
	By        string    `json:"by"`
}

package protocol

// UIRequest is a CTO instruction or a daemon notice delivered to local TUIs.
type UIRequest struct {
	Tab    string `json:"tab,omitempty"`
	Select string `json:"select,omitempty"`
	Filter string `json:"filter,omitempty"`
	Why    string `json:"why,omitempty"`
	Text   string `json:"text,omitempty"`
	// HerdrOpened marks a daemon notice, not text from the CTO.
	HerdrOpened bool `json:"herdr_opened,omitempty"`
	// DaemonNotice marks other session-start results from the daemon.
	DaemonNotice bool `json:"daemon_notice,omitempty"`
	// StartPlan is sent only to the owner TUI that approved a session start
	// when the command needs its terminal because Herdr is unavailable.
	StartPlan       *EnterPlan `json:"start_plan,omitempty"`
	StartProposalID string     `json:"start_proposal_id,omitempty"`
	StartName       string     `json:"start_name,omitempty"`
}

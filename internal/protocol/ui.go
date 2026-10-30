package protocol

// UIRequest is a CTO instruction delivered to every local TUI.
type UIRequest struct {
	Tab    string `json:"tab,omitempty"`
	Select string `json:"select,omitempty"`
	Filter string `json:"filter,omitempty"`
	Why    string `json:"why,omitempty"`
	Text   string `json:"text,omitempty"`
}

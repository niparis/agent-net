package protocol

import "encoding/json"

// CTOSettings is the owner's network-wide model preference. An empty model
// requests the machine's default.
type CTOSettings struct {
	Model string `json:"model"`
}

// CTOSettingsRequest reads the preference when Model is nil and saves it
// otherwise. A pointer permits the owner to reset it to the default.
type CTOSettingsRequest struct {
	Model *string `json:"model,omitempty"`
}

const OpCTOSettings = "cto_settings"

const (
	OpCTOSend         = "cto_send"
	OpCTONew          = "cto_new"
	OpCTOEvents       = "cto_events"
	OpCTOGraphHistory = "cto_graph_history"
)

type CTOSendRequest struct {
	Text string `json:"text"`
}
type CTOEventsRequest struct {
	Index int `json:"index"`
}
type CTOAction struct {
	ID   int64  `json:"id"`
	Text string `json:"text"`
	At   int64  `json:"at"`
}

type CTOEvents struct {
	Actions  []CTOAction       `json:"actions"`
	Messages []json.RawMessage `json:"messages"`
	Next     int               `json:"next"`
}

// CTOGraphChange is an attributed graph history entry, oldest first.
type CTOGraphChange struct {
	ID       int64  `json:"id"`
	Kind     string `json:"kind"`
	Node     string `json:"node"`
	Attr     string `json:"attr"`
	OldValue string `json:"old_value"`
	NewValue string `json:"new_value"`
	At       int64  `json:"at"`
}

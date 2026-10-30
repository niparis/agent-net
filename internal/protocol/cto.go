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
	OpCTOSend   = "cto_send"
	OpCTONew    = "cto_new"
	OpCTOEvents = "cto_events"
)

type CTOSendRequest struct {
	Text string `json:"text"`
}
type CTOEventsRequest struct {
	Index int `json:"index"`
}
type CTOEvents struct {
	Messages []json.RawMessage `json:"messages"`
	Next     int               `json:"next"`
}

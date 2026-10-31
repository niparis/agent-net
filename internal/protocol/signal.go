package protocol

// CharterSignal is evidence of an instruction or a reply to a request.
type CharterSignal struct {
	From       string `json:"from"`
	To         string `json:"to"`
	MessageID  string `json:"message_id"`
	Prompt     string `json:"prompt,omitempty"`
	ReplyID    string `json:"reply_id,omitempty"`
	Outcome    string `json:"outcome,omitempty"`
	Reason     string `json:"reason,omitempty"`
	RedirectTo string `json:"redirect_to,omitempty"`
	At         int64  `json:"at"`
}

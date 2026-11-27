package protocol

import "errors"

// The chief of staff is a standard agent session. Agents that need the owner
// send requests to it with agent_message_send (decision 0196).

// ChiefModelKey is the settings key of the model the chief session runs on.
const (
	ChiefModelKey     = "chief.model"
	DefaultChiefModel = "codex · openai · gpt-6.1-sol · medium"
)

// Operator methods of the owner's TUI and the CTO: the chief's status and
// setting.
const (
	OpChiefGet = "chief_get"
	OpChiefSet = "chief_set"
)

// ChiefSetRequest changes the chief of staff. It does what it names and
// nothing else: SessionID names the chief session, Clear unassigns it, and
// Harness, Provider, Model and Effort set the chief.model setting. A request
// that names none of them is refused, so a call can never unassign the chief
// by accident. Model fields not given keep their value from the current
// setting. The fields may come in one call or in separate calls.
type ChiefSetRequest struct {
	SessionID string `json:"session_id,omitempty" jsonschema:"the live agent session to make the chief"`
	Clear     bool   `json:"clear,omitempty" jsonschema:"true unassigns the chief; it cannot be combined with other fields"`
	Harness   string `json:"harness,omitempty" jsonschema:"harness of the chief model, for example claude"`
	Provider  string `json:"provider,omitempty" jsonschema:"provider of the chief model; derived from the model when empty"`
	Model     string `json:"model,omitempty" jsonschema:"chief model, resolved like a worker model; a model no machine lists is refused"`
	Effort    string `json:"effort,omitempty" jsonschema:"effort of the chief model, for example medium"`
	// Models replaces the whole ordered list. The single fields above change
	// its first entry, which is the chief's current choice.
	Models []RoleModel `json:"models,omitempty" jsonschema:"ordered chief model list; each entry is a full candidate"`
}

// HasModel reports whether the request changes the chief's model.
func (r ChiefSetRequest) HasModel() bool {
	return r.Harness != "" || r.Provider != "" || r.Model != "" || r.Effort != "" || len(r.Models) > 0
}

// Validate refuses a request that names nothing to do, or that asks to clear
// the chief and set something in the same call.
func (r ChiefSetRequest) Validate() error {
	switch {
	case r.Clear && (r.SessionID != "" || r.HasModel()):
		return errors.New("clear cannot be combined with session_id or model fields")
	case !r.Clear && r.SessionID == "" && !r.HasModel():
		return errors.New("chief_set needs session_id, clear: true, or model fields (harness, provider, model, effort)")
	}
	return nil
}

// ChiefStatus is the registered chief and the model it is set to run on.
type ChiefStatus struct {
	SessionID string `json:"session_id"`
	Name      string `json:"name,omitempty"`
	Online    bool   `json:"online"`
	Model     string `json:"model"`
	// Down is why the chief cannot work now, in a few words, or "" when it
	// can (or when there is no chief).
	Down string `json:"down,omitempty"`
	// ClearedAt is when the owner or the CTO cleared the chief (Unix nanos),
	// while that clear stands. It stands until a session is named chief or
	// chief.model changes. Nothing starts a chief while it does.
	ClearedAt int64 `json:"cleared_at,omitempty"`
}

// WakeHarness tells whether a message can wake an offline session of this
// harness. Pi cannot: its rpc mode does not open an existing session. A role
// session (the chief, a merger) runs only on a harness that can be woken
// (decision 0171).
func WakeHarness(h string) bool { return h == "claude" || h == "codex" || h == "opencode" }

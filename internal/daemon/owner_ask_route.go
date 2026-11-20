package daemon

import (
	"encoding/json"
	"errors"
)

// ownerAskRoute turns the params of the agent_owner_ask tool into the daemon
// call for its kind. Every harness sends the tool the same way and this is
// the one place that picks the kind. A call that only names an ask to replace
// is a question.
func ownerAskRoute(raw json.RawMessage) (string, json.RawMessage, error) {
	var p struct {
		Kind           string   `json:"kind"`
		Text           string   `json:"text"`
		Recommendation string   `json:"recommendation"`
		Options        []string `json:"options"`
		Replaces       string   `json:"replaces"`
	}
	if len(raw) > 0 && string(raw) != "null" && json.Unmarshal(raw, &p) != nil {
		return "", nil, errors.New("bad owner_ask params")
	}
	if p.Kind == "" && p.Replaces != "" {
		p.Kind = "question"
	}
	var method string
	params := map[string]any{"recommendation": p.Recommendation, "replaces": p.Replaces}
	switch p.Kind {
	case "question":
		method = "ask_question"
		params["text"], params["options"] = p.Text, p.Options
	case "approval":
		method = "request_approval"
		params["action"], params["options"] = p.Text, p.Options
	case "idea":
		method = "propose_idea"
		params["text"] = p.Text
	default:
		return "", nil, errors.New("kind must be question, approval or idea")
	}
	out, err := json.Marshal(params)
	return method, out, err
}

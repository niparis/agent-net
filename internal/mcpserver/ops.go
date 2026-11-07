package mcpserver

// One call implementation per operation. The new tools and their old-name
// aliases both go through these functions, so an alias adds no second path to
// the daemon. They return the daemon's own result, which lets an alias keep the
// old result shape while the new tool shapes its catalog answer.

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/niparis/agent-net/internal/protocol"
)

// opResult turns one daemon answer into an object. An empty answer is an empty
// object, as the old tools already reported it.
func opResult(out any, err error) (map[string]any, error) {
	if err != nil {
		return nil, err
	}
	m, _ := out.(map[string]any)
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

func workerStartCall(ctx context.Context, run func(context.Context, string, any) (any, error), in protocol.WorkerStartRequest) (map[string]any, error) {
	return opResult(run(ctx, protocol.OpWorkerStart, in))
}

func workerStopCall(ctx context.Context, run func(context.Context, string, any) (any, error), session string) (map[string]any, error) {
	return opResult(run(ctx, protocol.OpWorkerStop, protocol.WorkerStopRequest{Session: session}))
}

func rateCall(ctx context.Context, run func(context.Context, string, any) (any, error), in protocol.RateRequest) (map[string]any, error) {
	return opResult(run(ctx, protocol.OpRate, in))
}

func handbackCall(ctx context.Context, run func(context.Context, string, any) (any, error), in protocol.WorkerHandbackRequest) (map[string]any, error) {
	return opResult(run(ctx, protocol.OpWorkerHandback, in))
}

func announceCall(ctx context.Context, run func(context.Context, string, any) (any, error), in announceIn) (map[string]any, error) {
	return opResult(run(ctx, "announce", in))
}

func learnCall(ctx context.Context, run func(context.Context, string, any) (any, error), in protocol.LearnRequest) (map[string]any, error) {
	return opResult(run(ctx, protocol.OpLearn, in))
}

func projectAddCall(ctx context.Context, run func(context.Context, string, any) (any, error), in protocol.ProjectAddRequest) (map[string]any, error) {
	return opResult(run(ctx, "project_add", in))
}

func mcpTestCall(ctx context.Context, run func(context.Context, string, any) (any, error), server string) (map[string]any, error) {
	return opResult(run(ctx, "mcp_test", map[string]any{"name": server}))
}

// ownerAskCall is the one owner request operation. Kind picks the daemon
// method: question asks the owner, approval asks for one action, idea sends a
// feature idea.
func ownerAskCall(ctx context.Context, run func(context.Context, string, any) (any, error), kind, text, recommendation string, options []string, urgency string, replaces ...string) (map[string]any, error) {
	repl := ""
	if len(replaces) > 0 {
		repl = replaces[0]
	}
	if kind == "" && repl != "" {
		kind = "question"
	}
	switch kind {
	case "question":
		return opResult(run(ctx, "ask_question", map[string]any{"kind": "question", "text": text, "recommendation": recommendation, "options": options, "replaces": repl}))
	case "approval":
		return opResult(run(ctx, "request_approval", map[string]any{"action": text, "recommendation": recommendation, "options": options, "replaces": repl}))
	case "idea":
		return opResult(run(ctx, "propose_idea", map[string]any{"text": text, "recommendation": recommendation, "replaces": repl}))
	}
	return nil, errors.New("kind must be question, approval or idea")
}

// sendCall sends one message. receipt asks the daemon for a receipt, which is
// what the new tool reports; the old tool keeps the sent-message answer.
func sendCall(ctx context.Context, run func(context.Context, string, any) (any, error), to string, set map[string]any, body, kind string, asOwner, receipt bool) (map[string]any, error) {
	params := map[string]any{
		"body":            body,
		"type":            kind,
		"as_owner":        asOwner,
		"receipt":         receipt,
		"idempotency_key": protocol.NewID("k"),
	}
	if set != nil {
		params["to_set"] = set
	} else {
		params["to"] = to
	}
	return opResult(run(ctx, "send", params))
}

// replyCall replies to one message with the same outcome fields the new tool
// uses.
func replyCall(ctx context.Context, run func(context.Context, string, any) (any, error), in messageReplyIn, outcome protocol.ReplyOutcome, receipt bool) (map[string]any, error) {
	params := map[string]any{
		"message_id":      in.Message,
		"body":            in.Body,
		"outcome":         outcome.Outcome,
		"reason":          outcome.Reason,
		"redirect_to":     outcome.RedirectTo,
		"recommendation":  outcome.Recommendation,
		"receipt":         receipt,
		"idempotency_key": protocol.NewID("k"),
	}
	return opResult(run(ctx, "reply", params))
}

// decodeAny is the old tools' pass-through decode: it returns the daemon's
// answer as it came, so an alias keeps the old result shape exactly.
func decodeAny(raw json.RawMessage) (any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return map[string]any{}, nil
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

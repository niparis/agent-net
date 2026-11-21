package protocol

// MethodUrgentTake is the local method a Claude Code hook uses, at a tool call
// inside a turn, to fetch the urgent messages held for its session (decision
// 0180). A command line client names the session as MethodWaiting does.
const MethodUrgentTake = "urgent_take"

// FrameUrgentHeld is the gateway telling a node that an urgent message is held
// for one of its busy sessions. It lets the node answer most hook calls
// without asking the gateway. Its params are UrgentHeld.
const FrameUrgentHeld = "node.urgent_held"

// OpUrgentTake is the gateway request behind MethodUrgentTake. Its params are
// UrgentTake and its result is UrgentTaken.
const OpUrgentTake = "msg.take_urgent"

// UrgentHeld is FrameUrgentHeld.
type UrgentHeld struct {
	SessionID string `json:"session_id"`
}

// UrgentTake is OpUrgentTake.
type UrgentTake struct {
	SessionID string `json:"session_id"`
}

// UrgentTaken is the result of OpUrgentTake: the urgent messages the gateway
// handed over, oldest first, with the delivery fields an adapter shows.
type UrgentTaken struct {
	Messages []Message `json:"messages"`
}

// UrgentTakeRequest is MethodUrgentTake.
type UrgentTakeRequest struct {
	Harness  string `json:"harness"`
	NativeID string `json:"native_id"`
	// DeadlineMS is how long the caller waits for the answer. The daemon never
	// hands a message over after it, so a caller that gave up has not left a
	// message marked delivered that nobody saw. 0 means the daemon's own limit.
	DeadlineMS int `json:"deadline_ms,omitempty"`
}

// UrgentTakeResult is the answer to MethodUrgentTake. A Claude Code hook gets
// Text, ready to show the model. A plugin (Pi, OpenCode) gets the Messages and
// formats them as it formats a pushed delivery. Both are empty when nothing
// urgent was waiting.
type UrgentTakeResult struct {
	Text     string    `json:"text,omitempty"`
	Messages []Message `json:"messages,omitempty"`
}

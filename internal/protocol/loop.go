package protocol

import (
	"strings"
	"time"
)

// Standing loops (decision 0094). A node with loop_every set wakes its lead
// agent on a clock and asks it to keep working toward the node's purpose.

// LoopDailyCap is how many wakes of one session a day the loop and the idle
// check's autonomy wake share.
const LoopDailyCap = 6

// LoopNoProgressStop is how many ticks in a row may pass with no progress
// before the loop pauses.
const LoopNoProgressStop = 2

// Outcomes of one tick, as the Desk log words them.
const (
	LoopSent         = "sent"
	LoopSkippedBusy  = "skipped busy"
	LoopWoke         = "woke"
	LoopPaused       = "paused"
	LoopSkippedNone  = "skipped, no session"
	LoopSkippedQuiet = "skipped, nothing new"
	LoopSkippedWake  = "skipped, cannot be woken"
)

// Values of loop_mode.
const (
	LoopModeAlways   = "always"
	LoopModeOnChange = "on_change"
)

// Answers to the Desk ask that a paused loop opens.
const (
	LoopAnswerResume = "resume"
	LoopAnswerStop   = "stop"
)

// FrameLoopResume is the gateway frame behind the owner operation loop_resume.
// It restarts a paused loop on the owner's quoted words (decision 0196).
const FrameLoopResume = "loop.resume"

// LoopResumeRequest is cto_op loop_resume. Node is the loop's id.
type LoopResumeRequest struct {
	Node       string `json:"node" jsonschema:"the loop id, which is the node name"`
	OwnerWords string `json:"owner_words" jsonschema:"a short quote of the owner's instruction in this chat"`
}

// LoopResumeFrame carries the daemon's checked owner words to the gateway.
type LoopResumeFrame struct {
	Node string `json:"node"`
	// Owner and By name the owner session that was checked and who acted.
	Owner      string `json:"owner"`
	By         string `json:"by,omitempty"`
	OwnerWords string `json:"owner_words"`
	// WordsVerified is set by the daemon when the quote is the owner's own.
	WordsVerified bool `json:"words_verified,omitempty"`
}

// LoopResumeResult answers loop_resume.
type LoopResumeResult struct {
	Node   string `json:"node"`
	Status string `json:"status"`
}

// LoopView is what the Graph card, the CTO's graph read and the session views
// show of a node's loop. LastTick and NextTick are Unix seconds; 0 means never
// or not scheduled. Tokens is 0 when agent-net does not know.
type LoopView struct {
	Every         string `json:"every"`
	Mode          string `json:"mode,omitempty"`
	Plan          string `json:"plan,omitempty"`
	LastTick      int64  `json:"last_tick,omitempty"`
	NextTick      int64  `json:"next_tick,omitempty"`
	TicksToday    int    `json:"ticks_today"`
	ProgressToday int    `json:"progress_today"`
	Tokens        int64  `json:"tokens_today,omitempty"`
	Paused        bool   `json:"paused,omitempty"`
	LastResult    string `json:"last_result,omitempty"`
}

// LoopEvery reads a node's loop interval. An empty, invalid or non-positive
// value is no loop.
func LoopEvery(settings map[string]string) time.Duration {
	v := strings.TrimSpace(settings[SettingLoopEvery])
	if v == "" || ValidateNodeSetting(SettingLoopEvery, v) != nil {
		return 0
	}
	d, _ := time.ParseDuration(v)
	return d
}

// LoopPrompt is what a tick's prompt is built from.
type LoopPrompt struct {
	// Changes is the "Since ..." line: what changed since the last tick.
	Changes string
	// Members is the "Members:" part: the lines about team members that are
	// blocked or waiting and the workers they started. Empty when there is
	// nothing to say.
	Members string
	Purpose string
	// Custom replaces the middle sentence when set.
	Custom string
	// Plan is a plan file path, or empty.
	Plan string
}

// LoopOwnerLine tells the agent not to wait for the owner.
const LoopOwnerLine = "If you need the owner, send one request to the chief of staff with agent_message_send, saying what you need, why only the owner can decide, and your recommendation; carry on with work that does not depend on the answer; do not wait."

// LoopMessage builds the tick's prompt. The changes line comes first, then
// the Members part when there is one, then the purpose, then the middle
// sentence (the default or the node's own), the plan sentence when a plan is
// set, and the owner line.
func LoopMessage(p LoopPrompt) string {
	purpose := strings.TrimRight(strings.TrimSpace(NeutralizeMarkers(p.Purpose)), ". ")
	if purpose == "" {
		purpose = "to keep your announced task moving"
	}
	middle := strings.TrimSpace(NeutralizeMarkers(p.Custom))
	if middle == "" {
		middle = "Check where you stand against it, do the next step, then announce what you did or what blocks you."
	}
	out := "[agent-net] Loop: "
	if c := strings.TrimSpace(p.Changes); c != "" {
		out += strings.TrimSuffix(c, ".") + ". "
	}
	if m := strings.TrimSpace(NeutralizeMarkers(p.Members)); m != "" {
		out += "Members:\n" + m + "\n"
	}
	out += "Your purpose is " + purpose + ". " + middle
	if plan := strings.TrimSpace(NeutralizeMarkers(p.Plan)); plan != "" {
		out += " Read " + plan + ", do the next step, then update " + plan + "."
	}
	return out + " " + LoopOwnerLine
}

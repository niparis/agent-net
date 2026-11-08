package protocol

import (
	"bytes"
	"encoding/json"
	"strings"
	"unicode"

	"github.com/niparis/agent-net/internal/redact"
)

// "Needs you" (decision 0016). A session says it is waiting for its user: a
// permission prompt, a question, or plain input. The daemon keeps the state
// with the session and sends it to the gateway like the status. It is cleared
// when the session works again, or when the user prompts it.
const (
	WaitingPermission = "permission"
	WaitingQuestion   = "question"
	WaitingInput      = "input"
)

// StatusWaitingForUser is the status agent_sessions_find and agent_session_get
// show for an online session that waits for its user. It is a read status
// only: a daemon never reports it, and the stored status stays as reported.
const StatusWaitingForUser = "waiting for its user"

// MaxWaitingTextLen is the longest waiting text the gateway keeps, in
// characters. The text is shown on one line.
const MaxWaitingTextLen = 200

// MethodWaiting is the local method a plugin or a harness hook uses to report
// or clear a session's waiting state. It changes nothing else, so it is not an
// owner-only method: like a hint, a hook may call it for its own session.
const MethodWaiting = "waiting"

// FrameWaitClear is the gateway telling a node that the owner closed a
// session's question or approval without a message (a dismissal): the
// session's explicit wait ends. Its params are WaitClear.
const FrameWaitClear = "node.wait_clear"

// WaitClear is FrameWaitClear.
type WaitClear struct {
	SessionID string `json:"session_id"`
}

// WaitingRequest is MethodWaiting. A plugin is bound to its session and leaves
// Harness and NativeID empty. A command line hook names the session it runs
// for, as it does for MethodHint.
type WaitingRequest struct {
	Explicit bool     `json:"explicit,omitempty"`
	Kind     string   `json:"kind,omitempty"`
	Text     string   `json:"text,omitempty"`
	Options  []string `json:"options,omitempty"`
	Urgency  string   `json:"urgency,omitempty"`
	Clear    bool     `json:"clear,omitempty"`
	// Harness and NativeID are only read from a command-line client.
	Harness  string `json:"harness,omitempty"`
	NativeID string `json:"native_id,omitempty"`
}

// Waiting is what a session asks its user. Since is the Unix time in seconds
// when the wait began.
type Waiting struct {
	Explicit bool `json:"explicit,omitempty"`
	// Blocking marks a wait that a harness event reported (a permission, a
	// form, a dialog): it blocks the running turn, so it ends with the turn.
	// An explicit ask and an announced wait outlive the turn.
	Blocking bool     `json:"blocking,omitempty"`
	Kind     string   `json:"kind"`
	Text     string   `json:"text"`
	Since    int64    `json:"since,omitempty"`
	Options  []string `json:"options,omitempty"`
	Urgency  string   `json:"urgency,omitempty"`
}

// ValidWaitingKind tells whether k is a kind a client may report.
func ValidWaitingKind(k string) bool {
	return k == WaitingPermission || k == WaitingQuestion || k == WaitingInput
}

// WaitingKindText is the word a notice to a peer uses for a wait: "approval"
// for a permission, "answer" for a question or input. The question itself is
// for the owner only.
func WaitingKindText(kind string) string {
	if kind == WaitingPermission {
		return "approval"
	}
	return "answer"
}

// CleanWaitingText drops control characters, makes one line of the text, and
// cuts it to MaxWaitingTextLen characters.
func CleanWaitingText(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			return ' '
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r):
			return -1
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > MaxWaitingTextLen {
		s = string(r[:MaxWaitingTextLen])
	}
	return s
}

// CleanWaiting returns the state to store, or nil when there is nothing to
// wait for. A text with no kind is an input question; an unknown kind is not
// accepted from a client and becomes input. Secrets in the text and the
// options are masked first: the text leaves the machine and goes to every TUI.
// A peer sees only the kind and the time, never the text.
func CleanWaiting(w *Waiting) *Waiting {
	if w == nil {
		return nil
	}
	kind := strings.ToLower(strings.TrimSpace(w.Kind))
	if !ValidWaitingKind(kind) {
		kind = WaitingInput
	}
	text := CleanWaitingText(redact.Secrets(w.Text))
	if text == "" && !ValidWaitingKind(strings.ToLower(strings.TrimSpace(w.Kind))) {
		return nil
	}
	var options []string
	for _, option := range w.Options {
		if len(options) == 4 {
			break
		}
		if option = CleanWaitingText(redact.Secrets(option)); option != "" {
			options = append(options, option)
		}
	}
	urgency := ""
	if w.Urgency == "high" {
		urgency = "high"
	}
	return &Waiting{Kind: kind, Text: text, Since: w.Since, Options: options, Urgency: urgency, Explicit: w.Explicit, Blocking: w.Blocking}
}

// UnmarshalJSON accepts the object form and the short string form that the
// agent_announce tool and the plugins send ("waiting": "the question").
func (w *Waiting) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*w = Waiting{Text: s}
		if CleanWaitingText(s) != "" {
			w.Kind = WaitingInput
		}
		return nil
	}
	type plain Waiting
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*w = Waiting(p)
	return nil
}

package protocol

import (
	"errors"
	"strings"
)

// Ask intake is the chief of staff's read of a session's last turn and its
// import as an owner ask (decision 0114, part 3). It reads a session's last
// assistant message and the owner's last typed prompt before it, with secrets
// masked, and it may create one ask from that turn. Nothing in it sends input
// to the session, answers an ask, or changes the session's state.
const (
	// OpAskIntake is the agent tool agent_ask_intake. FrameAskIntake carries it
	// from the daemon to the gateway.
	OpAskIntake    = "ask_intake"
	FrameAskIntake = "ask.intake"
	// FrameAskIntakeTurn is the gateway asking the daemon that holds a session
	// for its last turn.
	FrameAskIntakeTurn = "node.session_last_turn"
)

// The three actions of agent_ask_intake.
const (
	AskIntakeList   = "list"
	AskIntakeRead   = "read"
	AskIntakeImport = "import"
)

// The state of a last-turn read. "unavailable" means the harness has no
// reader, the transcript is missing, or the machine did not answer. The tool
// never fills the text by guessing.
const (
	AskIntakeOK          = "ok"
	AskIntakeUnavailable = "unavailable"
)

// The state of an import. "created" is a new ask. "already_imported" is the
// same turn imported before, even if that ask is closed. "existing_open" is an
// earlier open ask from the same session.
const (
	AskIntakeCreated          = "created"
	AskIntakeAlreadyImported  = "already_imported"
	AskIntakeExistingOpen     = "existing_open"
	AskIntakeSourceRefPrefix  = "ask-intake:"
	AskIntakeDefaultListLimit = 25
	AskIntakeMaxListLimit     = 50
)

// AskIntakeSourceRef builds the stable source key of one turn: the session id
// and the harness's own cursor, which is the transcript entry or message id.
func AskIntakeSourceRef(session, turn string) string {
	return AskIntakeSourceRefPrefix + session + ":" + turn
}

// AskIntakeArgs are the arguments of agent_ask_intake. Session is the target
// session for read and import. Turn, Text, Options and Recommendation are for
// import. Limit bounds the list.
type AskIntakeArgs struct {
	Action         string   `json:"action"`
	Session        string   `json:"session,omitempty"`
	Turn           string   `json:"turn,omitempty"`
	Text           string   `json:"text,omitempty"`
	Options        []string `json:"options,omitempty"`
	Recommendation string   `json:"recommendation,omitempty"`
	Limit          int      `json:"limit,omitempty"`
}

// Validate checks the action and its fields before the gateway reads anything.
func (a AskIntakeArgs) Validate() error {
	switch a.Action {
	case AskIntakeList:
		if a.Session != "" || a.Turn != "" || a.Text != "" || len(a.Options) > 0 || a.Recommendation != "" {
			return errors.New("list takes only limit")
		}
		if a.Limit < 0 || a.Limit > AskIntakeMaxListLimit {
			return errors.New("limit must be 1 to 50")
		}
	case AskIntakeRead:
		if strings.TrimSpace(a.Session) == "" {
			return errors.New("read needs session: the session id or exact name")
		}
		if a.Turn != "" || a.Text != "" || len(a.Options) > 0 || a.Recommendation != "" || a.Limit != 0 {
			return errors.New("read takes only session")
		}
	case AskIntakeImport:
		if strings.TrimSpace(a.Session) == "" {
			return errors.New("import needs session: the session id or exact name")
		}
		if strings.TrimSpace(a.Turn) == "" {
			return errors.New("import needs turn: the turn reference from list or read")
		}
		if strings.TrimSpace(a.Text) == "" {
			return errors.New("import needs text: the question in one line")
		}
		if strings.ContainsAny(a.Text, "\r\n") {
			return errors.New("import text must be one line")
		}
		if len(a.Options) > 4 {
			return errors.New("import takes at most four options")
		}
		if a.Limit != 0 {
			return errors.New("import takes no limit")
		}
	default:
		return errors.New("action must be list, read or import")
	}
	return nil
}

// AskIntakeRequest is the frame body: the chief's session and its arguments.
// The daemon fills SessionID; an agent never sends it.
type AskIntakeRequest struct {
	SessionID string `json:"session_id"`
	AskIntakeArgs
}

// AskIntakeWaiting is a session's current waiting state as the gateway stores
// it. Kind is "explicit" for an ask the session raised itself, "harness" for a
// wait the harness reported, and empty when there is none.
type AskIntakeWaiting struct {
	Kind  string `json:"kind,omitempty"`
	Text  string `json:"text,omitempty"`
	Since int64  `json:"since,omitempty"`
}

// AskIntakeSession is one row of the list. State "unavailable" carries Reason
// and no turn. OpenAsk is the id of an open ask from that session, if any.
type AskIntakeSession struct {
	Session    string            `json:"session"`
	Name       string            `json:"name,omitempty"`
	Harness    string            `json:"harness"`
	Machine    string            `json:"machine,omitempty"`
	State      string            `json:"state"`
	Reason     string            `json:"reason,omitempty"`
	LastTurn   string            `json:"last_turn,omitempty"`
	LastTurnAt string            `json:"last_turn_at,omitempty"`
	Waiting    *AskIntakeWaiting `json:"waiting,omitempty"`
	OpenAsk    string            `json:"open_ask,omitempty"`
}

// AskIntakeTurn is the exact last assistant turn of one session, with secrets
// masked. Prompt is the owner's last typed prompt before it, as context. At
// and PromptAt are RFC 3339 in UTC. Ref is stable for the same turn.
type AskIntakeTurn struct {
	Session  string `json:"session"`
	State    string `json:"state"`
	Reason   string `json:"reason,omitempty"`
	Ref      string `json:"ref,omitempty"`
	At       string `json:"at,omitempty"`
	Text     string `json:"text,omitempty"`
	Prompt   string `json:"prompt,omitempty"`
	PromptAt string `json:"prompt_at,omitempty"`
	Cut      bool   `json:"cut,omitempty"`
	Ended    bool   `json:"ended,omitempty"`
}

// AskIntakeAsk is the result of an import. Detail carries a short line about
// an existing open ask.
type AskIntakeAsk struct {
	ID     string `json:"id"`
	State  string `json:"state"`
	Next   string `json:"next,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// AskIntakeResult answers agent_ask_intake. List fills Sessions, read fills
// Turn, and import fills Ask.
type AskIntakeResult struct {
	Action   string             `json:"action"`
	State    string             `json:"state,omitempty"`
	Sessions []AskIntakeSession `json:"sessions,omitempty"`
	Turn     *AskIntakeTurn     `json:"turn,omitempty"`
	Ask      *AskIntakeAsk      `json:"ask,omitempty"`
}

// AskIntakeTurnRequest asks one machine's daemon for the last turn of a
// session it holds. The gateway sends it only after it checked the caller is
// the chief.
type AskIntakeTurnRequest struct {
	SessionID string `json:"session_id"`
}

// AskIntakeTurnAnswer is what the machine's daemon returns. State is "ok" or
// "unavailable"; Ref is the harness's stable cursor, not a source key.
type AskIntakeTurnAnswer struct {
	Session  string `json:"session"`
	Harness  string `json:"harness,omitempty"`
	State    string `json:"state"`
	Reason   string `json:"reason,omitempty"`
	Ref      string `json:"ref,omitempty"`
	At       string `json:"at,omitempty"`
	Text     string `json:"text,omitempty"`
	Prompt   string `json:"prompt,omitempty"`
	PromptAt string `json:"prompt_at,omitempty"`
	Cut      bool   `json:"cut,omitempty"`
	Ended    bool   `json:"ended,omitempty"`
}

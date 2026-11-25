package gateway

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// workerStartOwnerChoiceMigration adds the owner's choice to the start record
// (decision 0191): the model, provider, harness or effort the owner named for
// a worker, so every continue that names none keeps it. Appended at the end of
// the migration list and never edited once it ships.
const workerStartOwnerChoiceMigration = `SELECT 1 /* worker start owner choice column */;`

// ownerChoice is what the owner named for a worker at its start: in his own
// command, or in his words that the caller's daemon verified. Only the fields
// he named are set. A named model is kept as it was resolved for the run, with
// the harness it ran on.
type ownerChoice struct {
	Harness  string `json:"harness,omitempty"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	Effort   string `json:"effort,omitempty"`
	// NamedAt is when he named it, in Unix nanoseconds of the gateway's
	// clock. His newest word wins: a pause he set after it beats his choice.
	NamedAt int64 `json:"named_at,omitempty"`
}

func (c ownerChoice) empty() bool {
	return c.Harness == "" && c.Provider == "" && c.Model == "" && c.Effort == ""
}

// pausedSince tells whether the owner paused the held model after he named
// it. A pause he set before he named the model, or one with no time (set
// before decision 0191, so before any held choice), does not count: naming a
// paused model is his newer word, and it runs as in decision 0188.
func (c ownerChoice) pausedSince(scores []protocol.ModelScore) bool {
	if c.Model == "" {
		return false
	}
	at, paused := protocol.WorkerModelPausedAt(protocol.WorkerModelCandidate{Harness: c.Harness, Model: c.Model}, scores)
	return paused && at > c.NamedAt
}

// ownerSaid tells whether the start's selection fields are the owner's own:
// his command line or TUI, or his words found by the caller's daemon
// (owner_selected). The CTO's start without his words is not.
func ownerSaid(p protocol.WorkerStartFrame, owner protocol.AgentView) bool {
	return p.OwnerSelected || (protocol.IsOwner(owner.Harness) && !p.FromCTO)
}

// modelNamed tells whether a model field names a model: not empty, not auto.
func modelNamed(m string) bool {
	m = strings.TrimSpace(m)
	return m != "" && !strings.EqualFold(m, "auto")
}

// namedSelection tells whether a start request names any selection field.
func namedSelection(r protocol.WorkerStartRequest) bool {
	return modelNamed(r.Model) || strings.TrimSpace(r.Harness) != "" ||
		strings.TrimSpace(r.Provider) != "" || strings.TrimSpace(r.Effort) != ""
}

// ownerChoiceOf is what the owner named in a start at time at. asked is the
// request as the owner sent it; harness, provider, model and effort are what
// the start resolved and will run. A named model keeps the resolved model and
// the harness it runs on; otherwise only the fields he named are kept.
func ownerChoiceOf(asked protocol.WorkerStartRequest, said bool, at time.Time, harness, provider, model, effort string) ownerChoice {
	if !said || !namedSelection(asked) {
		return ownerChoice{}
	}
	if modelNamed(asked.Model) {
		return ownerChoice{Harness: harness, Provider: provider, Model: model, Effort: effort, NamedAt: at.UnixNano()}
	}
	return ownerChoice{Harness: strings.TrimSpace(asked.Harness), Provider: strings.TrimSpace(asked.Provider), Effort: strings.TrimSpace(asked.Effort), NamedAt: at.UnixNano()}
}

// setStartOwnerChoice stores the owner's choice on a tracked start.
func (s *Store) setStartOwnerChoice(ref string, c ownerChoice) error {
	if ref == "" || c.empty() {
		return nil
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE worker_starts SET owner_choice=? WHERE ref=?`, string(raw), ref)
	return err
}

// ownerChoiceForSession reads the owner's choice of the start that became a
// worker session. A session without one, or an older start, has none.
func (s *Store) ownerChoiceForSession(sessionID string) (ownerChoice, error) {
	var c ownerChoice
	if sessionID == "" {
		return c, nil
	}
	var raw string
	err := s.db.QueryRow(`SELECT owner_choice FROM worker_starts WHERE session_id=? AND owner_choice<>'' ORDER BY created_at DESC LIMIT 1`, sessionID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return ownerChoice{}, nil
	}
	return c, nil
}

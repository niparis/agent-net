package protocol

import (
	"errors"
	"strings"
	"time"
)

// Due items (agent_due) are steps a session owns that fall due later, at a
// time or after an event. The gateway checks them on its minute tick and wakes
// the session when one falls due (see gateway session_due.go).
const (
	OpDue    = "due" // takes DueArgs, answers DueResult
	FrameDue = "due" // daemon to gateway, takes DueRequest

	DueActionAdd  = "add"
	DueActionList = "list"
	DueActionDone = "done"

	// DueBlockerNone wakes the owning session when the item falls due. Owner
	// blocks raise one chief ask and no wake. Any other value is a session id:
	// the chief chases that session, and the owner session is not woken.
	DueBlockerNone  = "none"
	DueBlockerOwner = "owner"
)

// DueAfter names the event an item waits for. Exactly one field is set.
type DueAfter struct {
	Reply      string `json:"reply,omitempty"`
	ChangeStep string `json:"change_step,omitempty"`
	AskClosed  string `json:"ask_closed,omitempty"`
}

// DueArgs is agent_due. Add takes What, and either Due or After. Done takes ID.
// The catalog calls the item id "id", but a gateway frame owns that name, so
// the wire name is due_id (toolcatalog daemonFields maps it).
type DueArgs struct {
	Action  string    `json:"action"`
	What    string    `json:"what,omitempty"`
	Due     string    `json:"due,omitempty"`
	After   *DueAfter `json:"after,omitempty"`
	Blocker string    `json:"blocker,omitempty"`
	ID      string    `json:"due_id,omitempty"`
}

// Validate checks the shape of the call. The gateway checks that the named
// message, change, ask or session exists.
func (a DueArgs) Validate() error {
	switch a.Action {
	case DueActionList:
		return nil
	case DueActionDone:
		if a.ID == "" {
			return errors.New("id is required for action done")
		}
		return nil
	case DueActionAdd:
	default:
		return errors.New("action must be add, list or done")
	}
	if strings.TrimSpace(a.What) == "" {
		return errors.New("what is required for action add")
	}
	timed, evented := a.Due != "", a.After != nil
	if timed == evented {
		return errors.New("give exactly one of due or after")
	}
	if timed {
		if _, err := ParseFollowUp(a.Due, time.Now()); err != nil {
			return errors.New("due is an RFC 3339 time (2026-11-21T07:30:00Z) or a positive duration such as 45m")
		}
		return nil
	}
	n := 0
	for _, v := range []string{a.After.Reply, a.After.ChangeStep, a.After.AskClosed} {
		if v != "" {
			n++
		}
	}
	if n != 1 {
		return errors.New("after takes exactly one of reply (a message id), change_step (a change id) or ask_closed (an ask id)")
	}
	return nil
}

// DueRequest is FrameDue. The gateway checks that SessionID is on the machine
// that sent it.
type DueRequest struct {
	SessionID string `json:"session_id"`
	DueArgs
}

// DueItem is one due item as agent_due answers it.
type DueItem struct {
	ID        string    `json:"id"`
	What      string    `json:"what"`
	Status    string    `json:"status"`
	Blocker   string    `json:"blocker"`
	Due       string    `json:"due,omitempty"`
	After     *DueAfter `json:"after,omitempty"`
	WokenAt   string    `json:"woken_at,omitempty"`
	CreatedAt string    `json:"created_at"`
}

// DueResult lists the items a call touched: the new item, the open items of
// list, or the item marked done.
type DueResult struct {
	Items []DueItem `json:"items"`
}

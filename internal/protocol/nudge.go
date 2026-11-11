package protocol

import (
	"fmt"
	"time"
)

const (
	OpNudgePause         = "nudge_pause"
	OpNudgeResume        = "nudge_resume"
	OpNudgePausesFind    = "nudge_pauses_find"
	OpNudgeUndo          = "nudge_undo"
	FrameNudgePause      = "nudge.pause"
	FrameNudgeResume     = "nudge.resume"
	FrameNudgePausesFind = "nudge.pauses.find"
	FrameNudgeUndo       = "nudge.undo"
)

var NudgeKinds = []string{"idle_check", "autonomy_continue", "reply_reminder", "loop_tick", "step_reminder", "stuck_step", "undelivered", "waiting_notice", "wake_failed", "member_blocked"}

type NudgePauseArgs struct {
	Kind   string `json:"kind"`
	Team   string `json:"team,omitempty"`
	Agent  string `json:"agent,omitempty"`
	Until  string `json:"until,omitempty"`
	Reason string `json:"reason,omitempty"`
	Mode   string `json:"mode"`
}

func (a NudgePauseArgs) Validate() error {
	valid := false
	for _, k := range NudgeKinds {
		if a.Kind == k {
			valid = true
			break
		}
	}
	if !valid {
		return fmt.Errorf("unknown nudge kind %q", a.Kind)
	}
	if (a.Team == "") == (a.Agent == "") {
		return fmt.Errorf("exactly one of team or agent is required")
	}
	if a.Mode != "preview" && a.Mode != "apply" {
		return fmt.Errorf("mode must be preview or apply")
	}
	if a.Until != "" && a.Until != "provider_reset" {
		if _, err := ParseNudgeUntil(a.Until); err != nil {
			return fmt.Errorf("until must be an RFC3339 time or provider_reset")
		}
	}
	return nil
}

type NudgePauseRequest struct {
	SessionID string `json:"session_id"`
	NudgePauseArgs
}

// NudgeResumeArgs is the input of the chief's agent_nudge_resume tool. The
// wire field is pause_id: a frame already owns id.
type NudgeResumeArgs struct {
	ID string `json:"id"`
}
type NudgeResumeRequest struct {
	SessionID string `json:"session_id"`
	PauseID   string `json:"pause_id"`
}
type NudgePausesFindArgs struct{}
type NudgePausesFindRequest struct {
	SessionID string `json:"session_id"`
}
type NudgeSession struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type NudgePause struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Team      string `json:"team,omitempty"`
	Agent     string `json:"agent,omitempty"`
	Until     string `json:"until,omitempty"`
	Reason    string `json:"reason,omitempty"`
	CreatedAt int64  `json:"created_at"`
	EndedAt   int64  `json:"ended_at,omitempty"`
	EndedBy   string `json:"ended_by,omitempty"`
}
type NudgeResult struct {
	ID          string         `json:"id,omitempty"`
	Sessions    []NudgeSession `json:"sessions,omitempty"`
	Messages24h int            `json:"messages_24h"`
	Pauses      []NudgePause   `json:"pauses,omitempty"`
}

// NudgeUndoArgs is an owner-only Desk receipt, never an agent tool. Old rows
// store the pause as id; new rows store pause_id. Read both.
type NudgeUndoArgs struct {
	ID      string `json:"id,omitempty"`
	PauseID string `json:"pause_id,omitempty"`
	Receipt string `json:"receipt"`
}

// Pause returns the pause id from the old or the new field.
func (a NudgeUndoArgs) Pause() string {
	if a.PauseID != "" {
		return a.PauseID
	}
	return a.ID
}

type NudgeUndoRequest struct {
	PauseID string `json:"pause_id"`
	Receipt string `json:"receipt"`
	Owner   string `json:"owner"`
}

// ParseNudgeUntil also accepts a minute-resolution RFC3339 timestamp.
func ParseNudgeUntil(value string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02T15:04Z07:00", value)
}

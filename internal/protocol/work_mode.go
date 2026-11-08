package protocol

import (
	"fmt"
)

// Work mode (decision 0094). It says who writes code in a session. It is a
// charter attribute, so it inherits global -> team -> repo -> agent and the
// most specific level wins. Unset means on_request.
const (
	// AttrWorkMode is the charter attribute name; SettingWorkMode is the same
	// name as a node_settings_set setting.
	AttrWorkMode    = "work_mode"
	SettingWorkMode = "work_mode"
	// WorkModeOnRequest is the default: a session hands a task to a worker only
	// when the owner asks.
	WorkModeOnRequest = "on_request"
	// WorkModeWorkersCode means the session designs, architects and reviews, and
	// agent-net workers write all code, with no per-task owner ask.
	WorkModeWorkersCode = "workers_code"
)

// ValidateWorkMode checks a work mode value. An empty value clears the level.
func ValidateWorkMode(value string) error {
	switch value {
	case "", WorkModeOnRequest, WorkModeWorkersCode:
		return nil
	}
	return fmt.Errorf("work_mode must be %s or %s", WorkModeWorkersCode, WorkModeOnRequest)
}

// WorkModeSourceText names the level a work mode came from: "team forge".
func WorkModeSourceText(src CharterSource) string {
	if src.Level == "global" || src.Scope == "" {
		return src.Level
	}
	return src.Level + " " + src.Scope
}

// WorkModeLine is the one line a session reads each turn when its effective
// work mode is not the default. It is empty for on_request and for an unset
// or invalid value, so a normal prompt costs no extra tokens.
func WorkModeLine(c Charter) string {
	if c.WorkMode != WorkModeWorkersCode {
		return ""
	}
	where := ""
	if c.Sources != nil {
		if list := (*c.Sources)["work_mode"]; len(list) > 0 {
			where = " (" + WorkModeSourceText(list[len(list)-1]) + ")"
		}
	}
	return "Work mode: workers write all code" + where + ". Design and review; start agent-net workers for code; no owner ask needed."
}

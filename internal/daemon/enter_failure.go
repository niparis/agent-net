package daemon

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/niparis/agent-net/internal/protocol"
)

// A session Enter is planned by the daemon and run by the TUI in the owner's
// terminal. When the command ends with an error, only the TUI knows why. These
// two paths put it in the daemon log: the plan's own error, and a report from
// the TUI with the command's last output. The lead reads them with
// agent_machine_log.

// logEnterFailure writes one line for a session Enter that failed.
func (d *Daemon) logEnterFailure(e protocol.EnterFailure) {
	if d.log == nil {
		return
	}
	line := fmt.Sprintf("event=enter_failed session=%s err=%q",
		logToken(logText(e.SessionID, protocol.EnterFailureMaxText)), logText(e.Err, protocol.EnterFailureMaxText))
	if e.Host != "" {
		line += " host=" + logToken(logText(e.Host, protocol.EnterFailureMaxText))
	}
	if e.Output != "" {
		line += fmt.Sprintf(" output=%q", logText(e.Output, protocol.EnterFailureMaxText))
	}
	d.log.Printf("%s", line)
}

// enterFailure takes a report from the TUI: a session Enter it ran failed.
func (lc *localConn) enterFailure(client string, raw json.RawMessage) (any, error) {
	if client != "cli" {
		return nil, errors.New("enter_failure needs a cli client")
	}
	var e protocol.EnterFailure
	if json.Unmarshal(raw, &e) != nil || e.SessionID == "" {
		return nil, errors.New("bad enter_failure params")
	}
	lc.d.logEnterFailure(e)
	return map[string]bool{"ok": true}, nil
}

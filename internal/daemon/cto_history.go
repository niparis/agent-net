package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

const ctoHistoryLimit = 500

type ctoHistoryEntry struct {
	at      int64
	message json.RawMessage
	action  protocol.CTOAction
	isMsg   bool
}

// snapshotCTOHarness saves the conversation a harness holds before it is left
// or ended (a switch, a fallback, cto_new), so a later events request shows it
// without starting the harness again. A Codex or OpenCode harness that runs is
// read with no lock held; one that is down is read the way a timeline read
// does. A harness with no conversation has nothing to save.
func (d *Daemon) snapshotCTOHarness(ctx context.Context, harness string) error {
	conversation, err := d.ctoConversation(harness)
	if err != nil || conversation == "" {
		return err
	}
	var events protocol.CTOEvents
	switch harness {
	case "claude":
		events, err = (claudeCTOHarness{}).events(ctx, d, 0)
	case "codex":
		if events, err = d.codexCTORunningEvents(ctx); errors.Is(err, errCTONotRunning) {
			events, err = d.codexCTOEvents(ctx, 0)
		}
	default:
		if events, err = d.openCodeCTORunningEvents(ctx); errors.Is(err, errCTONotRunning) {
			events, err = d.openCodeCTOEvents(ctx, 0)
		}
	}
	if err != nil {
		return err
	}
	return d.saveCTOHistory(harness, events)
}

// saveCTOHistory replaces the saved copy of the harness's current
// conversation in one transaction, so a failed save leaves the earlier one as
// it was. The rows of the harness's earlier conversations stay: they are the
// only copy once a new conversation has replaced them.
func (d *Daemon) saveCTOHistory(harness string, events protocol.CTOEvents) error {
	events.Messages = dropHandoverExchange(events.Messages)
	sessionID, err := d.ctoConversation(harness)
	if err != nil {
		return err
	}
	tx, err := d.st.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`DELETE FROM cto_history WHERE harness=? AND session_id=? AND kind IN ('message','action')`, harness, sessionID); err != nil {
		return err
	}
	for _, message := range events.Messages {
		if _, err = tx.Exec(`INSERT INTO cto_history(harness,session_id,kind,at,payload) VALUES(?,?,'message',?,?)`, harness, sessionID, ctoMessageAt(message), string(message)); err != nil {
			return err
		}
	}
	for _, action := range events.Actions {
		payload, e := json.Marshal(action)
		if e != nil {
			return e
		}
		if _, err = tx.Exec(`INSERT INTO cto_history(harness,session_id,kind,at,payload) VALUES(?,?,'action',?,?)`, harness, sessionID, action.At, string(payload)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func ctoMessageAt(raw json.RawMessage) int64 {
	var message struct {
		Time struct {
			Created int64 `json:"created"`
		} `json:"time"`
	}
	if json.Unmarshal(raw, &message) != nil {
		return 0
	}
	return message.Time.Created * int64(time.Millisecond)
}

// mergeCTOHistory joins the live conversation of the active harness with the
// saved rows and the switch dividers. The saved copy of the conversation read
// live is left out; the saved rows of every other conversation, of any
// harness, are shown. With no active harness every saved row is shown.
func (d *Daemon) mergeCTOHistory(out protocol.CTOEvents, active string, index int) (protocol.CTOEvents, error) {
	if index == -int(^uint(0)>>1)-1 {
		return out, errors.New("invalid CTO history index")
	}
	live := ""
	if active != "" {
		var err error
		if live, err = d.ctoConversation(active); err != nil {
			return out, err
		}
	}
	entries := make([]ctoHistoryEntry, 0, len(out.Messages)+len(out.Actions))
	for _, message := range out.Messages {
		entries = append(entries, ctoHistoryEntry{at: ctoMessageAt(message), message: message, isMsg: true})
	}
	for _, action := range out.Actions {
		entries = append(entries, ctoHistoryEntry{at: action.At, action: action})
	}
	rows, err := d.st.db.Query(`SELECT id,harness,kind,at,payload FROM cto_history WHERE (kind IN ('message','action') AND NOT (harness=? AND session_id=?)) OR kind='switch' ORDER BY id`, active, live)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var id, at int64
		var harness, kind, payload string
		if err = rows.Scan(&id, &harness, &kind, &at, &payload); err != nil {
			rows.Close()
			return out, err
		}
		switch kind {
		case "message":
			entries = append(entries, ctoHistoryEntry{at: at, message: json.RawMessage(payload), isMsg: true})
		case "action":
			var action protocol.CTOAction
			if err = json.Unmarshal([]byte(payload), &action); err != nil {
				rows.Close()
				return out, err
			}
			entries = append(entries, ctoHistoryEntry{at: at, action: action})
		case "switch":
			var model string
			if err = json.Unmarshal([]byte(payload), &model); err != nil {
				rows.Close()
				return out, err
			}
			text := fmt.Sprintf("switched to %s · %s · %s", harness, model, time.Unix(0, at).Local().Format("2006-01-02 15:04"))
			entries = append(entries, ctoHistoryEntry{at: at, action: protocol.CTOAction{ID: id, Text: text, At: at}})
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	rows.Close()

	sort.SliceStable(entries, func(i, j int) bool { return entries[i].at < entries[j].at })
	start, end := 0, len(entries)
	switch {
	case index < 0:
		end = min(-index, len(entries))
		start = max(end-ctoHistoryLimit, 0)
	case index == 0:
		start = max(len(entries)-ctoHistoryLimit, 0)
	case index > 0:
		start = min(index, len(entries))
		end = min(start+ctoHistoryLimit, len(entries))
	}
	out.Messages = []json.RawMessage{}
	out.Actions = []protocol.CTOAction{}
	out.Next = len(entries)
	out.Older = start
	for i := start; i < end; i++ {
		entry := entries[i]
		if entry.isMsg {
			out.Messages = append(out.Messages, entry.message)
		} else {
			out.Actions = append(out.Actions, entry.action)
		}
	}
	return out, nil
}

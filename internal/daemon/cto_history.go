package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
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

// snapshotCTOHarness stores the session that is being left so a later events
// request can read the full timeline without starting an inactive harness.
func (d *Daemon) snapshotCTOHarness(ctx context.Context, harness string) error {
	var events protocol.CTOEvents
	var err error
	if harness == "claude" {
		events, err = (claudeCTOHarness{}).events(ctx, d, 0)
	} else {
		events, err = d.openCodeCTOEvents(ctx, 0)
	}
	if err != nil {
		return err
	}
	var sessionID string
	if harness == "claude" {
		err = d.st.db.QueryRow(`SELECT claude_session_id FROM cto WHERE id=1`).Scan(&sessionID)
	} else {
		err = d.st.db.QueryRow(`SELECT session_id FROM cto WHERE id=1`).Scan(&sessionID)
	}
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	tx, err := d.st.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`DELETE FROM cto_history WHERE harness=? AND kind IN ('message','action')`, harness); err != nil {
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

func (d *Daemon) mergeCTOHistory(out protocol.CTOEvents, active string, index int) (protocol.CTOEvents, error) {
	entries := make([]ctoHistoryEntry, 0, len(out.Messages)+len(out.Actions))
	for _, message := range out.Messages {
		entries = append(entries, ctoHistoryEntry{at: ctoMessageAt(message), message: message, isMsg: true})
	}
	for _, action := range out.Actions {
		entries = append(entries, ctoHistoryEntry{at: action.At, action: action})
	}
	rows, err := d.st.db.Query(`SELECT id,harness,kind,at,payload FROM cto_history WHERE (harness<>? AND kind IN ('message','action')) OR kind='switch' ORDER BY id`, active)
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
	if len(entries) > ctoHistoryLimit {
		entries = entries[len(entries)-ctoHistoryLimit:]
	}
	if index < 0 {
		index = 0
	}
	out.Messages = []json.RawMessage{}
	out.Actions = []protocol.CTOAction{}
	out.Next = len(entries)
	for i := index; i < len(entries); i++ {
		entry := entries[i]
		if entry.isMsg {
			out.Messages = append(out.Messages, entry.message)
		} else {
			out.Actions = append(out.Actions, entry.action)
		}
	}
	return out, nil
}

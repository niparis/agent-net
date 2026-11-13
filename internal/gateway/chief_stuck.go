package gateway

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

// Claim age (decision 0150). A claimed ask (state verifying: with the chief, or
// with a session the chief chose) stops every clock. One that has not moved for
// the stuck-work limit pings the chief and shows in the morning brief as "still
// open, N hours"; it never goes to the owner. An item with the CTO (with_cto)
// that has not moved for the same limit goes back to the chief, because the CTO
// acts only in owner conversations. A routed ask whose holder session has been
// offline for the same limit goes back to the chief too, or to the session
// that replaced the holder. Each return keeps a one-line note (back_note) that
// the chief reads in its batch. The limit is the stuck-step setting
// (delivery.step_stuck, 12 hours), not a new number.

// chiefStuckPingPrefix keys the owner_settings rows that remember how many
// limits an ask has already been pinged for, so a tick pings once per limit.
const chiefStuckPingPrefix = "chief_stuck_ping:"

// chiefStuckLimit is the stuck-work limit.
func (s *Store) chiefStuckLimit() time.Duration {
	limit := 12 * time.Hour
	if snap, err := s.SettingsSnapshot(); err == nil {
		limit = snap.Duration("delivery.step_stuck", settings.Where{})
	}
	return limit
}

// stuckItem is an open Desk item that has not moved for the stuck-work limit.
type stuckItem struct {
	id, text, aboutKind, aboutID, state, next string
	idle                                      time.Duration
}

// stuckItems lists the open items in one Desk state whose last move is older
// than limit. The last move is the newest of its creation, the time it started
// waiting for the chief and its newest sort.
func (s *Store) stuckItems(now time.Time, limit time.Duration, state string) ([]stuckItem, error) {
	rows, err := s.db.Query(`SELECT id,text,about_kind,about_id,state,next_who,
		MAX(created_at, chief_at, COALESCE((SELECT MAX(at) FROM ask_sorts WHERE ask_id=inbox_items.id),0)) AS moved
		FROM inbox_items WHERE status='open' AND state=? AND moved<=? ORDER BY moved,id`, state, now.Add(-limit).UnixNano())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []stuckItem
	for rows.Next() {
		var it stuckItem
		var moved int64
		if err := rows.Scan(&it.id, &it.text, &it.aboutKind, &it.aboutID, &it.state, &it.next, &moved); err != nil {
			return nil, err
		}
		it.idle = now.Sub(time.Unix(0, moved))
		out = append(out, it)
	}
	return out, rows.Err()
}

// stillOpenLine is how a quiet claimed ask reads in a ping and in the brief.
func stillOpenLine(it stuckItem) string {
	hours := int(it.idle / time.Hour)
	unit := "hours"
	if hours == 1 {
		unit = "hour"
	}
	return fmt.Sprintf("%s still open, %d %s: %s", it.id, hours, unit, askFirstLine(it.text, 100))
}

// stillOpenText lists the claimed asks that went quiet, one line each, or "".
func (s *Store) stillOpenText(now time.Time) (string, error) {
	items, err := s.stuckItems(now, s.chiefStuckLimit(), protocol.DeskVerifying)
	if err != nil || len(items) == 0 {
		return "", err
	}
	lines := make([]string, len(items))
	for i, it := range items {
		lines[i] = stillOpenLine(it)
	}
	return strings.Join(lines, "\n"), nil
}

// chiefStuckWork runs the claim-age rules. It sends an idle item with the CTO
// back to the chief and returns the one message that pings the chief about the
// claimed asks that went quiet. The gateway dispatches the message.
func (s *Store) chiefStuckWork(now time.Time, limit time.Duration) ([]protocol.Message, error) {
	if limit <= 0 {
		return nil, nil
	}
	h, err := s.availableChief(s.db, now)
	if err != nil {
		return nil, err
	}
	if err := s.returnIdleCTOItems(now, limit); err != nil {
		return nil, err
	}
	moved, err := s.returnOfflineHolderAsks(now, limit)
	if err != nil {
		return nil, err
	}
	claimed, err := s.stuckItems(now, limit, protocol.DeskVerifying)
	if err != nil {
		return nil, err
	}
	// Drop the memory of asks that are no longer claimed and quiet.
	keep := map[string]bool{}
	for _, it := range claimed {
		keep[chiefStuckPingPrefix+it.id] = true
	}
	rows, err := s.db.Query(`SELECT key,value FROM owner_settings WHERE key LIKE ?`, chiefStuckPingPrefix+"%")
	if err != nil {
		return nil, err
	}
	pinged := map[string]int{}
	var gone []string
	for rows.Next() {
		var k, v string
		var n int
		if err := rows.Scan(&k, &v); err != nil {
			rows.Close()
			return nil, err
		}
		fmt.Sscan(v, &n)
		pinged[k] = n
		if !keep[k] {
			gone = append(gone, k)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, k := range gone {
		if _, err := s.db.Exec(`DELETE FROM owner_settings WHERE key=?`, k); err != nil {
			return nil, err
		}
	}
	// A chief that cannot work is covered by the safety net, not by pings.
	if h.id == "" {
		return moved, nil
	}
	var lines []string
	periods := map[string]int{}
	for _, it := range claimed {
		n := int(it.idle / limit)
		if n <= pinged[chiefStuckPingPrefix+it.id] {
			continue
		}
		periods[chiefStuckPingPrefix+it.id] = n
		lines = append(lines, stillOpenLine(it))
	}
	if len(lines) == 0 {
		return moved, nil
	}
	body := fmt.Sprintf("Chief of staff: %d ask(s) you claimed have not moved for %s or more. Carry each one out, sort it, or say why it waits.\n%s",
		len(lines), askAge(now.Add(limit), now), strings.Join(lines, "\n"))
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	m, err := s.storeAgentNetMessage(tx, h.id, body, "", now)
	if err != nil {
		return nil, err
	}
	for k, n := range periods {
		if err := s.setOwnerSetting(tx, k, fmt.Sprint(n), now); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return append(moved, m), nil
}

// returnIdleCTOItems gives the items that sat with the CTO for the limit back
// to the chief, as new asks: its clock and its batch start again. With no chief
// registered there is nobody to give them to.
func (s *Store) returnIdleCTOItems(now time.Time, limit time.Duration) error {
	chief, err := chiefSession(s.db)
	if err != nil || chief == "" {
		return err
	}
	items, err := s.stuckItems(now, limit, protocol.DeskWithCTO)
	if err != nil {
		return err
	}
	for _, it := range items {
		note := fmt.Sprintf("back from %s: no move for %d h", sessionLabel(s.db, it.next), int(it.idle/time.Hour))
		if err := s.giveBackToChief(it, protocol.DeskWithCTO, note, now); err != nil {
			return err
		}
	}
	return nil
}

// giveBackToChief puts one routed ask back in the chief's batch with the note
// that says why, and logs one Desk line. An ask that moved since it was read
// (state differs) is left alone.
func (s *Store) giveBackToChief(it stuckItem, state, note string, now time.Time) error {
	res, err := s.db.Exec(`UPDATE inbox_items SET next_who=?, chief_at=?, chief_sent_at=0, back_note=? WHERE id=? AND state=? AND next_who=?`,
		protocol.AskNextChief, now.UnixNano(), note, it.id, state, it.next)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil
	}
	text := fmt.Sprintf("Ask %s is back with the chief, %s: %s", it.id, note, askFirstLine(it.text, 120))
	return s.deskLog(s.db, "gateway", it.aboutKind, it.aboutID, text, "", nil)
}

// offlineHolder is a routed ask whose holder session is offline.
type offlineHolder struct {
	stuckItem
	offline time.Duration
}

// returnOfflineHolderAsks moves the routed asks whose holder has been offline
// for the limit, and that have not moved for the limit either. An ask goes to
// the session that replaced its holder when that session is not itself offline
// past the limit; any other goes back to the chief. It returns the messages to
// the replacements; the gateway dispatches them.
func (s *Store) returnOfflineHolderAsks(now time.Time, limit time.Duration) ([]protocol.Message, error) {
	chief, err := chiefSession(s.db)
	if err != nil || chief == "" {
		return nil, err
	}
	items, err := s.stuckItems(now, limit, protocol.DeskVerifying)
	if err != nil {
		return nil, err
	}
	cutoff := now.Add(-limit).UnixNano()
	var held []offlineHolder
	for _, it := range items {
		if it.next == protocol.AskNextChief || it.next == chief {
			continue
		}
		var lastSeen int64
		err := s.db.QueryRow(`SELECT last_seen FROM sessions WHERE id=? AND (online=0 OR archived_at<>0) AND last_seen<=?`, it.next, cutoff).Scan(&lastSeen)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		held = append(held, offlineHolder{it, now.Sub(time.Unix(0, lastSeen))})
	}
	var out []protocol.Message
	for _, h := range held {
		note := fmt.Sprintf("back from %s: offline for %d h", sessionLabel(s.db, h.next), int(h.offline/time.Hour))
		to, err := s.liveReplacement(h.next, chief, cutoff)
		if err != nil {
			return nil, err
		}
		if to == "" {
			if err := s.giveBackToChief(h.stuckItem, protocol.DeskVerifying, note, now); err != nil {
				return nil, err
			}
			continue
		}
		m, ok, err := s.giveToReplacement(h.stuckItem, to, note, now)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, m)
		}
	}
	return out, nil
}

// liveReplacement is the session that replaced holder, when there is one that
// is not archived, is not the chief and has not been offline past cutoff. It
// is "" otherwise.
func (s *Store) liveReplacement(holder, chief string, cutoff int64) (string, error) {
	live, ok, err := liveSession(s.db, holder)
	if err != nil || !ok || live == holder || live == chief {
		return "", err
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id=? AND archived_at=0 AND (online=1 OR last_seen>?)`, live, cutoff).Scan(&n); err != nil {
		return "", err
	}
	if n == 0 {
		return "", nil
	}
	return live, nil
}

// giveToReplacement moves one routed ask to the session that replaced its
// holder, tells that session why, and logs one Desk line.
func (s *Store) giveToReplacement(it stuckItem, to, note string, now time.Time) (protocol.Message, bool, error) {
	var none protocol.Message
	tx, err := s.db.Begin()
	if err != nil {
		return none, false, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE inbox_items SET next_who=?, back_note=? WHERE id=? AND state=? AND next_who=?`,
		to, note, it.id, protocol.DeskVerifying, it.next)
	if err != nil {
		return none, false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return none, false, nil
	}
	var asker string
	if err := tx.QueryRow(`SELECT session_id FROM inbox_items WHERE id=?`, it.id).Scan(&asker); err != nil {
		return none, false, err
	}
	body := fmt.Sprintf("agent-net gave you ask %s from %s: %s\nWhy it reached you: it was %s, and you replaced that session.\nAnswer it yourself only if you can, and close it with agent_ask_sort (result close) once it is resolved. Anything that needs the owner goes to them with agent_owner_ask.",
		it.id, sessionLabel(tx, asker), strings.TrimSpace(it.text), note)
	m, err := s.storeAgentNetMessage(tx, to, body, it.id, now)
	if err != nil {
		return none, false, err
	}
	text := fmt.Sprintf("Ask %s went to %s, %s: %s", it.id, sessionLabel(tx, to), note, askFirstLine(it.text, 120))
	if err := s.deskLog(tx, "gateway", it.aboutKind, it.aboutID, text, "", nil); err != nil {
		return none, false, err
	}
	return m, true, tx.Commit()
}

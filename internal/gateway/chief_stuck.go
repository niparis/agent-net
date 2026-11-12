package gateway

import (
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
// acts only in owner conversations. The limit is the stuck-step setting
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
	id, text, aboutKind, aboutID, state string
	idle                                time.Duration
}

// stuckItems lists the open items in one Desk state whose last move is older
// than limit. The last move is the newest of its creation, the time it started
// waiting for the chief and its newest sort.
func (s *Store) stuckItems(now time.Time, limit time.Duration, state string) ([]stuckItem, error) {
	rows, err := s.db.Query(`SELECT id,text,about_kind,about_id,state,
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
		if err := rows.Scan(&it.id, &it.text, &it.aboutKind, &it.aboutID, &it.state, &moved); err != nil {
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
		return nil, nil
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
		return nil, nil
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
	return []protocol.Message{m}, nil
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
		res, err := s.db.Exec(`UPDATE inbox_items SET next_who=?, chief_at=?, chief_sent_at=0 WHERE id=? AND state=?`,
			protocol.AskNextChief, now.UnixNano(), it.id, protocol.DeskWithCTO)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			continue
		}
		why := fmt.Sprintf("The CTO did not act on it in %d hours, so it is back with the chief: ", int(it.idle/time.Hour))
		if err := s.deskLog(s.db, "gateway", it.aboutKind, it.aboutID, why+it.text, "", nil); err != nil {
			return err
		}
	}
	return nil
}

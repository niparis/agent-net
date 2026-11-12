package gateway

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

// A lead-level Agent may start a request with the CTO. The level is explicit on
// the Agent, or comes from leading a Team or being the chief of staff. It is
// never inferred from Team membership alone.
const (
	ctoRateHoldKind  = "cto_rate"
	ctoRateHourKey   = "cto.requests_per_hour"
	ctoRateDayKey    = "cto.requests_per_day"
	ctoRateHoldLabel = "held: over %d per %s; it goes at %s"
)

// ctoLeadLevel reads the sending session's Agent and says whether that Agent
// has lead level. It returns the Agent ID for the rate limit.
func ctoLeadLevel(q queryer, session string) (string, bool, error) {
	var agent string
	err := q.QueryRow(`SELECT COALESCE(agent_id,'') FROM sessions WHERE id=?`, session).Scan(&agent)
	if err != nil {
		return "", false, err
	}
	if agent == "" {
		return "", false, nil
	}
	var explicit int
	err = q.QueryRow(`SELECT lead_level FROM agents WHERE id=?`, agent).Scan(&explicit)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", false, err
	}
	if err == nil && explicit != 0 {
		return agent, true, nil
	}
	var chief string
	err = q.QueryRow(`SELECT COALESCE(s.agent_id,'') FROM owner_settings o JOIN sessions s ON s.id=o.value WHERE o.key=?`, chiefSessionKey).Scan(&chief)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", false, err
	}
	if err == nil && chief != "" && chief == agent {
		return agent, true, nil
	}
	var teamLead bool
	if err := q.QueryRow(`SELECT EXISTS(SELECT 1 FROM teams WHERE owner_agent_id=? AND active=1)`, agent).Scan(&teamLead); err != nil {
		return "", false, err
	}
	return agent, teamLead, nil
}

// ctoRequestHold returns when a new request from agent may go out, the limit
// that binds it and that window's name. A zero time means the request may go
// now. exclude leaves one held message out of its own count.
func ctoRequestHold(tx *sql.Tx, agent, cto string, now time.Time, exclude string) (time.Time, int, string, error) {
	snap, err := settingsSnapshotTx(tx)
	if err != nil {
		return time.Time{}, 0, "", err
	}
	hourLimit := snap.Int(ctoRateHourKey, settings.Where{})
	dayLimit := snap.Int(ctoRateDayKey, settings.Where{})
	hourAt, err := ctoRateWait(tx, agent, cto, now, time.Hour, hourLimit, exclude)
	if err != nil {
		return time.Time{}, 0, "", err
	}
	dayAt, err := ctoRateWait(tx, agent, cto, now, 24*time.Hour, dayLimit, exclude)
	if err != nil {
		return time.Time{}, 0, "", err
	}
	holdUntil, limit, label := time.Time{}, 0, ""
	if !hourAt.IsZero() {
		holdUntil, limit, label = hourAt, hourLimit, "hour"
	}
	if !dayAt.IsZero() && (holdUntil.IsZero() || dayAt.After(holdUntil)) {
		holdUntil, limit, label = dayAt, dayLimit, "day"
	}
	return holdUntil, limit, label, nil
}

// ctoRateWait returns when one more new conversation fits. It counts only new
// requests from the same Agent to the CTO in the window, never replies.
func ctoRateWait(tx *sql.Tx, agent, cto string, now time.Time, span time.Duration, limit int, exclude string) (time.Time, error) {
	if limit <= 0 {
		return time.Time{}, nil
	}
	rows, err := tx.Query(`SELECT created_at FROM messages
		WHERE recipient=? AND parent_id='' AND type=? AND id<>? AND created_at>?
		AND sender IN (SELECT id FROM sessions WHERE agent_id=?)
		ORDER BY created_at, rowid`, cto, protocol.TypeRequest, exclude, now.Add(-span).UnixNano(), agent)
	if err != nil {
		return time.Time{}, err
	}
	var times []time.Time
	for rows.Next() {
		var at int64
		if err := rows.Scan(&at); err != nil {
			rows.Close()
			return time.Time{}, err
		}
		times = append(times, time.Unix(0, at))
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return time.Time{}, err
	}
	if len(times) < limit {
		return time.Time{}, nil
	}
	// The (n-limit)th oldest message must leave the window before one more
	// request fits.
	return times[len(times)-limit].Add(span), nil
}

// ReleaseCTOHolds releases the lead-level requests whose rate window now
// allows them and returns them ready to dispatch.
func (s *Store) ReleaseCTOHolds(now time.Time) ([]protocol.Message, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT id FROM messages WHERE state=? AND hold_kind=? AND hold_until<=? AND expires_at>?
		ORDER BY created_at, rowid`, protocol.StateHeld, ctoRateHoldKind, now.UnixNano(), now.UnixNano())
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	var out []protocol.Message
	for _, id := range ids {
		m, ok, err := s.message(tx, "m.id=?", id)
		if err != nil {
			return nil, err
		}
		if !ok || m.State != protocol.StateHeld {
			continue
		}
		var agent, cto string
		err = tx.QueryRow(`SELECT COALESCE(s.agent_id,''),COALESCE((SELECT value FROM owner_settings WHERE key='cto_session'),'')
			FROM sessions s WHERE s.id=?`, m.From).Scan(&agent, &cto)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return nil, err
		}
		if agent == "" || cto == "" {
			continue
		}
		holdUntil, _, _, err := ctoRequestHold(tx, agent, cto, now, id)
		if err != nil {
			return nil, err
		}
		if !holdUntil.IsZero() {
			if _, err := tx.Exec(`UPDATE messages SET hold_until=? WHERE id=?`, holdUntil.UnixNano(), id); err != nil {
				return nil, err
			}
			continue
		}
		res, err := tx.Exec(`UPDATE messages SET state=?, hold_kind='', hold_until=0 WHERE id=? AND state=?`, protocol.StateStored, id, protocol.StateHeld)
		if err != nil {
			return nil, err
		}
		if n, err := res.RowsAffected(); err != nil || n != 1 {
			if err != nil {
				return nil, err
			}
			continue
		}
		m.State = protocol.StateStored
		out = append(out, m)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

// ctoHoldNote is the sender's receipt line for a held request.
func ctoHoldNote(limit int, label string, until time.Time, loc *time.Location) string {
	return fmt.Sprintf(ctoRateHoldLabel, limit, label, until.In(loc).Format("15:04"))
}

package gateway

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Chief health. An online chief session can still be unable to work: its
// provider has no quota, its last turn failed with a usage limit, or it got
// its batch and sorted nothing. The chief is then unavailable. New asks skip
// it, the asks that wait for it move to the owner path (the Desk, which the CTO
// reads and answers, as before the chief existed), and the Desk says why.

// chiefSilentKey is the owner_settings row that holds the time (Unix nanos) a
// silent chief was marked unavailable. A sort by the chief, or ChiefRetryAfter,
// ends it.
const chiefSilentKey = "chief_silent_at"

// chiefHealth is what agent-net knows about the chief session now.
type chiefHealth struct {
	// id is the chief session when it can work, "" otherwise.
	id string
	// down says why the chief cannot work. It is "" when it can, and when no
	// chief is registered.
	down string
	// offline marks a session that is not online. Asks that wait for it are
	// returned as before: they may be adopted again when it is back.
	offline bool
}

// chiefQuotaReading is the quota verdict the gateway stored for one session.
type chiefQuotaReading struct{ session, reason string }

// setChiefQuota stores why the chief session's provider has no quota ("" when
// it has some). The gateway tick sets it from the machine's quota reading; a
// transaction never reads quota data, so routing an ask cannot wait on it.
func (s *Store) setChiefQuota(session, reason string) {
	s.chiefQuota.Store(&chiefQuotaReading{session: session, reason: reason})
}

// availableChief reads the health of the registered chief. Routing, batches and
// adoption use it; liveChief only says the session is online, which is all a
// sort needs. It may write the silence mark.
func (s *Store) availableChief(q askDB, now time.Time) (chiefHealth, error) {
	id, err := chiefSession(q)
	if err != nil || id == "" {
		return chiefHealth{}, err
	}
	var online, archived, lost, limitedUntil int64
	var limitedReason string
	err = q.QueryRow(`SELECT online, archived_at, harness_lost, limited_until, limited_reason FROM sessions WHERE id=?`, id).
		Scan(&online, &archived, &lost, &limitedUntil, &limitedReason)
	if errors.Is(err, sql.ErrNoRows) {
		return chiefHealth{down: "session offline", offline: true}, nil
	}
	if err != nil {
		return chiefHealth{}, err
	}
	switch {
	case archived != 0 || online == 0:
		if lost != 0 && archived == 0 {
			return chiefHealth{down: "harness lost", offline: true}, nil
		}
		return chiefHealth{down: "session offline", offline: true}, nil
	case limitedUntil > now.Unix():
		why := "harness error: " + limitedReason
		if limitedReason == "" {
			why = "harness error: usage limit"
		}
		return chiefHealth{down: why + " until " + time.Unix(limitedUntil, 0).UTC().Format("2006-01-02 15:04 UTC")}, nil
	}
	if r := s.chiefQuota.Load(); r != nil && r.session == id && r.reason != "" {
		return chiefHealth{down: r.reason}, nil
	}
	silent, err := s.chiefSilent(q, id, now)
	if err != nil {
		return chiefHealth{}, err
	}
	if silent {
		return chiefHealth{down: protocol.ChiefSilentText(protocol.ChiefSilentAfter)}, nil
	}
	return chiefHealth{id: id}, nil
}

// chiefSilent tells whether the chief got asks and has sorted nothing, with
// no harness activity, for ChiefSilentAfter. The verdict is kept in chiefSilentKey so that moving the
// asks away does not end it. It ends at the chief's next sort, or after
// ChiefRetryAfter, when the next ask tries the chief again.
func (s *Store) chiefSilent(q askDB, chief string, now time.Time) (bool, error) {
	nowN := now.UnixNano()
	var lastSort int64
	if err := q.QueryRow(`SELECT COALESCE(MAX(at),0) FROM ask_sorts WHERE sorter=?`, chief).Scan(&lastSort); err != nil {
		return false, err
	}
	var markedS string
	err := q.QueryRow(`SELECT value FROM owner_settings WHERE key=?`, chiefSilentKey).Scan(&markedS)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if err == nil {
		var marked int64
		fmt.Sscan(markedS, &marked)
		if lastSort <= marked && nowN-marked < int64(protocol.ChiefRetryAfter) {
			return true, nil
		}
		if _, err := q.Exec(`DELETE FROM owner_settings WHERE key=?`, chiefSilentKey); err != nil {
			return false, err
		}
	}
	// The oldest ask that was sent to the chief and still waits for it.
	var oldest sql.NullInt64
	if err := q.QueryRow(`SELECT MIN(chief_sent_at) FROM inbox_items WHERE status='open' AND next_who=? AND chief_sent_at>0`, protocol.AskNextChief).Scan(&oldest); err != nil {
		return false, err
	}
	if !oldest.Valid {
		return false, nil
	}
	// A chief whose harness did something lately (output, a turn, a delivery
	// acknowledgement) is working, even if it has not sorted yet.
	var activity int64
	if err := q.QueryRow(`SELECT harness_activity FROM sessions WHERE id=?`, chief).Scan(&activity); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	anchor := max(oldest.Int64, lastSort, activity)
	if nowN-anchor < int64(protocol.ChiefSilentAfter) {
		return false, nil
	}
	_, err = q.Exec(`INSERT INTO owner_settings(key,value,updated_at) VALUES(?,?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`, chiefSilentKey, fmt.Sprint(nowN), nowN)
	return err == nil, err
}

// releaseChiefAsks gives the asks that wait for the chief to the owner path
// when the chief cannot work, and the ones it has held for ChiefTimeout. Each
// gets a log line that says why. The gateway tick runs it, so a chief that
// stopped working loses its asks in seconds, not at the next minute reconcile.
func (s *Store) releaseChiefAsks(now time.Time) error {
	h, err := s.availableChief(s.db, now)
	if err != nil {
		return err
	}
	rows, err := s.db.Query(`SELECT id,text,about_kind,about_id,created_at,chief_at FROM inbox_items WHERE status='open' AND next_who=?`, protocol.AskNextChief)
	if err != nil {
		return err
	}
	type held struct {
		id, text, aboutKind, aboutID string
		created, chiefAt             int64
	}
	var late []held
	cutoff := now.Add(-protocol.ChiefTimeout).UnixNano()
	for rows.Next() {
		var x held
		if err := rows.Scan(&x.id, &x.text, &x.aboutKind, &x.aboutID, &x.created, &x.chiefAt); err != nil {
			rows.Close()
			return err
		}
		anchor := x.created
		if x.chiefAt != 0 {
			anchor = x.chiefAt
		}
		if h.id == "" || anchor < cutoff {
			late = append(late, x)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(late) == 0 {
		return err
	}
	for _, x := range late {
		why := "The chief of staff did not sort it in 15 minutes, so it is with you: "
		switch {
		case h.offline || (h.id == "" && h.down == ""):
			why = "The chief of staff is offline, so it is with you: "
		case h.id == "":
			why = "The chief of staff cannot work (" + h.down + "), so it is with you: "
		}
		// An ask moved past a chief that is online but cannot work is not
		// offered to it again when it is back (route_fixed).
		fixed := 0
		if h.id == "" && !h.offline && h.down != "" {
			fixed = 1
		}
		if _, err := s.db.Exec(`UPDATE inbox_items SET next_who=?, route_fixed=MAX(route_fixed,?) WHERE id=? AND next_who=?`, protocol.AskNextOwner, fixed, x.id, protocol.AskNextChief); err != nil {
			return err
		}
		if err := s.deskLog(s.db, "gateway", x.aboutKind, x.aboutID, why+x.text, "", nil); err != nil {
			return err
		}
	}
	return nil
}

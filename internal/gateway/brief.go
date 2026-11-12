package gateway

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

// The daily brief of the chief of staff (decision 0114). The gateway wakes the
// chief once per owner-local date at chief.brief_time, and the chief delivers
// one informational item with agent_owner_brief. The date sent is durable, so
// a restart never sends a second wake and a missed tick still sends once later
// that day. Without the owner's time zone no timer runs.

type briefSchedule struct {
	timeText string
	hour     int
	minute   int
	zoneName string
	loc      *time.Location
	zoneSet  bool
	valid    bool
}

func parseBriefClock(text string) (hour, minute int, ok bool) {
	parts := strings.Split(strings.TrimSpace(text), ":")
	if len(parts) != 2 {
		return 0, 0, false
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil || h < 0 || h > 23 {
		return 0, 0, false
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil || m < 0 || m > 59 || len(parts[1]) != 2 {
		return 0, 0, false
	}
	return h, m, true
}

func (s *Store) briefSchedule() (briefSchedule, error) {
	out := briefSchedule{}
	zone, ok, err := s.ownerTimeZone()
	if err != nil {
		return out, err
	}
	if ok {
		out.zoneName = zone
		out.loc, err = time.LoadLocation(zone)
		if err == nil {
			out.zoneSet = true
		} else {
			// A stored name that no longer loads is not a zone to wait on.
			out.zoneName = ""
		}
	}
	snap, err := s.SettingsSnapshot()
	if err != nil {
		return out, err
	}
	out.timeText = strings.TrimSpace(snap.String(protocol.ChiefBriefTimeKey, settings.Where{}))
	if out.timeText == "" {
		out.timeText = protocol.DefaultChiefBriefTime
	}
	if out.hour, out.minute, out.valid = parseBriefClock(out.timeText); !out.valid {
		out.hour, out.minute = 9, 0
	}
	return out, nil
}

func ownerSetting(q askDB, key string) (string, error) {
	var value string
	err := q.QueryRow(`SELECT value FROM owner_settings WHERE key=?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return value, err
}

func (s *Store) ownerSetting(key string) (string, error) {
	return ownerSetting(s.db, key)
}

// briefLastWake is the owner-local date of the last wake. Before the wake had
// its own key, an older gateway wrote it to BriefLastSentKey; read that value
// as the wake date when the new key is empty, so an upgrade does not wake the
// chief twice on the same day.
func briefLastWake(q askDB) (string, error) {
	wake, err := ownerSetting(q, protocol.BriefLastWakeKey)
	if err != nil || wake != "" {
		return wake, err
	}
	return ownerSetting(q, protocol.BriefLastSentKey)
}

func (s *Store) setOwnerSetting(q askDB, key, value string, now time.Time) error {
	_, err := q.Exec(`INSERT INTO owner_settings(key,value,updated_at) VALUES(?,?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`, key, value, now.UnixNano())
	return err
}

// briefDue says whether the brief of the current owner-local date is due and
// has not gone out.
func briefDue(now time.Time, loc *time.Location, hour, minute int, lastWake string) bool {
	local := now.In(loc)
	today := time.Date(local.Year(), local.Month(), local.Day(), hour, minute, 0, 0, loc)
	return !local.Before(today) && lastWake != local.Format("2006-01-02")
}

// nextBriefRun is the next time the timer would send. When the brief is due
// today and has not gone out, it is the next minute tick.
func nextBriefRun(now time.Time, loc *time.Location, hour, minute int, lastWake string) time.Time {
	local := now.In(loc)
	today := time.Date(local.Year(), local.Month(), local.Day(), hour, minute, 0, 0, loc)
	if lastWake == local.Format("2006-01-02") {
		return today.AddDate(0, 0, 1)
	}
	if local.Before(today) {
		return today
	}
	return now.Truncate(time.Minute).Add(time.Minute)
}

// checkDailyBrief stores one wake message for the chief when the brief is due.
// The message is dispatched by the gateway; a nil and false answer means the
// timer did nothing.
func (s *Store) checkDailyBrief(now time.Time) (protocol.Message, bool, error) {
	sch, err := s.briefSchedule()
	if err != nil || !sch.zoneSet || !sch.valid {
		return protocol.Message{}, false, err
	}
	chief, err := chiefSession(s.db)
	if err != nil || chief == "" {
		return protocol.Message{}, false, err
	}
	last, err := briefLastWake(s.db)
	if err != nil || !briefDue(now, sch.loc, sch.hour, sch.minute, last) {
		return protocol.Message{}, false, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return protocol.Message{}, false, err
	}
	defer tx.Rollback()
	// Re-read inside the transaction: a tick cannot send twice even if two
	// callers race.
	last, err = briefLastWake(tx)
	if err != nil {
		return protocol.Message{}, false, err
	}
	date := now.In(sch.loc).Format("2006-01-02")
	if !briefDue(now, sch.loc, sch.hour, sch.minute, last) {
		return protocol.Message{}, false, nil
	}
	body := fmt.Sprintf("Daily brief due for %s. Write it and deliver it with agent_owner_brief.", date)
	// Claimed asks that went quiet show in the brief (decision 0150).
	open, err := s.stillOpenText(now)
	if err != nil {
		return protocol.Message{}, false, err
	}
	if open != "" {
		body += "\n\nStill open (claimed, no move for the stuck-work limit):\n" + open
	}
	m, err := s.storeAgentNetMessage(tx, chief, body, "", now)
	if err != nil {
		return protocol.Message{}, false, err
	}
	if err = s.setOwnerSetting(tx, protocol.BriefLastWakeKey, date, now); err != nil {
		return protocol.Message{}, false, err
	}
	return m, true, tx.Commit()
}

// dailyBriefStatus returns the schedule and the delivery receipt.
func (s *Store) dailyBriefStatus(now time.Time) (protocol.OwnerBriefResult, error) {
	sch, err := s.briefSchedule()
	if err != nil {
		return protocol.OwnerBriefResult{}, err
	}
	out := protocol.OwnerBriefResult{BriefTime: sch.timeText, TimeZone: sch.zoneName}
	if out.LastDateSent, err = s.ownerSetting(protocol.BriefLastSentKey); err != nil {
		return out, err
	}
	if out.LastWakeDate, err = briefLastWake(s.db); err != nil {
		return out, err
	}
	if out.LastItemID, err = s.ownerSetting(protocol.BriefLastItemKey); err != nil {
		return out, err
	}
	// An older gateway wrote the wake date to BriefLastSentKey. A delivered
	// date always has its brief item, so report the newest item's date when
	// the stored date has no item of its own.
	if out.LastDateSent != "" {
		delivered := ""
		if out.LastItemID != "" {
			var ref string
			if err := s.db.QueryRow(`SELECT source_ref FROM inbox_items WHERE id=?`, out.LastItemID).Scan(&ref); err != nil {
				return out, err
			}
			delivered = strings.TrimPrefix(ref, protocol.BriefSourceRef(""))
		}
		if delivered != out.LastDateSent {
			out.LastDateSent = delivered
		}
	}
	if !sch.zoneSet {
		out.Status = protocol.BriefStatusWaitingZone
		return out, nil
	}
	if !sch.valid {
		out.Status = protocol.BriefStatusLooksInvalid
		return out, nil
	}
	chief, err := chiefSession(s.db)
	if err != nil {
		return out, err
	}
	if chief == "" {
		out.Status = protocol.BriefStatusNoChief
		return out, nil
	}
	out.Status = protocol.BriefStatusActive
	next := nextBriefRun(now, sch.loc, sch.hour, sch.minute, out.LastWakeDate)
	out.NextRunLocal = next.In(sch.loc).Format("2006-01-02 15:04")
	out.NextRunUTC = next.UTC().Format(time.RFC3339)
	return out, nil
}

// putOwnerBrief stores or replaces the brief of one owner-local date. Only the
// live chief may deliver one. The item is informational: its only action is
// dismiss.
func (s *Store) putOwnerBrief(session string, a protocol.OwnerBriefArgs, now time.Time) (protocol.OwnerBriefResult, error) {
	if err := a.Validate(); err != nil {
		return protocol.OwnerBriefResult{}, err
	}
	if a.IsStatus() {
		return protocol.OwnerBriefResult{}, reject("a brief body is required")
	}
	chief, err := liveChief(s.db)
	if err != nil {
		return protocol.OwnerBriefResult{}, err
	}
	if chief == "" || chief != session {
		return protocol.OwnerBriefResult{}, reject("only the chief of staff may deliver a brief")
	}
	date := strings.TrimSpace(a.Date)
	body := strings.TrimRight(a.Body, "\r\n")
	// The gateway adds the system-health line from the CTO intake queue and
	// the day's recorded decisions (decision 0152), so the brief always has
	// them, whatever the chief wrote.
	extra, err := s.briefFacts(now)
	if err != nil {
		return protocol.OwnerBriefResult{}, err
	}
	body += "\n\n" + extra
	label := "Daily brief " + date
	ref := protocol.BriefSourceRef(date)
	tx, err := s.db.Begin()
	if err != nil {
		return protocol.OwnerBriefResult{}, err
	}
	defer tx.Rollback()
	var id string
	err = tx.QueryRow(`SELECT id FROM inbox_items WHERE source_ref=? ORDER BY created_at DESC, id DESC LIMIT 1`, ref).Scan(&id)
	switch {
	case err == nil:
		// One brief per local date. A repeat keeps the item id and replaces
		// its text, also when the owner dismissed the earlier one.
		_, err = tx.Exec(`UPDATE inbox_items SET kind=?,session_id=?,node='',text=?,recommendation='',message_id='',conversation_id='',
			created_at=?,status='open',answer='',answered_at=0,options='[]',asker_name=?,change_title='',
			from_kind=?,source_ref=?,about_kind='',about_id='',next_who=?,why_you='',detail='',route_fixed=1,priority=0
			WHERE id=?`, protocol.AskKindBrief, chief, body, now.UnixNano(), label, protocol.AskFromGateway, ref, protocol.AskNextOwner, id)
		if err != nil {
			return protocol.OwnerBriefResult{}, err
		}
	case errors.Is(err, sql.ErrNoRows):
		item, cerr := s.createInboxItem(tx, protocol.InboxItem{Kind: protocol.AskKindBrief, SessionID: chief, Text: body,
			AskerName: label, FromKind: protocol.AskFromGateway, SourceRef: ref, NextWho: protocol.AskNextOwner})
		if cerr != nil {
			return protocol.OwnerBriefResult{}, cerr
		}
		id = item.ID
	default:
		return protocol.OwnerBriefResult{}, err
	}
	if err = s.setOwnerSetting(tx, protocol.BriefLastItemKey, id, now); err != nil {
		return protocol.OwnerBriefResult{}, err
	}
	if err = s.setOwnerSetting(tx, protocol.BriefLastSentKey, date, now); err != nil {
		return protocol.OwnerBriefResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return protocol.OwnerBriefResult{}, err
	}
	return protocol.OwnerBriefResult{ItemID: id, Date: date, Status: "delivered"}, nil
}

// briefDecisionsMax caps the decision list of one brief.
const briefDecisionsMax = 20

// briefFacts is what the gateway adds to the morning brief: one
// system-health line and the decisions recorded in the last 24 hours.
func (s *Store) briefFacts(now time.Time) (string, error) {
	health, err := s.healthLine(s.db)
	if err != nil {
		return "", err
	}
	rows, err := s.db.Query(`SELECT text FROM standing_orders WHERE kind IN (?,?) AND saved_at>=? ORDER BY saved_at,id`,
		protocol.StandingKindAnswer, protocol.StandingKindRule, now.Add(-24*time.Hour).Unix())
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var lines []string
	n := 0
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			return "", err
		}
		n++
		if len(lines) < briefDecisionsMax {
			lines = append(lines, "- "+askFirstLine(text, 160))
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	out := health + "\n"
	if n == 0 {
		return out + "Decisions recorded today: none.", nil
	}
	out += "Decisions recorded today:\n" + strings.Join(lines, "\n")
	if n > len(lines) {
		out += fmt.Sprintf("\n- and %d more", n-len(lines))
	}
	return out, nil
}

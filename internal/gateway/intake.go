package gateway

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The CTO intake queue of decision 0152. Detectors write entries keyed by
// (kind, subject); a repeat raises the count and the last seen time. An entry
// whose wake rule holds raises one P1 "CTO alert" item in the chief's queue,
// naming the lead that owns the cause. Nothing here starts a CTO turn or
// reaches the owner.

// ctoIntakeMigration adds the queue and the merge queue detector's counter.
const ctoIntakeMigration = `
-- CTO intake queue (decision 0152).
CREATE TABLE IF NOT EXISTS cto_intake (
 id TEXT PRIMARY KEY,
 kind TEXT NOT NULL,
 subject TEXT NOT NULL,
 severity INTEGER NOT NULL,
 text TEXT NOT NULL DEFAULT '',
 detail TEXT NOT NULL DEFAULT '',
 first_seen INTEGER NOT NULL,
 last_seen INTEGER NOT NULL,
 count INTEGER NOT NULL DEFAULT 1,
 state TEXT NOT NULL DEFAULT 'open',
 alert_ask TEXT NOT NULL DEFAULT '',
 stuck_ask TEXT NOT NULL DEFAULT '',
 lead TEXT NOT NULL DEFAULT '',
 closed_at INTEGER NOT NULL DEFAULT 0,
 close_reason TEXT NOT NULL DEFAULT '');
CREATE UNIQUE INDEX IF NOT EXISTS cto_intake_open ON cto_intake(kind,subject) WHERE state='open';
CREATE INDEX IF NOT EXISTS cto_intake_seen ON cto_intake(state,last_seen);
-- The merge queue detector: failed test runs in a row on a project's train tip.
CREATE TABLE IF NOT EXISTS merge_train_fails (
 project TEXT PRIMARY KEY,
 fails INTEGER NOT NULL DEFAULT 0,
 last_run TEXT NOT NULL DEFAULT '',
 updated_at INTEGER NOT NULL DEFAULT 0);
`

// Intake close reasons.
const (
	intakeResolved = "resolved"
	intakeReturned = "returned"
	intakeSlept    = "slept"
	intakePassed   = "passed"
)

// intakeEvent is one sighting a detector reports.
type intakeEvent struct {
	Kind, Subject, Text, Detail string
	// AboutKind and AboutID tie the CTO alert to what it is about, so the
	// code that closes asks about a machine closes it too.
	AboutKind, AboutID string
	// Project is the project that owns the cause, when the detector knows it.
	Project string
}

// recordIntake stores one sighting in the caller's transaction and raises the
// CTO alert when the kind's wake rule holds. It returns the entry.
func (s *Store) recordIntake(tx askDB, ev intakeEvent, now time.Time) (protocol.IntakeEntry, error) {
	rt, ok := routeOf(routeIntake, ev.Kind)
	if !ok {
		return protocol.IntakeEntry{}, fmt.Errorf("intake kind %q has no route", ev.Kind)
	}
	if strings.TrimSpace(ev.Subject) == "" {
		return protocol.IntakeEntry{}, errors.New("intake subject is required")
	}
	n := now.UnixNano()
	res, err := tx.Exec(`UPDATE cto_intake SET count=count+1,last_seen=?,text=?,detail=? WHERE kind=? AND subject=? AND state='open'`,
		n, ev.Text, ev.Detail, ev.Kind, ev.Subject)
	if err != nil {
		return protocol.IntakeEntry{}, err
	}
	if c, err := res.RowsAffected(); err != nil {
		return protocol.IntakeEntry{}, err
	} else if c == 0 {
		severity := protocol.IntakeSeverityAlert
		if rt.Wake == wakeNever {
			severity = protocol.IntakeSeverityDigest
		}
		if _, err := tx.Exec(`INSERT INTO cto_intake(id,kind,subject,severity,text,detail,first_seen,last_seen,count,lead) VALUES(?,?,?,?,?,?,?,?,1,?)`,
			protocol.NewID("ci"), ev.Kind, ev.Subject, severity, ev.Text, ev.Detail, n, n, s.causeLead(tx, ev.Project)); err != nil {
			return protocol.IntakeEntry{}, err
		}
	}
	e, err := openIntake(tx, ev.Kind, ev.Subject)
	if err != nil {
		return e, err
	}
	if e.Alert == "" && intakeWakes(rt.Wake, e) {
		if e, err = s.raiseCTOAlert(tx, e, ev); err != nil {
			return e, err
		}
	}
	return e, nil
}

// intakeWakes applies a wake rule of decision 0152 to an entry.
func intakeWakes(rule string, e protocol.IntakeEntry) bool {
	switch rule {
	case wakeAtOnce:
		return true
	case wakeRepeatHour:
		return e.Count > 1 && e.LastSeen.Sub(e.FirstSeen) > protocol.IntakeRepeatWake
	}
	return false
}

// raiseCTOAlert opens the one P1 "CTO alert" item of an entry in the chief's
// queue. It names the lead that owns the cause; the chief routes it there.
func (s *Store) raiseCTOAlert(tx askDB, e protocol.IntakeEntry, ev intakeEvent) (protocol.IntakeEntry, error) {
	lead := e.Lead
	if lead == "" {
		lead = "none: the CTO owns it"
	}
	item, err := s.createInboxItem(tx, protocol.InboxItem{
		Kind: protocol.AskKindCTOAlert, SessionID: gatewayAsker, Node: ev.Project,
		FromKind: protocol.AskFromGateway, AskerName: "agent-net",
		SourceRef: "cto-alert:" + e.ID, AboutKind: ev.AboutKind, AboutID: ev.AboutID,
		NextWho: protocol.AskNextChief,
		Text:    "CTO alert: " + e.Text,
		WhyYou:  "Lead that owns the cause: " + lead + ". Route it there and tell the CTO; it reaches the owner only for money, secrets or security.",
		Detail:  fmt.Sprintf("CTO intake entry %s (%s, %s): seen %d times since %s.", e.ID, e.Kind, e.Subject, e.Count, e.FirstSeen.UTC().Format(time.RFC3339)),
	})
	if err != nil {
		return e, err
	}
	if _, err := tx.Exec(`UPDATE inbox_items SET priority=1 WHERE id=?`, item.ID); err != nil {
		return e, err
	}
	if _, err := tx.Exec(`UPDATE cto_intake SET alert_ask=?,severity=? WHERE id=?`, item.ID, protocol.IntakeSeverityWake, e.ID); err != nil {
		return e, err
	}
	e.Alert, e.Severity = item.ID, protocol.IntakeSeverityWake
	return e, nil
}

// causeLead names the lead that owns the cause: the project's lead when the
// detector knows the project, else the lead of agent-net itself, since the
// other alerts are about agent-net's own machines, harnesses and providers.
func (s *Store) causeLead(q askDB, project string) string {
	for _, p := range []string{project, "agent-net"} {
		if p == "" {
			continue
		}
		var repo string
		err := q.QueryRow(`SELECT id FROM repos WHERE id=? OR display_name=? LIMIT 1`, p, p).Scan(&repo)
		if err != nil {
			continue
		}
		lead, err := repoTopAgentQ(q, repo)
		if err != nil || lead == "" {
			continue
		}
		if name, err := agentNameQ(q, lead); err == nil && name != "" {
			return name
		}
	}
	return ""
}

// openIntake reads the open entry of a kind and subject.
func openIntake(q askDB, kind, subject string) (protocol.IntakeEntry, error) {
	return scanIntake(q.QueryRow(`SELECT `+intakeColumns+` FROM cto_intake WHERE kind=? AND subject=? AND state='open'`, kind, subject))
}

const intakeColumns = `id,kind,subject,severity,text,detail,first_seen,last_seen,count,alert_ask,stuck_ask,lead`

func scanIntake(row inboxScanner) (protocol.IntakeEntry, error) {
	var e protocol.IntakeEntry
	var first, last int64
	err := row.Scan(&e.ID, &e.Kind, &e.Subject, &e.Severity, &e.Text, &e.Detail, &first, &last, &e.Count, &e.Alert, &e.Stuck, &e.Lead)
	e.FirstSeen, e.LastSeen = time.Unix(0, first).UTC(), time.Unix(0, last).UTC()
	return e, err
}

// closeIntake closes the open entry of a kind and subject, and with it the
// CTO alert and the stuck-work item it raised: closing the cause closes the
// whole thread. It is a no-op when no entry is open.
func (s *Store) closeIntake(tx askDB, kind, subject, reason string, now time.Time) error {
	e, err := openIntake(tx, kind, subject)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE cto_intake SET state='closed',closed_at=?,close_reason=? WHERE id=?`, now.UnixNano(), reason, e.ID); err != nil {
		return err
	}
	for _, id := range []string{e.Alert, e.Stuck} {
		if id == "" {
			continue
		}
		if _, err := s.closeAsks(tx, `id=?`, []any{id}, protocol.AskReasonResolved); err != nil {
			return err
		}
	}
	return nil
}

// openIntakeSubjects lists the subjects of the open entries of one kind.
func (s *Store) openIntakeSubjects(kind string) (map[string]bool, error) {
	rows, err := s.db.Query(`SELECT subject FROM cto_intake WHERE kind=? AND state='open'`, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var subject string
		if err := rows.Scan(&subject); err != nil {
			return nil, err
		}
		out[subject] = true
	}
	return out, rows.Err()
}

// RecordIntake stores one sighting in its own transaction.
func (s *Store) RecordIntake(ev intakeEvent) (protocol.IntakeEntry, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return protocol.IntakeEntry{}, err
	}
	defer tx.Rollback()
	e, err := s.recordIntake(tx, ev, s.now().UTC())
	if err != nil {
		return e, err
	}
	return e, tx.Commit()
}

// CloseIntake closes an entry and its thread in its own transaction.
func (s *Store) CloseIntake(kind, subject, reason string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := s.closeIntake(tx, kind, subject, reason, s.now().UTC()); err != nil {
		return err
	}
	return tx.Commit()
}

// intakeFindMax caps one read; the queue is deduplicated, so it stays short.
const intakeFindMax = 200

// CTOIntake returns the open entries, newest first. The CTO calls it at the
// start of each owner conversation.
func (s *Store) CTOIntake(limit int) (protocol.IntakeFindResult, error) {
	if limit <= 0 || limit > intakeFindMax {
		limit = intakeFindMax
	}
	out := protocol.IntakeFindResult{Entries: []protocol.IntakeEntry{}}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM cto_intake WHERE state='open'`).Scan(&out.Total); err != nil {
		return out, err
	}
	rows, err := s.db.Query(`SELECT `+intakeColumns+` FROM cto_intake WHERE state='open' ORDER BY last_seen DESC,id LIMIT ?`, limit)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		e, err := scanIntake(rows)
		if err != nil {
			return out, err
		}
		out.Entries = append(out.Entries, e)
	}
	return out, rows.Err()
}

// ReportIntake stores a daemon's report. The subject gains the machine's name,
// so the same harness on two machines is two entries.
func (s *Store) ReportIntake(node string, r protocol.IntakeReport) error {
	if err := r.Validate(); err != nil {
		return reject("%s", err.Error())
	}
	subject := s.hostname(node) + "/" + strings.TrimSpace(r.Subject)
	if r.Resolved {
		return s.CloseIntake(r.Kind, subject, intakeResolved)
	}
	_, err := s.RecordIntake(intakeEvent{Kind: r.Kind, Subject: subject, Text: r.Text, Detail: r.Detail})
	return err
}

// quotaStuck records that work stopped because a provider has no quota left:
// the CTO entry of the provider (the cause) and the chief's stuck-work item
// that links it are one thread.
func (s *Store) quotaStuck(tx askDB, provider, text string, now time.Time) (protocol.IntakeEntry, error) {
	e, err := s.recordIntake(tx, intakeEvent{Kind: protocol.IntakeQuota, Subject: provider, Text: text}, now)
	if err != nil || e.Stuck != "" {
		return e, err
	}
	item, err := s.createInboxItem(tx, protocol.InboxItem{
		Kind: protocol.AskKindQuotaStuck, SessionID: gatewayAsker, FromKind: protocol.AskFromGateway, AskerName: "agent-net",
		SourceRef: "quota-stuck:" + provider, NextWho: protocol.AskNextChief,
		Text:   "Work is stuck: " + text,
		WhyYou: "Get the work moving or re-route it. The CTO fixes the cause; buying more goes to the owner through you.",
		Detail: "CTO intake entry " + e.ID + " (quota, " + provider + "). It closes when the provider has quota again.",
	})
	if err != nil {
		return e, err
	}
	if _, err := tx.Exec(`UPDATE cto_intake SET stuck_ask=? WHERE id=?`, item.ID, e.ID); err != nil {
		return e, err
	}
	e.Stuck = item.ID
	return e, nil
}

// healthLine is the morning brief's one system-health line, from the open
// entries of the queue.
func (s *Store) healthLine(q askDB) (string, error) {
	rows, err := q.Query(`SELECT kind,severity,COUNT(*) FROM cto_intake WHERE state='open' GROUP BY kind,severity ORDER BY severity,kind`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	total, wakes := 0, 0
	var kinds []string
	seen := map[string]bool{}
	for rows.Next() {
		var kind string
		var sev, n int
		if err := rows.Scan(&kind, &sev, &n); err != nil {
			return "", err
		}
		total += n
		if sev == protocol.IntakeSeverityWake {
			wakes += n
		}
		if !seen[kind] {
			seen[kind] = true
			kinds = append(kinds, strings.ReplaceAll(kind, "_", " "))
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if total == 0 {
		return "System health: no open alerts.", nil
	}
	alerts, woke := fmt.Sprintf("%d open alerts", total), fmt.Sprintf("%d woke the CTO", wakes)
	if total == 1 {
		alerts = "1 open alert"
	}
	if wakes == 0 {
		woke = "none woke the CTO"
	}
	return fmt.Sprintf("System health: %s, %s (%s).", alerts, woke, strings.Join(kinds, ", ")), nil
}

package gateway

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The CTO intake queue of decision 0152, stored as inbox_items owned by 'cto'
// keyed by dedupe_key (kind:subject). A repeat bumps count and last_seen; a
// wake or quota_stuck promotes that same row into the chief's queue.

// ctoIntakeMigration is the shipped migration; deskTablesMigration later folds
// cto_intake into inbox_items.
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

const (
	intakeResolved = "resolved"
	intakeReturned = "returned"
	intakeSlept    = "slept"
	intakePassed   = "passed"
	intakeFindMax  = 200
)

type intakeEvent struct {
	Kind, Subject, Text, Detail string
	AboutKind, AboutID, Project string
}

func (s *Store) recordIntake(tx askDB, ev intakeEvent, now time.Time) (protocol.IntakeEntry, error) {
	rt, ok := routeOf(routeIntake, ev.Kind)
	if !ok {
		return protocol.IntakeEntry{}, fmt.Errorf("intake kind %q has no route", ev.Kind)
	}
	if strings.TrimSpace(ev.Subject) == "" {
		return protocol.IntakeEntry{}, errors.New("intake subject is required")
	}
	n, key := now.UnixNano(), ev.Kind+":"+ev.Subject
	res, err := tx.Exec(`UPDATE inbox_items SET count=count+1, last_seen=?,
		text=CASE WHEN kind='cto_alert' THEN 'CTO alert: '||? WHEN kind='quota_stuck' THEN 'Work is stuck: '||? ELSE ? END,
		detail=CASE WHEN kind='cto_intake' THEN ? ELSE detail END WHERE dedupe_key=? AND status='open'`,
		n, ev.Text, ev.Text, ev.Text, ev.Detail, key)
	if err != nil {
		return protocol.IntakeEntry{}, err
	}
	if c, err := res.RowsAffected(); err != nil {
		return protocol.IntakeEntry{}, err
	} else if c == 0 {
		sev, cat := protocol.IntakeSeverityAlert, protocol.CategoryNormal
		if rt.Wake == wakeNever {
			sev = protocol.IntakeSeverityDigest
		}
		if ev.Kind == protocol.IntakeMachineSilent {
			cat = protocol.CategoryOutage
		}
		if _, err := tx.Exec(`INSERT INTO inbox_items (id,kind,session_id,node,text,recommendation,message_id,conversation_id,created_at,status,options,asker_name,change_title,
			from_kind,source_ref,about_kind,about_id,next_who,why_you,detail,route_fixed,owner,category,dedupe_key,count,first_seen,last_seen,severity)
			VALUES (?,?,?,?,?,'','','',?,'open','[]','agent-net','',?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			protocol.NewID("i"), protocol.AskKindCTOIntake, gatewayAsker, ev.Project, ev.Text, n,
			protocol.AskFromGateway, "", ev.AboutKind, ev.AboutID, protocol.DeskOwnerCTO, "", ev.Detail,
			1, protocol.DeskOwnerCTO, cat, key, 1, n, n, sev); err != nil {
			return protocol.IntakeEntry{}, err
		}
	}
	e, err := s.openIntake(tx, ev.Kind, ev.Subject)
	if err != nil {
		return e, err
	}
	if e.Alert == "" && (rt.Wake == wakeAtOnce || (rt.Wake == wakeRepeatHour && e.Count > 1 && e.LastSeen.Sub(e.FirstSeen) > protocol.IntakeRepeatWake)) {
		lead := e.Lead
		if lead == "" {
			lead = "none: the CTO owns it"
		}
		why := "Lead that owns the cause: " + lead + ". Route it there and tell the CTO; it reaches the owner only for money, secrets or security."
		detail := fmt.Sprintf("CTO intake entry %s (%s, %s): seen %d times since %s.", e.ID, e.Kind, e.Subject, e.Count, e.FirstSeen.UTC().Format(time.RFC3339))
		if _, err := tx.Exec(`UPDATE inbox_items SET kind=CASE WHEN kind='quota_stuck' THEN 'quota_stuck' ELSE ? END,
			next_who=?, priority=1, severity=?, source_ref=CASE WHEN source_ref='' THEN ? ELSE source_ref END,
			text=CASE WHEN kind='quota_stuck' THEN text ELSE ? END, why_you=CASE WHEN kind='quota_stuck' THEN why_you ELSE ? END,
			detail=CASE WHEN kind='quota_stuck' THEN detail ELSE ? END WHERE id=?`,
			protocol.AskKindCTOAlert, protocol.AskNextChief, protocol.IntakeSeverityWake, "cto-alert:"+e.ID, "CTO alert: "+e.Text, why, detail, e.ID); err != nil {
			return e, err
		}
		e.Alert, e.Severity = e.ID, protocol.IntakeSeverityWake
	}
	return e, nil
}

func (s *Store) causeLead(q askDB, project string) string {
	for _, p := range []string{project, "agent-net"} {
		if p == "" {
			continue
		}
		var repo string
		if q.QueryRow(`SELECT id FROM repos WHERE id=? OR display_name=? LIMIT 1`, p, p).Scan(&repo) != nil {
			continue
		}
		if lead, err := repoTopAgentQ(q, repo); err == nil && lead != "" {
			if name, err := agentNameQ(q, lead); err == nil && name != "" {
				return name
			}
		}
	}
	return ""
}

const intakeColumns = `id,kind,dedupe_key,severity,text,detail,first_seen,last_seen,count,node,why_you`

type scannedIntake struct {
	entry        protocol.IntakeEntry
	node, whyYou string
}

func scanIntakeRow(row inboxScanner) (scannedIntake, error) {
	var r scannedIntake
	var itemKind, key string
	var first, last int64
	if err := row.Scan(&r.entry.ID, &itemKind, &key, &r.entry.Severity, &r.entry.Text, &r.entry.Detail, &first, &last, &r.entry.Count, &r.node, &r.whyYou); err != nil {
		return r, err
	}
	r.entry.Kind, r.entry.Subject, _ = strings.Cut(key, ":")
	r.entry.FirstSeen, r.entry.LastSeen = time.Unix(0, first).UTC(), time.Unix(0, last).UTC()
	if r.entry.Severity == protocol.IntakeSeverityWake || itemKind == protocol.AskKindCTOAlert {
		r.entry.Alert, r.entry.Text = r.entry.ID, strings.TrimPrefix(r.entry.Text, "CTO alert: ")
	}
	if itemKind == protocol.AskKindQuotaStuck {
		r.entry.Stuck, r.entry.Text = r.entry.ID, strings.TrimPrefix(r.entry.Text, "Work is stuck: ")
	}
	return r, nil
}

func (s *Store) fillIntakeLead(q askDB, r *scannedIntake) {
	proj := r.node
	if proj == "" && r.entry.Kind == protocol.IntakeMergeQueue {
		proj = r.entry.Subject
	}
	if lead := s.causeLead(q, proj); lead != "" {
		r.entry.Lead = lead
	} else if rest, ok := strings.CutPrefix(r.whyYou, "Lead that owns the cause: "); ok {
		if lead, _, ok := strings.Cut(rest, "."); ok && lead != "none: the CTO owns it" {
			r.entry.Lead = lead
		}
	}
}

func (s *Store) openIntake(q askDB, kind, subject string) (protocol.IntakeEntry, error) {
	r, err := scanIntakeRow(q.QueryRow(`SELECT `+intakeColumns+` FROM inbox_items WHERE dedupe_key=? AND status='open'`, kind+":"+subject))
	if err != nil {
		return protocol.IntakeEntry{}, err
	}
	s.fillIntakeLead(q, &r)
	return r.entry, nil
}

func (s *Store) closeIntake(tx askDB, kind, subject, reason string, now time.Time) error {
	_, err := tx.Exec(`UPDATE inbox_items SET status='closed', answer=?, answered_at=?
		WHERE status='open' AND (dedupe_key=? OR (kind='quota_stuck' AND source_ref=?))`,
		reason, now.UnixNano(), kind+":"+subject, "quota-stuck:"+subject)
	return err
}

func (s *Store) openIntakeSubjects(kind string) (map[string]bool, error) {
	rows, err := s.db.Query(`SELECT substr(dedupe_key,?) FROM inbox_items WHERE dedupe_key LIKE ? AND status='open'`, len(kind)+2, kind+":%")
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

func (s *Store) CloseIntake(kind, subject, reason string) error {
	return s.closeIntake(s.db, kind, subject, reason, s.now().UTC())
}

// CTOIntake returns the open entries, newest first.
func (s *Store) CTOIntake(limit int) (protocol.IntakeFindResult, error) {
	if limit <= 0 || limit > intakeFindMax {
		limit = intakeFindMax
	}
	out := protocol.IntakeFindResult{Entries: []protocol.IntakeEntry{}}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM inbox_items WHERE dedupe_key<>'' AND status='open'`).Scan(&out.Total); err != nil {
		return out, err
	}
	rows, err := s.db.Query(`SELECT `+intakeColumns+` FROM inbox_items WHERE dedupe_key<>'' AND status='open' ORDER BY last_seen DESC,id LIMIT ?`, limit)
	if err != nil {
		return out, err
	}
	var scanned []scannedIntake
	for rows.Next() {
		r, err := scanIntakeRow(rows)
		if err != nil {
			rows.Close()
			return out, err
		}
		scanned = append(scanned, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	for i := range scanned {
		s.fillIntakeLead(s.db, &scanned[i])
		out.Entries = append(out.Entries, scanned[i].entry)
	}
	return out, nil
}

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

func (s *Store) quotaStuck(tx askDB, provider, text string, now time.Time) (protocol.IntakeEntry, error) {
	e, err := s.recordIntake(tx, intakeEvent{Kind: protocol.IntakeQuota, Subject: provider, Text: text}, now)
	if err != nil || e.Stuck != "" {
		return e, err
	}
	if _, err := tx.Exec(`UPDATE inbox_items SET kind=?, next_who=?, source_ref=?, text=?, why_you=?, detail=? WHERE id=?`,
		protocol.AskKindQuotaStuck, protocol.AskNextChief, "quota-stuck:"+provider, "Work is stuck: "+text,
		"Get the work moving or re-route it. The CTO fixes the cause; buying more goes to the owner through you.",
		"CTO intake entry "+e.ID+" (quota, "+provider+"). It closes when the provider has quota again.", e.ID); err != nil {
		return e, err
	}
	e.Stuck = e.ID
	return e, nil
}

func (s *Store) healthLine(q askDB) (string, error) {
	rows, err := q.Query(`SELECT substr(dedupe_key,1,instr(dedupe_key,':')-1) AS k, severity, COUNT(*)
		FROM inbox_items WHERE dedupe_key<>'' AND status='open' GROUP BY k, severity ORDER BY severity, k`)
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
	if err := rows.Err(); err != nil || total == 0 {
		return "System health: no open alerts.", err
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

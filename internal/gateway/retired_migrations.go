package gateway

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Retired migrations. The code that used these tables is gone, but a shipped
// migration never changes or moves (testdata/migrations.lock), so its text
// stays here. retiredTablesDropMigration, at the end of the list, drops the
// tables they create.

// sessionDuesMigration created the table of agent_due items. Due items were
// removed; retiredTablesDropMigration drops the table.
const sessionDuesMigration = `
CREATE TABLE IF NOT EXISTS session_dues (
 id TEXT PRIMARY KEY,
 session_id TEXT NOT NULL,
 what TEXT NOT NULL,
 due_at INTEGER NOT NULL DEFAULT 0,
 after_kind TEXT NOT NULL DEFAULT '' CHECK (after_kind IN ('','reply','change_step','ask_closed')),
 after_ref TEXT NOT NULL DEFAULT '',
 blocker TEXT NOT NULL DEFAULT 'none',
 status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','woken','active','raised','done')),
 woken_at INTEGER NOT NULL DEFAULT 0,
 created_at INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS session_dues_open ON session_dues(status, due_at);
CREATE INDEX IF NOT EXISTS session_dues_session ON session_dues(session_id, status);
`

// retiredTablesDropMigration drops the tables of four removed subsystems:
// session due items (agent_due), learned charter suggestions, findings
// (agent_learn) and permission proposals. Permissions are now granted only on
// the owner's words, with nothing stored. The older migrations that create or
// change these tables run before this one, so an old database replays through
// them first; IF EXISTS keeps a replayed drop safe. Automatic playbook notes
// came from findings: the unpinned ones go too, and the owner's notes stay.
const retiredTablesDropMigration = `
-- retired subsystems: session dues, learned suggestions, findings, permission proposals
DROP TABLE IF EXISTS session_dues;
DROP TABLE IF EXISTS learned_suggestions;
DROP TABLE IF EXISTS learned_suggestion_details;
DROP TABLE IF EXISTS learned_phrase_state;
DROP TABLE IF EXISTS learned_message_flow;
DROP TABLE IF EXISTS learned_phrase_runs;
DROP TABLE IF EXISTS learned_views;
DROP TABLE IF EXISTS findings;
DROP TABLE IF EXISTS permission_proposals;
DELETE FROM playbook WHERE source='auto' AND pinned=0;
`

// retiredTablesMarker finds retiredTablesDropMigration in the list.
const retiredTablesMarker = "retired subsystems: session dues"

// keepRetiredRows runs in the drop's transaction, before the drop. It keeps
// what someone still needs from the retired tables: each pending permission
// proposal becomes a message, and each finding an owner-wide note. Both use
// keys made from the row, so a replay adds nothing twice.
func (s *Store) keepRetiredRows(tx *sql.Tx) error {
	if err := s.sendPendingPermissions(tx); err != nil {
		return fmt.Errorf("pending permission proposals: %w", err)
	}
	if err := keepFindingsAsNotes(tx, s.now()); err != nil {
		return fmt.Errorf("findings: %w", err)
	}
	return nil
}

// retiredProposal is one pending row of permission_proposals.
type retiredProposal struct {
	id, kind, reason, by, name string
	rules                      []string
	targets                    []protocol.PermissionTarget
}

// sendPendingPermissions turns each pending permission proposal into one
// message. It goes to the chief of staff, who asks the owner. A proposer that
// is a known session sends it as a request, so the chief's reply reaches the
// proposer; otherwise agent-net sends it as a notice. With no chief it goes to
// the CTO as a notice.
func (s *Store) sendPendingPermissions(tx *sql.Tx) error {
	if ok, err := hasTable(tx, "permission_proposals"); err != nil || !ok {
		return err
	}
	props, err := pendingProposals(tx)
	if err != nil || len(props) == 0 {
		return err
	}
	to, err := chiefSession(tx)
	if err != nil {
		return err
	}
	viaChief := to != ""
	if !viaChief {
		if err := tx.QueryRow(`SELECT value FROM gateway_state WHERE key='cto_session'`).Scan(&to); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	if to == "" {
		if s.logf != nil {
			s.logf("permission.proposals_dropped", "count", len(props), "reason", "no chief and no CTO")
		}
		return nil
	}
	now := s.now().UTC()
	for _, p := range props {
		// The owner item prefix makes the gateway's one-second tick deliver
		// the stored message (flushHolderAsks).
		key := ownerItemKeyPrefix + "perm_migrate:" + p.id
		var sent int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM messages WHERE idem_key=?`, key).Scan(&sent); err != nil {
			return err
		}
		if sent > 0 {
			continue
		}
		from, typ := agentNetSender, protocol.TypeNotification
		if viaChief && p.by != to {
			var known int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id=?`, p.by).Scan(&known); err != nil {
				return err
			}
			if known > 0 {
				from, typ = p.by, protocol.TypeRequest
			}
		}
		if _, err := tx.Exec(`INSERT INTO messages(id,conversation_id,parent_id,sender,recipient,type,body,hop,idem_key,created_at,expires_at,state)
			VALUES(?,?,'',?,?,?,?,0,?,?,?,?)`, protocol.NewID("m"), protocol.NewID("c"), from, to, typ, pendingPermissionBody(tx, p),
			key, now.UnixNano(), now.Add(protocol.DefaultTTL).UnixNano(), protocol.StateStored); err != nil {
			return err
		}
		if s.logf != nil {
			s.logf("permission.proposal_sent", "proposal", p.id, "to", to, "type", typ)
		}
	}
	return nil
}

// pendingProposals reads the pending rows, oldest first. The rows are read
// whole before any message is written.
func pendingProposals(tx *sql.Tx) ([]retiredProposal, error) {
	rows, err := tx.Query(`SELECT id,kind,rules,targets,reason,proposed_by,proposed_name FROM permission_proposals WHERE status='pending' ORDER BY created_at,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []retiredProposal
	for rows.Next() {
		var p retiredProposal
		var rules, targets string
		if err := rows.Scan(&p.id, &p.kind, &rules, &targets, &p.reason, &p.by, &p.name); err != nil {
			return nil, err
		}
		// The gateway wrote both with json.Marshal; a row it cannot read
		// still goes out with its reason and the raw text in the body.
		if json.Unmarshal([]byte(rules), &p.rules) != nil {
			p.rules = []string{rules}
		}
		if json.Unmarshal([]byte(targets), &p.targets) != nil {
			p.targets = []protocol.PermissionTarget{{Name: targets}}
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// pendingPermissionBody names everything the proposal asked for and the exact
// permission_apply call the CTO runs if the owner agrees.
func pendingPermissionBody(q askDB, p retiredProposal) string {
	who := sessionLabel(q, p.by)
	if name := strings.TrimSpace(p.name); name != "" && name != who {
		who = name + " (" + who + ")"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Permission proposal %s was waiting for the owner when agent-net removed permission proposals. Nothing was applied.\n", p.id)
	fmt.Fprintf(&b, "Proposed by: %s\nKind: %s\n", who, p.kind)
	if len(p.rules) > 0 {
		b.WriteString("Rules:\n")
		for _, r := range p.rules {
			fmt.Fprintf(&b, "  - %s\n", r)
		}
	}
	b.WriteString("Targets:\n")
	// One call per machine for folder trust, one call for all repos for
	// rules: permission_apply takes one machine for trust.
	var machines []string
	folders := map[string][]string{}
	var repos []string
	for _, t := range p.targets {
		host := firstNonEmpty(t.Hostname, t.NodeID)
		if p.kind == protocol.PermissionKindTrust {
			fmt.Fprintf(&b, "  - %s on %s\n", t.Path, host)
			if _, ok := folders[host]; !ok {
				machines = append(machines, host)
			}
			folders[host] = append(folders[host], t.Path)
			continue
		}
		fmt.Fprintf(&b, "  - %s on %s (%s)\n", t.Name, host, t.Path)
		repos = append(repos, t.Name)
	}
	fmt.Fprintf(&b, "Reason: %s\n", strings.TrimSpace(p.reason))
	b.WriteString("Ask the owner. If he agrees, the CTO applies it on his words with:")
	words := "<quote the owner's words>"
	if p.kind == protocol.PermissionKindTrust {
		for _, host := range machines {
			b.WriteString("\n" + ctoOpCallText(protocol.OpPermissionApply, map[string]any{
				"kind": protocol.PermissionKindTrust, "machine": host, "targets": folders[host], "owner_words": words}))
		}
	} else {
		b.WriteString("\n" + ctoOpCallText(protocol.OpPermissionApply, map[string]any{
			"kind": protocol.PermissionKindRules, "rules": p.rules, "targets": repos, "owner_words": words}))
	}
	return b.String()
}

// noteNameUnsafe matches the runs of characters a note name cannot hold.
var noteNameUnsafe = regexp.MustCompile(`[^a-z0-9._-]+`)

// keepFindingsAsNotes copies each finding into an owner-wide gateway note
// named finding-<id>-<subject kind>-<subject>. Every session reads owner notes,
// as every worker brief read the playbook the findings fed. INSERT OR IGNORE
// on the unique note name makes a replay safe.
func keepFindingsAsNotes(tx *sql.Tx, now time.Time) error {
	if ok, err := hasTable(tx, "findings"); err != nil || !ok {
		return err
	}
	type finding struct {
		id                                              int64
		kind, subject, text, evidence, reporter, status string
		at                                              int64
	}
	rows, err := tx.Query(`SELECT id,subject_kind,subject,finding,evidence,reporter,status,at FROM findings ORDER BY id`)
	if err != nil {
		return err
	}
	var all []finding
	for rows.Next() {
		var f finding
		if err := rows.Scan(&f.id, &f.kind, &f.subject, &f.text, &f.evidence, &f.reporter, &f.status, &f.at); err != nil {
			rows.Close()
			return err
		}
		all = append(all, f)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, f := range all {
		name := fmt.Sprintf("finding-%d-%s-%s", f.id, f.kind, f.subject)
		name = strings.Trim(noteNameUnsafe.ReplaceAllString(strings.ToLower(name), "-"), "-")
		if len(name) > 80 {
			name = strings.TrimRight(name[:80], "-")
		}
		text := fmt.Sprintf("%s\nAbout %s %s. Reported by %s on %s. Status: %s.",
			strings.TrimSpace(f.text), f.kind, f.subject, sessionLabel(tx, f.reporter), time.Unix(f.at, 0).UTC().Format("2006-01-02"), f.status)
		if ev := strings.TrimSpace(f.evidence); ev != "" && ev != "[]" {
			text += "\nEvidence: " + ev
		}
		area := ""
		if f.kind == "project" {
			area = f.subject
		}
		tags, _ := json.Marshal([]string{"finding", f.kind})
		if _, err := tx.Exec(`INSERT OR IGNORE INTO standing_orders(id,text,saved_at,source,tags,pinned,updated_at,kind,area,ref,scope,scope_ref,name,description,used_at,uses)
 VALUES(?,?,?,?,?,0,0,?,?,'',?,'',?,?,?,0)`, protocol.NewID("so"), text, f.at, protocol.StandingSourceAgent, string(tags),
			protocol.StandingKindFact, area, protocol.NoteScopeOwner, name, fmt.Sprintf("Finding about %s %s", f.kind, f.subject), now.Unix()); err != nil {
			return err
		}
	}
	return nil
}

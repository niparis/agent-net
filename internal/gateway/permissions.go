package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/niparis/agent-net/internal/protocol"
)

// Claude Code permission proposals (decision 0127).
//
// An agent proposes exact allow rules for registered repositories, or exact
// folders to trust on one machine. The gateway stores the proposal and opens
// one owner-only ask. The ask names every rule or folder, every target with its
// machine and path, the reason and the proposer. Only an owner answer on that
// ask makes the gateway tell each target machine's daemon to apply. The ask is
// route-fixed to the owner, so the chief of staff never adopts it. No agent can
// apply a proposal. The owner's direct instruction in the CTO chat is the one
// other approval: the CTO applies it with his quoted words, which the proposal
// keeps as the record (decision 0159).

const permissionAskPrefix = "permission:"

// normalizePermissionKind treats an omitted kind as the permission-rule kind.
func normalizePermissionKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "", protocol.PermissionKindRules:
		return protocol.PermissionKindRules
	case protocol.PermissionKindTrust:
		return protocol.PermissionKindTrust
	}
	return ""
}

// cleanPermissionRules trims the rules, refuses empty ones and drops exact
// repeats. It adds no other filtering.
func cleanPermissionRules(rules []string) ([]string, error) {
	out := make([]string, 0, len(rules))
	seen := map[string]bool{}
	for _, rule := range rules {
		rule = strings.TrimSpace(rule)
		if rule == "" {
			return nil, reject("a permission rule is empty")
		}
		if seen[rule] {
			continue
		}
		seen[rule] = true
		out = append(out, rule)
	}
	if len(out) == 0 {
		return nil, reject("give at least one permission rule")
	}
	if raw, err := json.Marshal(out); err != nil {
		return nil, err
	} else if len(raw) > protocol.MaxBodyBytes {
		return nil, reject("permission rules are too long")
	}
	return out, nil
}

// cleanTrustPaths keeps exact absolute folder paths, or paths under ~, and
// drops exact repeats. The daemon that holds the folder checks that it exists
// and applies its own home rule, as the owner's folder trust already does.
func cleanTrustPaths(paths []string) ([]string, error) {
	out := make([]string, 0, len(paths))
	seen := map[string]bool{}
	for _, raw := range paths {
		path := strings.TrimSpace(raw)
		if path == "" {
			return nil, reject("a trust target is empty")
		}
		for _, r := range path {
			if unicode.IsControl(r) {
				return nil, reject("a trust path has a control character")
			}
		}
		if !filepath.IsAbs(path) && !strings.HasPrefix(path, "~/") {
			return nil, reject("folder %q must be absolute or start with ~/", path)
		}
		path = filepath.Clean(path)
		if seen[path] {
			continue
		}
		seen[path] = true
		out = append(out, path)
	}
	if len(out) == 0 {
		return nil, reject("give at least one folder")
	}
	return out, nil
}

// resolvePermissionTargets turns the caller's repo or project names into
// registered machine and path rows. An unknown name is refused by name.
func (s *Store) resolvePermissionTargets(names []string) ([]protocol.PermissionTarget, error) {
	var out []protocol.PermissionTarget
	seen := map[string]bool{}
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			return nil, reject("a permission target is empty")
		}
		rows, err := s.registeredProjects(name)
		if err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			return nil, reject("target %q does not resolve to a registered repo; register it with agent_project_add", name)
		}
		for _, p := range rows {
			key := p.NodeID + "\x00" + p.Path
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, protocol.PermissionTarget{Name: p.Name, NodeID: p.NodeID, Hostname: p.Hostname, Path: p.Path})
		}
	}
	if len(out) == 0 {
		return nil, reject("give at least one target")
	}
	return out, nil
}

// resolveTrustMachine turns the caller's machine name or node ID into one node.
func (s *Store) resolveTrustMachine(machine string) (nodeID, hostname string, err error) {
	nodes, err := s.resolveMachines([]string{strings.TrimSpace(machine)})
	if err != nil {
		return "", "", err
	}
	if len(nodes) != 1 {
		return "", "", reject("machine %q does not name one enrolled machine", machine)
	}
	return nodes[0], s.hostname(nodes[0]), nil
}

// trustTargets makes one target per folder on the chosen machine.
func trustTargets(nodeID, hostname string, paths []string) []protocol.PermissionTarget {
	out := make([]protocol.PermissionTarget, 0, len(paths))
	for _, path := range paths {
		name := filepath.Base(path)
		if name == "." || name == string(filepath.Separator) || name == "~" {
			name = path
		}
		out = append(out, protocol.PermissionTarget{Name: name, NodeID: nodeID, Hostname: hostname, Path: path})
	}
	return out
}

// permissionAskText builds the full owner ask. It names every rule or folder,
// every target with its machine and path, the reason and who proposed it.
func permissionAskText(p protocol.PermissionProposal) string {
	var b strings.Builder
	if p.Kind == protocol.PermissionKindTrust {
		host := ""
		if len(p.Targets) > 0 {
			host = p.Targets[0].Hostname
			if host == "" {
				host = p.Targets[0].NodeID
			}
		}
		fmt.Fprintf(&b, "Trust %d Claude Code folder(s) on %s?\n", len(p.Targets), host)
		if len(p.Targets) > 0 {
			fmt.Fprintf(&b, "Machine: %s (%s)\n", host, p.Targets[0].NodeID)
		}
		b.WriteString("Folders:\n")
		for _, t := range p.Targets {
			fmt.Fprintf(&b, "  - %s\n", t.Path)
		}
		fmt.Fprintf(&b, "Reason: %s\n", p.Reason)
		fmt.Fprintf(&b, "Proposed by: %s (%s)\n", p.ProposedName, p.ProposedBy)
		b.WriteString("After approval, the machine trusts the folder for Claude Code. A session already running there may need a restart, or /hooks opened once.")
		return b.String()
	}
	fmt.Fprintf(&b, "Allow %d Claude Code permission rule(s) in %d repo(s)?\n", len(p.Rules), len(p.Targets))
	b.WriteString("Rules:\n")
	for _, rule := range p.Rules {
		fmt.Fprintf(&b, "  - %s\n", rule)
	}
	b.WriteString("Targets:\n")
	for _, t := range p.Targets {
		host := t.Hostname
		if host == "" {
			host = t.NodeID
		}
		fmt.Fprintf(&b, "  - %s on %s (%s)\n", t.Name, host, t.Path)
	}
	fmt.Fprintf(&b, "Reason: %s\n", p.Reason)
	fmt.Fprintf(&b, "Proposed by: %s (%s)\n", p.ProposedName, p.ProposedBy)
	b.WriteString("After approval, a Claude session already running in that repo may need a restart, or /hooks opened once, to load the new rules.")
	return b.String()
}

// insertPermissionRow stores one proposal row. It writes no file.
func insertPermissionRow(tx *sql.Tx, p protocol.PermissionProposal) error {
	rulesRaw, _ := json.Marshal(p.Rules)
	targetsRaw, _ := json.Marshal(p.Targets)
	_, err := tx.Exec(`INSERT INTO permission_proposals(id,kind,rules,targets,reason,proposed_by,proposed_name,status,created_at,decided_at,decided_by,dedupe_key,owner_words) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.ID, p.Kind, string(rulesRaw), string(targetsRaw), p.Reason, p.ProposedBy, p.ProposedName, p.Status, p.CreatedAt, p.DecidedAt, p.DecidedBy, p.DedupeKey, p.OwnerWords)
	return err
}

// insertPermissionProposal stores the proposal row and opens its owner-only
// ask in the same transaction. It writes no file and changes no setting.
func (s *Store) insertPermissionProposal(tx *sql.Tx, p protocol.PermissionProposal, session, why string) error {
	if err := insertPermissionRow(tx, p); err != nil {
		return err
	}
	about := ""
	if len(p.Targets) > 0 {
		if p.Kind == protocol.PermissionKindTrust {
			about = p.Targets[0].Hostname
		} else {
			about = p.Targets[0].Name
		}
	}
	text := permissionAskText(p)
	item := protocol.InboxItem{
		Kind: protocol.AskKindPermission, SessionID: session, FromKind: protocol.AskFromSession,
		AskerName: p.ProposedName, SourceRef: permissionAskPrefix + p.ID,
		AboutKind: protocol.AskAboutProject, AboutID: about,
		Text: text, Detail: text, Options: []string{"approve", "reject"},
		NextWho: protocol.AskNextOwner, WhyYou: why,
	}
	_, err := s.createInboxItem(tx, item)
	return err
}

// permissionRulesAndTargets checks the rules and resolves the targets of one
// proposal kind. A proposal and the CTO's apply on the owner's words resolve
// them the same way.
func (s *Store) permissionRulesAndTargets(kind string, rawRules, rawTargets []string, machine string) ([]string, []protocol.PermissionTarget, error) {
	switch kind {
	case protocol.PermissionKindRules:
		if strings.TrimSpace(machine) != "" {
			return nil, nil, reject("permission rules take repo names, not a machine")
		}
		rules, err := cleanPermissionRules(rawRules)
		if err != nil {
			return nil, nil, err
		}
		targets, err := s.resolvePermissionTargets(rawTargets)
		if err != nil {
			return nil, nil, err
		}
		return rules, targets, nil
	case protocol.PermissionKindTrust:
		if strings.TrimSpace(machine) == "" {
			return nil, nil, reject("folder trust needs a machine")
		}
		for _, rule := range rawRules {
			if strings.TrimSpace(rule) != "" {
				return nil, nil, reject("folder trust takes no permission rules")
			}
		}
		nodeID, hostname, err := s.resolveTrustMachine(machine)
		if err != nil {
			return nil, nil, err
		}
		paths, err := cleanTrustPaths(rawTargets)
		if err != nil {
			return nil, nil, err
		}
		return nil, trustTargets(nodeID, hostname, paths), nil
	}
	return nil, nil, reject("kind must be permissions or trust")
}

// ProposePermission stores an agent's proposal and opens its owner-only ask.
// It writes no file and changes no setting.
func (s *Store) ProposePermission(frame protocol.PermissionProposeFrame) (protocol.PermissionProposeResult, error) {
	var res protocol.PermissionProposeResult
	session := strings.TrimSpace(frame.SessionID)
	if session == "" {
		return res, reject("a permission proposal needs its session")
	}
	kind := normalizePermissionKind(frame.Kind)
	if kind == "" {
		return res, reject("kind must be permissions or trust")
	}
	reason := strings.TrimSpace(frame.Reason)
	if reason == "" {
		return res, reject("a permission proposal needs a reason")
	}
	if len(reason) > protocol.MaxBodyBytes {
		return res, reject("the reason is too long")
	}
	rules, targets, err := s.permissionRulesAndTargets(kind, frame.Rules, frame.Targets, frame.Machine)
	if err != nil {
		return res, err
	}
	p := protocol.PermissionProposal{
		ID:           protocol.NewID("pp"),
		Kind:         kind,
		Rules:        rules,
		Targets:      targets,
		Reason:       reason,
		ProposedBy:   session,
		ProposedName: sessionLabel(s.db, session),
		Status:       "pending",
		CreatedAt:    s.now().UnixNano(),
	}
	why := "Claude Code permission rules are yours to allow. Nothing is written before you answer."
	if kind == protocol.PermissionKindTrust {
		why = "Claude Code folder trust is yours to grant. Nothing is trusted before you answer."
	}
	tx, err := s.db.Begin()
	if err != nil {
		return res, err
	}
	defer tx.Rollback()
	if err = s.insertPermissionProposal(tx, p, session, why); err != nil {
		return res, err
	}
	if err = tx.Commit(); err != nil {
		return res, err
	}
	res.ID, res.Status, res.Targets = p.ID, "pending", targets
	return res, nil
}

// RaiseTrustAsk opens the gateway's own trust ask after a session start,
// resume or wake answered needs_trust. It is deduped by machine and folder
// while an ask is open. It writes nothing.
func (s *Store) RaiseTrustAsk(session, nodeID, hostname, path, project, reason string) (bool, error) {
	path = strings.TrimSpace(path)
	nodeID = strings.TrimSpace(nodeID)
	if path == "" || !filepath.IsAbs(path) {
		return false, reject("a trust ask needs an absolute folder")
	}
	if nodeID == "" {
		return false, reject("a trust ask needs a machine")
	}
	if strings.TrimSpace(hostname) == "" {
		hostname = s.hostname(nodeID)
	}
	key := nodeID + "\x00" + path
	var open int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM permission_proposals WHERE kind=? AND dedupe_key=? AND status IN ('pending','approved')`,
		protocol.PermissionKindTrust, key).Scan(&open); err != nil {
		return false, err
	}
	if open > 0 {
		return false, nil
	}
	who := strings.TrimSpace(session)
	if who == "" {
		who = gatewayAsker
	}
	name := "agent-net"
	if who != gatewayAsker {
		name = sessionLabel(s.db, who)
	}
	if strings.TrimSpace(reason) == "" {
		reason = fmt.Sprintf("%s needs Claude Code folder trust for %s on %s", name, path, hostname)
	}
	p := protocol.PermissionProposal{
		ID:           protocol.NewID("pp"),
		Kind:         protocol.PermissionKindTrust,
		Targets:      trustTargets(nodeID, hostname, []string{path}),
		Reason:       strings.TrimSpace(reason),
		ProposedBy:   who,
		ProposedName: name,
		Status:       "pending",
		CreatedAt:    s.now().UnixNano(),
		DedupeKey:    key,
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if err := s.insertPermissionProposal(tx, p, who, "Claude Code folder trust is yours to grant. Nothing is trusted before you answer."); err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return false, nil
		}
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// PermissionProposal reads one proposal, including the results of its apply.
func (s *Store) PermissionProposal(id string) (protocol.PermissionProposal, error) {
	var p protocol.PermissionProposal
	var rules, targets, results string
	err := s.db.QueryRow(`SELECT id,kind,rules,targets,reason,proposed_by,proposed_name,status,created_at,decided_at,decided_by,results,desk_log_id,dedupe_key,owner_words
		FROM permission_proposals WHERE id=?`, id).Scan(&p.ID, &p.Kind, &rules, &targets, &p.Reason, &p.ProposedBy, &p.ProposedName,
		&p.Status, &p.CreatedAt, &p.DecidedAt, &p.DecidedBy, &results, &p.DeskLogID, &p.DedupeKey, &p.OwnerWords)
	if err != nil {
		return p, err
	}
	if err = json.Unmarshal([]byte(rules), &p.Rules); err != nil {
		return p, err
	}
	if err = json.Unmarshal([]byte(targets), &p.Targets); err != nil {
		return p, err
	}
	if err = json.Unmarshal([]byte(results), &p.Results); err != nil {
		return p, err
	}
	return p, nil
}

// PermissionProposalForInbox finds the proposal of one permission ask.
func (s *Store) PermissionProposalForInbox(itemID string) (protocol.PermissionProposal, error) {
	var ref string
	if err := s.db.QueryRow(`SELECT source_ref FROM inbox_items WHERE id=?`, itemID).Scan(&ref); err != nil {
		return protocol.PermissionProposal{}, err
	}
	if !strings.HasPrefix(ref, permissionAskPrefix) {
		return protocol.PermissionProposal{}, reject("inbox item is not a permission proposal")
	}
	return s.PermissionProposal(strings.TrimPrefix(ref, permissionAskPrefix))
}

// permissionIDOfInbox reads the proposal ID from a permission ask.
func permissionIDOfInbox(q askDB, itemID string) (string, error) {
	var ref string
	if err := q.QueryRow(`SELECT source_ref FROM inbox_items WHERE id=?`, itemID).Scan(&ref); err != nil {
		return "", err
	}
	if !strings.HasPrefix(ref, permissionAskPrefix) {
		return "", reject("inbox item is not a permission proposal")
	}
	return strings.TrimPrefix(ref, permissionAskPrefix), nil
}

// answerPermissionAsk records the owner's approve or reject. It refuses every
// other actor, the chief and the CTO included, and changes the proposal only
// in the same transaction as the ask.
func (s *Store) answerPermissionAsk(tx *sql.Tx, item protocol.InboxItem, answer, actor string, validate ...bool) (protocol.InboxItem, error) {
	if actor != "owner" {
		return item, reject("only the owner answers a Claude Code permission proposal")
	}
	status := ""
	switch answer {
	case "approve":
		status = "approved"
	case "reject", "refuse":
		status = "rejected"
	default:
		return item, reject("a permission proposal answer must be approve or reject")
	}
	id, err := permissionIDOfInbox(tx, item.ID)
	if err != nil {
		return item, err
	}
	var current string
	if err = tx.QueryRow(`SELECT status FROM permission_proposals WHERE id=?`, id).Scan(&current); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return item, reject("permission proposal is not pending")
		}
		return item, err
	}
	if current != "pending" {
		return item, reject("permission proposal is not pending")
	}
	item.Status = status
	item.Answer = answer
	item.AnsweredAt = s.now().UTC()
	if _, err = tx.Exec(`UPDATE inbox_items SET status=?,answer=?,answered_at=? WHERE id=?`, item.Status, item.Answer, item.AnsweredAt.UnixNano(), item.ID); err != nil {
		return item, err
	}
	if _, err = tx.Exec(`UPDATE permission_proposals SET status=?,decided_at=?,decided_by=? WHERE id=?`, status, item.AnsweredAt.UnixNano(), actor, id); err != nil {
		return item, err
	}
	if len(validate) > 0 && validate[0] {
		return item, nil
	}
	if err = tx.Commit(); err != nil {
		return item, err
	}
	return item, nil
}

// rejectPermissionTx closes a dismissed permission ask and its proposal.
func (s *Store) rejectPermissionTx(tx *sql.Tx, itemID string) error {
	id, err := permissionIDOfInbox(tx, itemID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE permission_proposals SET status='rejected',decided_at=?,decided_by='owner' WHERE id=? AND status='pending'`, s.now().UnixNano(), id)
	return err
}

// ApprovePermissionOnOwnerWords records the owner's direct chat instruction as
// the approval of one permission change (decision 0159). With an id it
// approves a proposal that waits on the Desk and closes its Desk item with an
// answer that names who acted and the owner's words. Without one it stores a
// new, already approved proposal and opens no ask. The owner's words are
// required: they are the record of his approval. It writes no file.
func (s *Store) ApprovePermissionOnOwnerWords(frame protocol.PermissionOwnerApplyFrame) (string, error) {
	req := frame.Request
	words := strings.TrimSpace(req.OwnerWords)
	if words == "" {
		return "", reject("owner_words is required: quote the owner's instruction from this chat")
	}
	by := "owner"
	if frame.By == "cto" {
		by = "cto"
	}
	now := s.now()
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	id := strings.TrimSpace(req.ID)
	if id != "" {
		if strings.TrimSpace(req.Kind) != "" || len(req.Rules) > 0 || len(req.Targets) > 0 || strings.TrimSpace(req.Machine) != "" {
			return "", reject("give id alone to approve a proposal, or kind, rules and targets for a new grant")
		}
		var status string
		if err := tx.QueryRow(`SELECT status FROM permission_proposals WHERE id=?`, id).Scan(&status); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return "", reject("unknown permission proposal %s", id)
			}
			return "", err
		}
		if status != "pending" {
			return "", reject("permission proposal %s is %s, not pending", id, status)
		}
		if _, err := tx.Exec(`UPDATE permission_proposals SET status='approved',decided_at=?,decided_by=?,owner_words=? WHERE id=?`, now.UnixNano(), by, words, id); err != nil {
			return "", err
		}
		who := "the owner"
		if by == "cto" {
			who = "the CTO"
		}
		answer := fmt.Sprintf("approve: %s applied it on the owner's words %q", who, words)
		if _, err := tx.Exec(`UPDATE inbox_items SET status='approved',answer=?,answered_at=? WHERE source_ref=? AND status='open'`,
			answer, now.UTC().UnixNano(), permissionAskPrefix+id); err != nil {
			return "", err
		}
		return id, tx.Commit()
	}
	kind := normalizePermissionKind(req.Kind)
	if kind == "" {
		return "", reject("kind must be permissions or trust")
	}
	rules, targets, err := s.permissionRulesAndTargets(kind, req.Rules, req.Targets, req.Machine)
	if err != nil {
		return "", err
	}
	session := strings.TrimSpace(frame.Session)
	if session == "" {
		session = frame.Owner
	}
	p := protocol.PermissionProposal{
		ID:           protocol.NewID("pp"),
		Kind:         kind,
		Rules:        rules,
		Targets:      targets,
		Reason:       words,
		ProposedBy:   session,
		ProposedName: sessionLabel(s.db, session),
		Status:       "approved",
		CreatedAt:    now.UnixNano(),
		DecidedAt:    now.UnixNano(),
		DecidedBy:    by,
		OwnerWords:   words,
	}
	if err := insertPermissionRow(tx, p); err != nil {
		return "", err
	}
	return p.ID, tx.Commit()
}

// permissionResultLine says what happened to one target, in the owner's words.
func permissionResultLine(r protocol.PermissionApplyResult) string {
	host := r.Target.Hostname
	if host == "" {
		host = r.Target.NodeID
	}
	if r.Kind == protocol.PermissionKindTrust {
		head := fmt.Sprintf("%s on %s: ", r.Target.Path, host)
		switch r.Status {
		case "applied":
			return head + "trusted"
		case "already_present":
			return head + "was already trusted"
		case "removed":
			return head + "trust removed"
		case "absent":
			return head + "no trust to remove"
		case "failed":
			return head + "failed: " + r.Error
		}
		return head + r.Status
	}
	head := fmt.Sprintf("%s on %s (%s): ", r.Target.Name, host, r.Target.Path)
	switch r.Status {
	case "applied":
		return head + fmt.Sprintf("applied, %d new rule(s)", len(r.Added))
	case "already_present":
		return head + "already present"
	case "removed":
		return head + fmt.Sprintf("undone, %d rule(s) removed", len(r.Removed))
	case "absent":
		return head + "nothing to remove"
	case "failed":
		return head + "failed: " + r.Error
	}
	return head + r.Status
}

// RecordPermissionResult stores the per-target outcome of an approval, writes
// the Desk log line with its undo, and keeps the log id for an undo.
func (s *Store) RecordPermissionResult(id string, results []protocol.PermissionApplyResult) error {
	kind, status, undoable := protocol.PermissionKindRules, "applied", false
	for _, r := range results {
		if r.Kind == protocol.PermissionKindTrust {
			kind = protocol.PermissionKindTrust
		}
		if r.Status == "failed" {
			status = "failed"
		}
		if r.Kind == protocol.PermissionKindTrust {
			if r.TrustAdded {
				undoable = true
			}
		} else if len(r.Added) > 0 {
			undoable = true
		}
	}
	if status == "failed" && undoable {
		status = "partial"
	}
	resultsRaw, _ := json.Marshal(results)
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE permission_proposals SET status=?,results=? WHERE id=?`, status, string(resultsRaw), id); err != nil {
		return err
	}
	var decidedBy, words string
	if err = tx.QueryRow(`SELECT decided_by,owner_words FROM permission_proposals WHERE id=?`, id).Scan(&decidedBy, &words); err != nil {
		return err
	}
	var detail strings.Builder
	var names []string
	for _, r := range results {
		detail.WriteString(permissionResultLine(r))
		detail.WriteString("\n")
		if r.Kind == protocol.PermissionKindTrust {
			names = append(names, r.Target.Path)
		} else {
			names = append(names, r.Target.Name)
		}
	}
	text := fmt.Sprintf("Allowed Claude Code permissions in %s", strings.Join(names, ", "))
	if kind == protocol.PermissionKindTrust {
		text = fmt.Sprintf("Trusted Claude Code folders: %s", strings.Join(names, ", "))
	}
	// A grant the CTO applied on the owner's words is the CTO's log line, and
	// it quotes his words.
	source := "gateway"
	if decidedBy == "cto" {
		source = "cto"
	}
	if words != "" {
		fmt.Fprintf(&detail, "Owner's words: %q\n", words)
	}
	var options []string
	offer := status != "failed"
	if kind == protocol.PermissionKindTrust {
		offer = undoable
	}
	if offer {
		raw, _ := json.Marshal(map[string]string{"id": id})
		options = []string{protocol.OpPermissionUndo, string(raw)}
	}
	if err = s.deskLog(tx, source, "permission", id, text, strings.TrimRight(detail.String(), "\n"), options); err != nil {
		return err
	}
	var logID int64
	if err = tx.QueryRow(`SELECT id FROM desk_log ORDER BY id DESC LIMIT 1`).Scan(&logID); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE permission_proposals SET desk_log_id=? WHERE id=?`, logID, id); err != nil {
		return err
	}
	return tx.Commit()
}

// MarkPermissionUndone records the result of an undo and settles its log line.
func (s *Store) MarkPermissionUndone(id string, results []protocol.PermissionApplyResult) error {
	raw, _ := json.Marshal(results)
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var logID int64
	if err = tx.QueryRow(`SELECT desk_log_id FROM permission_proposals WHERE id=?`, id).Scan(&logID); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE permission_proposals SET status='undone',results=? WHERE id=?`, string(raw), id); err != nil {
		return err
	}
	if logID != 0 {
		if _, err = tx.Exec(`UPDATE desk_log SET options='[]',text='Undone: '||text WHERE id=? AND options<>'[]'`, logID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// PermissionApplyTimeout is how long the gateway waits for one machine to
// write or undo one target.
const PermissionApplyTimeout = 20 * time.Second

// applyPermissionProposal tells each target machine to merge the rules or
// trust the folder, and records what happened. It runs only after the owner
// approved the ask.
func (g *Gateway) applyPermissionProposal(ctx context.Context, itemID string) ([]protocol.PermissionApplyResult, error) {
	p, err := g.store.PermissionProposalForInbox(itemID)
	if err != nil {
		return nil, err
	}
	return g.applyApprovedPermission(ctx, p)
}

// ownerApplyPermission is the owner operation permission_apply: it records the
// owner's words as the approval, applies on each target machine, and returns
// the stored proposal with its per-target results (decision 0159).
func (g *Gateway) ownerApplyPermission(ctx context.Context, frame protocol.PermissionOwnerApplyFrame) (protocol.PermissionProposal, error) {
	id, err := g.store.ApprovePermissionOnOwnerWords(frame)
	if err != nil {
		return protocol.PermissionProposal{}, err
	}
	p, err := g.store.PermissionProposal(id)
	if err != nil {
		return p, err
	}
	if _, err := g.applyApprovedPermission(ctx, p); err != nil {
		return p, err
	}
	return g.store.PermissionProposal(id)
}

// applyApprovedPermission tells each target machine to apply one approved
// proposal and records what happened. The approval is the owner's Desk answer,
// or the owner's quoted chat words.
func (g *Gateway) applyApprovedPermission(ctx context.Context, p protocol.PermissionProposal) ([]protocol.PermissionApplyResult, error) {
	if p.Status != "approved" || (p.DecidedBy != "owner" && p.OwnerWords == "") {
		return nil, reject("permission proposal is not approved by the owner")
	}
	results := make([]protocol.PermissionApplyResult, 0, len(p.Targets))
	for _, target := range p.Targets {
		res := protocol.PermissionApplyResult{Target: target, Kind: p.Kind, Status: "failed"}
		nc := g.conn(target.NodeID)
		if nc == nil {
			res.Error = "machine is offline"
			results = append(results, res)
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, PermissionApplyTimeout)
		raw, err := nc.call(cctx, protocol.FramePermissionApply, protocol.PermissionApplyFrame{
			ProposalID: p.ID, Kind: p.Kind, Target: target, Rules: p.Rules,
		})
		cancel()
		if err != nil {
			res.Error = err.Error()
			results = append(results, res)
			continue
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			res = protocol.PermissionApplyResult{Target: target, Kind: p.Kind, Status: "failed", Error: "the machine answered without a result"}
			results = append(results, res)
			continue
		}
		res.Target, res.Kind = target, p.Kind
		results = append(results, res)
	}
	if err := g.store.RecordPermissionResult(p.ID, results); err != nil {
		return results, err
	}
	for _, r := range results {
		g.logLine("permission.apply", "proposal", p.ID, "kind", p.Kind, "by", p.DecidedBy, "node", r.Target.NodeID, "target", r.Target.Name,
			"path", r.Target.Path, "status", r.Status, "added", strings.Join(r.Added, ","), "trust_added", r.TrustAdded, "error", r.Error)
	}
	return results, nil
}

// undoPermissionProposal removes exactly the rules or trust the approval added
// and marks the result. It runs only for the owner.
func (g *Gateway) undoPermissionProposal(ctx context.Context, id string) (protocol.PermissionUndoResult, error) {
	p, err := g.store.PermissionProposal(id)
	if err != nil {
		return protocol.PermissionUndoResult{}, err
	}
	if p.Status == "undone" {
		return protocol.PermissionUndoResult{ID: id, Status: "undone", Results: p.Results}, nil
	}
	if p.Status != "applied" && p.Status != "partial" && p.Status != "failed" {
		return protocol.PermissionUndoResult{}, reject("permission proposal has not been applied")
	}
	var results []protocol.PermissionApplyResult
	for _, prior := range p.Results {
		if prior.Kind == protocol.PermissionKindTrust {
			if !prior.TrustAdded {
				continue
			}
		} else if len(prior.Added) == 0 {
			continue
		}
		res := protocol.PermissionApplyResult{Target: prior.Target, Kind: prior.Kind, Status: "failed"}
		nc := g.conn(prior.Target.NodeID)
		if nc == nil {
			res.Error = "machine is offline"
			results = append(results, res)
			continue
		}
		frame := protocol.PermissionRevokeFrame{ProposalID: id, Kind: prior.Kind, Path: prior.Target.Path}
		if prior.Kind != protocol.PermissionKindTrust {
			frame.Rules = prior.Added
		}
		cctx, cancel := context.WithTimeout(ctx, PermissionApplyTimeout)
		raw, err := nc.call(cctx, protocol.FramePermissionRevoke, frame)
		cancel()
		if err != nil {
			res.Error = err.Error()
			results = append(results, res)
			continue
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			res = protocol.PermissionApplyResult{Target: prior.Target, Kind: prior.Kind, Status: "failed", Error: "the machine answered without a result"}
		}
		res.Target, res.Kind = prior.Target, prior.Kind
		results = append(results, res)
	}
	for _, r := range results {
		if r.Status == "failed" {
			return protocol.PermissionUndoResult{ID: id, Status: "failed", Results: results}, reject("could not undo on %s: %s", r.Target.Name, r.Error)
		}
	}
	if err := g.store.MarkPermissionUndone(id, results); err != nil {
		return protocol.PermissionUndoResult{ID: id, Status: "failed", Results: results}, err
	}
	g.logLine("permission.undo", "proposal", id, "removed", len(results))
	return protocol.PermissionUndoResult{ID: id, Status: "undone", Results: results}, nil
}

// raiseTrustAsk is the gateway's own ask after a start, resume or wake answered
// needs_trust. The reason names the session, the folder and the machine.
func (g *Gateway) raiseTrustAsk(session, nodeID, hostname, path, project, requester string) {
	who := strings.TrimSpace(session)
	if who == "" {
		who = strings.TrimSpace(requester)
	}
	if who == "" {
		who = gatewayAsker
	}
	label := "agent-net"
	if who != gatewayAsker {
		label = sessionLabel(g.store.db, who)
	}
	if strings.TrimSpace(hostname) == "" {
		hostname = g.store.hostname(nodeID)
	}
	reason := fmt.Sprintf("%s needs Claude Code folder trust for %s on %s", label, path, hostname)
	if strings.TrimSpace(project) != "" {
		reason += " (project " + project + ")"
	}
	created, err := g.store.RaiseTrustAsk(who, nodeID, hostname, path, project, reason)
	if err != nil {
		g.logLine("permission.trust_ask.failed", "node", nodeID, "path", path, "error", err.Error())
		return
	}
	if created {
		g.logLine("permission.trust_ask", "node", nodeID, "path", path, "session", who)
	}
}

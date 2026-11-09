package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Claude Code permission proposals (decision 0126).
//
// An agent proposes exact allow rules for registered repositories. The gateway
// stores the proposal and opens one owner-only ask. The ask names every rule,
// every target with its machine and path, the reason and the proposer. Only an
// owner answer on that ask makes the gateway tell each target machine's daemon
// to write <repo>/.claude/settings.local.json. The ask is route-fixed to the
// owner, so the chief of staff never adopts it. No agent, the CTO included,
// can apply a proposal.

const permissionAskPrefix = "permission:"

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

// permissionAskText builds the full owner ask. It names every rule, every
// target with its repo, machine and path, the reason and who proposed it.
func permissionAskText(p protocol.PermissionProposal) string {
	var b strings.Builder
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

// ProposePermission stores an agent's proposal and opens its owner-only ask.
// It writes no file and changes no setting.
func (s *Store) ProposePermission(frame protocol.PermissionProposeFrame) (protocol.PermissionProposeResult, error) {
	var res protocol.PermissionProposeResult
	session := strings.TrimSpace(frame.SessionID)
	if session == "" {
		return res, reject("a permission proposal needs its session")
	}
	rules, err := cleanPermissionRules(frame.Rules)
	if err != nil {
		return res, err
	}
	reason := strings.TrimSpace(frame.Reason)
	if reason == "" {
		return res, reject("a permission proposal needs a reason")
	}
	if len(reason) > protocol.MaxBodyBytes {
		return res, reject("the reason is too long")
	}
	targets, err := s.resolvePermissionTargets(frame.Targets)
	if err != nil {
		return res, err
	}
	p := protocol.PermissionProposal{
		ID:           protocol.NewID("pp"),
		Rules:        rules,
		Targets:      targets,
		Reason:       reason,
		ProposedBy:   session,
		ProposedName: sessionLabel(s.db, session),
		Status:       "pending",
		CreatedAt:    s.now().UnixNano(),
	}
	rulesRaw, _ := json.Marshal(rules)
	targetsRaw, _ := json.Marshal(targets)
	tx, err := s.db.Begin()
	if err != nil {
		return res, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO permission_proposals(id,rules,targets,reason,proposed_by,proposed_name,status,created_at) VALUES(?,?,?,?,?,?,?,?)`,
		p.ID, string(rulesRaw), string(targetsRaw), p.Reason, p.ProposedBy, p.ProposedName, p.Status, p.CreatedAt); err != nil {
		return res, err
	}
	text := permissionAskText(p)
	item := protocol.InboxItem{
		Kind: protocol.AskKindPermission, SessionID: session, FromKind: protocol.AskFromSession,
		AskerName: p.ProposedName, SourceRef: permissionAskPrefix + p.ID,
		AboutKind: protocol.AskAboutProject, AboutID: targets[0].Name,
		Text: text, Detail: text, Options: []string{"approve", "reject"},
		NextWho: protocol.AskNextOwner,
		WhyYou:  "Claude Code permission rules are yours to allow. Nothing is written before you answer.",
	}
	if _, err = s.createInboxItem(tx, item); err != nil {
		return res, err
	}
	if err = tx.Commit(); err != nil {
		return res, err
	}
	res.ID, res.Status, res.Targets = p.ID, "pending", targets
	return res, nil
}

// PermissionProposal reads one proposal, including the results of its apply.
func (s *Store) PermissionProposal(id string) (protocol.PermissionProposal, error) {
	var p protocol.PermissionProposal
	var rules, targets, results string
	err := s.db.QueryRow(`SELECT id,rules,targets,reason,proposed_by,proposed_name,status,created_at,decided_at,decided_by,results,desk_log_id
		FROM permission_proposals WHERE id=?`, id).Scan(&p.ID, &rules, &targets, &p.Reason, &p.ProposedBy, &p.ProposedName,
		&p.Status, &p.CreatedAt, &p.DecidedAt, &p.DecidedBy, &results, &p.DeskLogID)
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

// permissionResultLine says what happened to one target, in the owner's words.
func permissionResultLine(r protocol.PermissionApplyResult) string {
	host := r.Target.Hostname
	if host == "" {
		host = r.Target.NodeID
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
	status, added := "applied", false
	for _, r := range results {
		if r.Status == "failed" {
			status = "failed"
		}
		if len(r.Added) > 0 {
			added = true
		}
	}
	if status == "failed" && added {
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
	var detail strings.Builder
	var names []string
	for _, r := range results {
		line := permissionResultLine(r)
		detail.WriteString(line)
		detail.WriteString("\n")
		names = append(names, r.Target.Name)
	}
	text := fmt.Sprintf("Allowed Claude Code permissions in %s", strings.Join(names, ", "))
	var options []string
	if status != "failed" {
		raw, _ := json.Marshal(map[string]string{"id": id})
		options = []string{protocol.OpPermissionUndo, string(raw)}
	}
	if err = s.deskLog(tx, "gateway", "permission", id, text, strings.TrimRight(detail.String(), "\n"), options); err != nil {
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
// write or undo one repository.
const PermissionApplyTimeout = 20 * time.Second

// applyPermissionProposal tells each target machine to merge the rules and
// records what happened. It runs only after the owner approved the ask.
func (g *Gateway) applyPermissionProposal(ctx context.Context, itemID string) ([]protocol.PermissionApplyResult, error) {
	p, err := g.store.PermissionProposalForInbox(itemID)
	if err != nil {
		return nil, err
	}
	if p.Status != "approved" || p.DecidedBy != "owner" {
		return nil, reject("permission proposal is not approved by the owner")
	}
	results := make([]protocol.PermissionApplyResult, 0, len(p.Targets))
	for _, target := range p.Targets {
		res := protocol.PermissionApplyResult{Target: target, Status: "failed"}
		nc := g.conn(target.NodeID)
		if nc == nil {
			res.Error = "machine is offline"
			results = append(results, res)
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, PermissionApplyTimeout)
		raw, err := nc.call(cctx, protocol.FramePermissionApply, protocol.PermissionApplyFrame{
			ProposalID: p.ID, Target: target, Rules: p.Rules,
		})
		cancel()
		if err != nil {
			res.Error = err.Error()
			results = append(results, res)
			continue
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			res = protocol.PermissionApplyResult{Target: target, Status: "failed", Error: "the machine answered without a result"}
			results = append(results, res)
			continue
		}
		res.Target = target
		results = append(results, res)
	}
	if err := g.store.RecordPermissionResult(p.ID, results); err != nil {
		return results, err
	}
	for _, r := range results {
		g.logLine("permission.apply", "proposal", p.ID, "node", r.Target.NodeID, "repo", r.Target.Name,
			"path", r.Target.Path, "status", r.Status, "added", strings.Join(r.Added, ","), "error", r.Error)
	}
	return results, nil
}

// undoPermissionProposal removes exactly the rules the approval added and
// marks the result. It runs only for the owner.
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
		if len(prior.Added) == 0 {
			continue
		}
		res := protocol.PermissionApplyResult{Target: prior.Target, Status: "failed"}
		nc := g.conn(prior.Target.NodeID)
		if nc == nil {
			res.Error = "machine is offline"
			results = append(results, res)
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, PermissionApplyTimeout)
		raw, err := nc.call(cctx, protocol.FramePermissionRevoke, protocol.PermissionRevokeFrame{
			ProposalID: id, Path: prior.Target.Path, Rules: prior.Added,
		})
		cancel()
		if err != nil {
			res.Error = err.Error()
			results = append(results, res)
			continue
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			res = protocol.PermissionApplyResult{Target: prior.Target, Status: "failed", Error: "the machine answered without a result"}
		}
		res.Target = prior.Target
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

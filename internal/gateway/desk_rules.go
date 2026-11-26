package gateway

import (
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/niparis/agent-net/internal/protocol"
)

// Answers with a scope become standing rules (decision 0150). A rule holds for
// one area (a repo, a team or the network) and one action, and applies only
// when both match an ask exactly. Every application cites the rule id.

// askRepoTeam is the repo and the team an ask belongs to, for matching a
// rule's area. Either can be empty.
func (s *Store) askRepoTeam(q askDB, id string) (repo, team string, err error) {
	var session, aboutKind, aboutID string
	if err = q.QueryRow(`SELECT session_id,about_kind,about_id FROM inbox_items WHERE id=?`, id).Scan(&session, &aboutKind, &aboutID); err != nil {
		return "", "", err
	}
	switch aboutKind {
	case protocol.AskAboutChange:
		_ = q.QueryRow(`SELECT project FROM delivery_changes WHERE id=?`, aboutID).Scan(&repo)
	case protocol.AskAboutStep:
		if changeID, _, _, _, ok := parseStepAboutID(aboutID); ok {
			_ = q.QueryRow(`SELECT project FROM delivery_changes WHERE id=?`, changeID).Scan(&repo)
		}
	case protocol.AskAboutProject, "repo":
		repo = aboutID
	case "team":
		team = aboutID
	}
	if repo == "" && session != "" {
		_ = q.QueryRow(`SELECT COALESCE(NULLIF(s.repo_project,''),NULLIF(s.announced_project,''),s.repo_id,'')
			FROM sessions s WHERE s.id=?`, session).Scan(&repo)
	}
	if team == "" && session != "" {
		_ = q.QueryRow(`SELECT t.name FROM teams t JOIN sessions s ON s.id=?
			WHERE t.active=1 AND ((s.repo_id<>'' AND EXISTS(SELECT 1 FROM repos r WHERE r.id=s.repo_id AND r.team_id=t.id)) OR (s.agent_id<>'' AND EXISTS(SELECT 1 FROM agents a WHERE a.id=s.agent_id AND a.team_id=t.id))) LIMIT 1`,
			session).Scan(&team)
	}
	return repo, team, nil
}

// ruleApplies checks that a standing rule covers an ask: the rule exists, its
// area and action match exactly, and a network rule never covers an ask in
// one of the owner's own categories.
func (s *Store) ruleApplies(q askDB, ask, category, ruleID, action string) error {
	var scope protocol.RuleScope
	err := q.QueryRow(`SELECT area,action,category FROM standing_orders WHERE id=? AND kind=?`, ruleID, protocol.StandingKindRule).
		Scan(&scope.Area, &scope.Action, &scope.Category)
	if errors.Is(err, sql.ErrNoRows) {
		return reject("unknown rule %q: it may have been undone", ruleID)
	}
	if err != nil {
		return err
	}
	repo, team, err := s.askRepoTeam(q, ask)
	if err != nil {
		return err
	}
	if !scope.Matches(repo, team, action) {
		return reject("rule %s does not cover ask %s: the rule holds for %q in %s; the ask is %q in repo %q, team %q", ruleID, ask, scope.Action, scope.Area, protocol.NormalizeRuleAction(action), repo, team)
	}
	if scope.Normalize().Area == protocol.RuleAreaNetwork && protocol.OwnerOnlyCategory(category) {
		return reject("rule %s holds for the network; a %s ask needs a rule that names its repo or team", ruleID, category)
	}
	return nil
}

// addRule stores a judgment and its Desk log line with the undo that removes
// it, in its own transaction.
func (s *Store) addRule(q protocol.JudgmentAddRequest, source string) (protocol.StandingOrder, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return protocol.StandingOrder{}, err
	}
	defer tx.Rollback()
	o, err := s.addRuleTx(tx, q, source)
	if err != nil {
		return o, err
	}
	return o, tx.Commit()
}

// addRuleTx stores a judgment and its Desk log line in the caller's
// transaction.
func (s *Store) addRuleTx(tx *sql.Tx, q protocol.JudgmentAddRequest, source string) (protocol.StandingOrder, error) {
	o, err := addJudgment(tx, s.now(), q)
	if err != nil {
		return o, reject("%v", err)
	}
	undo, err := json.Marshal(protocol.StandingOrdersRequest{Action: protocol.StandingActionRemove, ID: o.ID})
	if err != nil {
		return o, err
	}
	line := "Owner decision " + o.ID + ": " + askFirstLine(o.Text, 160)
	if o.Kind == protocol.StandingKindRule {
		line = "Standing rule " + o.ID + " (" + o.Action + " in " + o.Area + "): " + askFirstLine(o.Text, 160)
	}
	return o, s.deskLog(tx, source, "", "", line, "", []string{protocol.OpStandingOrders, string(undo)})
}

// RecordJudgment records a decision the owner made in a live chat with the
// chief of staff or the CTO (agent_owner_judgment_record). Only those two
// sessions, on their own machine, may record. A scope makes it a rule.
func (s *Store) RecordJudgment(node string, q protocol.JudgmentRecordRequest) (protocol.StandingOrder, error) {
	if err := q.Validate(); err != nil {
		return protocol.StandingOrder{}, reject("%v", err)
	}
	var harness, sessionNode string
	err := s.db.QueryRow(`SELECT harness,node_id FROM sessions WHERE id=? AND archived_at=0`, q.SessionID).Scan(&harness, &sessionNode)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return protocol.StandingOrder{}, err
	}
	chief, err := chiefSession(s.db)
	if err != nil {
		return protocol.StandingOrder{}, err
	}
	source := ""
	switch {
	case sessionNode == "" || sessionNode != node:
	case harness == "cto":
		source = "cto"
	case chief != "" && chief == q.SessionID:
		source = "chief"
	}
	if source == "" {
		return protocol.StandingOrder{}, reject("only the chief of staff or the CTO records the owner's decisions")
	}
	add := protocol.JudgmentAddRequest{Kind: protocol.StandingKindAnswer, Text: q.Text, Ref: "chat:" + q.SessionID, Source: protocol.StandingSourceOwner}
	if q.Area != "" || q.Action != "" {
		add.Kind, add.Area, add.Action, add.Category = protocol.StandingKindRule, q.Area, q.Action, q.Category
	}
	return s.addRule(add, source)
}

// joinedNodes lists the machines of the askers that joined an item.
func (s *Store) joinedNodes(id string) ([]string, error) {
	askers, err := joinedAskers(s.db, id)
	if err != nil || len(askers) == 0 {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, sid := range askers {
		var node string
		if err := s.db.QueryRow(`SELECT node_id FROM sessions WHERE id=?`, sid).Scan(&node); err == nil && node != "" && !seen[node] {
			seen[node] = true
			out = append(out, node)
		}
	}
	return out, nil
}

package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

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
	case protocol.AskAboutProject, "repo":
		repo = aboutID
	case "team":
		team = aboutID
	}
	if repo == "" {
		_ = q.QueryRow(`SELECT c.project FROM delivery_step_inbox d JOIN delivery_changes c ON c.id=d.change_id WHERE d.item_id=?`, id).Scan(&repo)
	}
	if repo == "" && session != "" {
		_ = q.QueryRow(`SELECT COALESCE(NULLIF(s.repo_project,''),NULLIF(p.project,''),s.repo_id,'')
			FROM sessions s LEFT JOIN profiles p ON p.session_id=s.id WHERE s.id=?`, session).Scan(&repo)
	}
	if team == "" && session != "" {
		_ = q.QueryRow(`SELECT t.name FROM teams t JOIN team_members tm ON tm.team_id=t.id JOIN sessions s ON s.id=?
			WHERE t.active=1 AND ((tm.member_kind='repo' AND tm.member_id=s.repo_id AND s.repo_id<>'') OR (tm.member_kind='agent' AND tm.member_id=s.agent_id AND s.agent_id<>'')) LIMIT 1`,
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

// AnswerInboxScoped records an answer. A scope (area and action) also makes
// the answer a standing rule, shown on the Desk with its undo. The scope is
// checked before the answer is stored, so a refused scope answers nothing.
func (s *Store) AnswerInboxScoped(id, answer, actor string, scope protocol.RuleScope) (protocol.InboxItem, error) {
	if scope.Area == "" && scope.Action == "" {
		return s.AnswerInboxAs(id, answer, actor)
	}
	var category string
	err := s.db.QueryRow(`SELECT category FROM inbox_items WHERE id=?`, id).Scan(&category)
	if errors.Is(err, sql.ErrNoRows) {
		return protocol.InboxItem{}, reject("inbox item not found")
	}
	if err != nil {
		return protocol.InboxItem{}, err
	}
	if scope.Category == "" {
		scope.Category = category
	}
	scope = scope.Normalize()
	if err := scope.Validate(); err != nil {
		return protocol.InboxItem{}, reject("%v", err)
	}
	item, err := s.AnswerInboxAs(id, answer, actor)
	if err != nil {
		return item, err
	}
	text := fmt.Sprintf("Always %s for %s in %s. From ask %s: %s", strings.TrimSpace(answer), scope.Action, scope.Area, id, askFirstLine(item.Text, 120))
	_, err = s.addRule(protocol.JudgmentAddRequest{Kind: protocol.StandingKindRule, Text: text, Ref: id,
		Area: scope.Area, Action: scope.Action, Category: scope.Category}, deskSource(actor))
	return item, err
}

// addRule stores a judgment and its Desk log line with the undo that removes
// it.
func (s *Store) addRule(q protocol.JudgmentAddRequest, source string) (protocol.StandingOrder, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return protocol.StandingOrder{}, err
	}
	defer tx.Rollback()
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
	if err := s.deskLog(tx, source, "", "", line, "", []string{protocol.OpStandingOrders, string(undo)}); err != nil {
		return o, err
	}
	return o, tx.Commit()
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
	rows, err := s.db.Query(`SELECT DISTINCT x.node_id FROM inbox_items j JOIN sessions x ON x.id=j.session_id WHERE j.dup_of=?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var node string
		if err := rows.Scan(&node); err != nil {
			return nil, err
		}
		out = append(out, node)
	}
	return out, rows.Err()
}

// discussAsk is the owner's "discuss" on a Desk item (decision 0150). For a CTO
// item it changes nothing: the owner opens his CTO chat, and CTO turns start
// only from him. For a chief item it stores a message to the chief that names
// the item; the caller dispatches it and wakes the chief.
func (s *Store) discussAsk(id string) (protocol.AskDiscussResult, protocol.Message, error) {
	var out protocol.AskDiscussResult
	var none protocol.Message
	var owner, text, status string
	err := s.db.QueryRow(`SELECT owner,text,status FROM inbox_items WHERE id=?`, id).Scan(&owner, &text, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return out, none, reject("unknown ask %q", id)
	}
	if err != nil {
		return out, none, err
	}
	if owner == protocol.DeskOwnerCTO {
		return protocol.AskDiscussResult{Open: protocol.DeskOwnerCTO}, none, nil
	}
	chief, err := chiefSession(s.db)
	if err != nil {
		return out, none, err
	}
	if chief == "" {
		return out, none, reject("no chief of staff is set: open the asking session instead")
	}
	body := fmt.Sprintf("The owner wants to discuss ask %s with you now, in your session: %s\nGive him what you verified, your recommendation and the options.", id, strings.TrimSpace(text))
	m, err := s.storeAgentNetMessage(s.db, chief, body, id, s.now())
	if err != nil {
		return out, none, err
	}
	return protocol.AskDiscussResult{Open: protocol.DeskOwnerChief, Session: chief}, m, nil
}

// discussAsk dispatches the chief's message and wakes the chief when it is
// offline: the owner asked for it, so the wake rules of a peer message do not
// apply.
func (g *Gateway) discussAsk(ctx context.Context, id string) (protocol.AskDiscussResult, error) {
	res, m, err := g.store.discussAsk(id)
	if err != nil || m.ID == "" {
		return res, err
	}
	g.dispatch(ctx, m)
	if a, ok, aerr := g.store.Agent(res.Session); aerr == nil && ok && !a.Online {
		res.Woken = g.wakeSessionAs(ctx, a, false, true)
	}
	return res, nil
}

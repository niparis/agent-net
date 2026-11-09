package gateway

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Charter drafts reach the Desk as one ask for each node, not one for each
// field. A node is a repo and its agent of the same name. A team has its own
// ask. The ask holds every pending field in its detail; accept and reject
// apply to all of them, and "accept repo:purpose" decides one field.
//
// A node that is a member of a team with asks_to=lead sends the ask to the team
// lead instead of the owner (decision 0100), through RedirectOwnerAsk.

// draftGroupRefPrefix starts the source key of the ask of one draft group:
// drafts:node:<scope> or drafts:team:<scope>.
const draftGroupRefPrefix = "drafts:"

// legacyDraftRefPrefix starts the source key of the old ask of one field.
const legacyDraftRefPrefix = "draft:"

// draftGroup is the pending drafts of one node, or of one team.
type draftGroup struct {
	key    string // node:<scope> or team:<scope>
	scope  string
	team   bool
	name   string
	drafts []protocol.CharterDraft
}

// draftSettle is how long the fields of a node must stay unchanged before the
// request goes to the team lead.
const draftSettle = 2 * time.Minute

// newest is the time of the latest draft of the group, in nanoseconds.
func (g draftGroup) newest() int64 {
	var at int64
	for _, d := range g.drafts {
		if d.CreatedAt > at {
			at = d.CreatedAt
		}
	}
	return at
}

func (g draftGroup) ref() string { return draftGroupRefPrefix + g.key }

func draftGroupKey(d protocol.CharterDraft) string {
	if d.Level == "team" {
		return "team:" + d.Scope
	}
	return "node:" + d.Scope
}

// groupDrafts groups pending drafts, in the order of their first draft.
func groupDrafts(drafts []protocol.CharterDraft) []draftGroup {
	var out []draftGroup
	index := map[string]int{}
	for _, d := range drafts {
		key := draftGroupKey(d)
		i, ok := index[key]
		if !ok {
			i = len(out)
			index[key] = i
			out = append(out, draftGroup{key: key, scope: d.Scope, team: d.Level == "team", name: d.Name})
		}
		out[i].drafts = append(out[i].drafts, d)
		// The agent's display name is the name the owner knows.
		if d.Level == "agent" && d.Name != "" {
			out[i].name = d.Name
		}
	}
	return out
}

func draftFieldLabel(d protocol.CharterDraft) string { return d.Level + ":" + d.Attr }

// draftValueLine makes a value fit one line of the detail.
func draftValueLine(v string) string {
	v = strings.Join(strings.Fields(strings.ReplaceAll(v, "\n", " ; ")), " ")
	if r := []rune(v); len(r) > 600 {
		v = string(r[:600]) + "..."
	}
	return v
}

func (g draftGroup) text() string {
	if len(g.drafts) == 1 {
		return fmt.Sprintf("Charter draft for %s: 1 field", g.name)
	}
	return fmt.Sprintf("Charter draft for %s: %d fields", g.name, len(g.drafts))
}

// detail lists each field with its proposed value, one line each. Each line is
// stable, so the lead is not asked again for a field it has already seen.
func (g draftGroup) detail() string {
	var b strings.Builder
	sources := map[string]bool{}
	var names []string
	for _, d := range g.drafts {
		if !sources[d.Source] {
			sources[d.Source] = true
			names = append(names, d.Source)
		}
	}
	b.WriteString("Drafted from " + strings.Join(names, ", ") + ":\n")
	for _, d := range g.drafts {
		fmt.Fprintf(&b, "- %s %s = %s\n", d.Level, d.Attr, draftValueLine(d.Value))
	}
	first := draftFieldLabel(g.drafts[0])
	fmt.Fprintf(&b, "Accept or reject decides every field. To decide one field, answer \"accept %s\" or \"reject %s\".", first, first)
	return b.String()
}

// fieldLines are the lines of the detail that name a field and its value.
func (g draftGroup) fieldLines() []string {
	var out []string
	for _, d := range g.drafts {
		out = append(out, fmt.Sprintf("- %s %s = %s", d.Level, d.Attr, draftValueLine(d.Value)))
	}
	return out
}

// reconcileDraftAsks opens the one ask of each draft group, sends it to the
// team lead when the node's team asks to the lead, and closes the asks that no
// longer have drafts. The old asks of one field close as "grouped" once the
// grouped ask exists, so no draft is left without an ask.
func (s *Store) reconcileDraftAsks(drafts []protocol.CharterDraft) error {
	type openRow struct{ id, ref, about string }
	rows, err := s.db.Query(`SELECT id,source_ref,about_id FROM inbox_items WHERE status='open' AND kind='draft'`)
	if err != nil {
		return err
	}
	var open []openRow
	ownerOpen, leadOpen := map[string]bool{}, map[string]bool{}
	for rows.Next() {
		var r openRow
		if err := rows.Scan(&r.id, &r.ref, &r.about); err != nil {
			rows.Close()
			return err
		}
		open = append(open, r)
		switch {
		case strings.HasPrefix(r.ref, draftGroupRefPrefix):
			ownerOpen[r.ref] = true
		case strings.HasPrefix(r.ref, leadAskRefPrefix):
			leadOpen[r.about] = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	groups := groupDrafts(drafts)
	for _, g := range groups {
		item := protocol.InboxItem{
			Kind: protocol.AskKindDraft, SessionID: gatewayAsker, FromKind: protocol.AskFromGateway, Node: g.scope, AskerName: "charter draft",
			SourceRef: g.ref(), AboutKind: protocol.AskAboutProject, AboutID: g.scope,
			Text: g.text(), Detail: g.detail(), Options: []string{"accept", "reject"},
		}
		if ownerOpen[g.ref()] {
			// Refresh the text and the field list.
			if _, err := s.createInboxItem(s.db, item); err != nil {
				return err
			}
			continue
		}
		if !g.team {
			if leadOpen[g.scope] {
				continue
			}
			seen, err := s.leadSawDrafts(g)
			if err != nil {
				return err
			}
			if seen {
				continue
			}
			prefer := g.drafts[len(g.drafts)-1].DraftedBy
			routed, err := s.nodeRoutesToLead(g.scope, prefer)
			if err != nil {
				return err
			}
			// A session submits its fields one after another, and the lead gets
			// one request. Wait until the fields stop coming.
			if routed && s.now().UnixNano()-g.newest() < int64(draftSettle) {
				continue
			}
			item.Options = nil
			sent, err := s.redirectNodeAsk(g.scope, prefer, item)
			if err != nil {
				return err
			}
			if sent {
				continue
			}
			item.Options = []string{"accept", "reject"}
		}
		if _, err := s.createInboxItem(s.db, item); err != nil {
			return err
		}
	}
	// Close what has no drafts any more, and fold the old asks of one field.
	pendingRefs, pendingNodes, pendingIDs := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, g := range groups {
		pendingRefs[g.ref()] = true
		if !g.team {
			pendingNodes[g.scope] = true
		}
		for _, d := range g.drafts {
			pendingIDs[fmt.Sprintf("%s%d", legacyDraftRefPrefix, d.ID)] = true
		}
	}
	grouped := 0
	for _, r := range open {
		reason := protocol.AskReasonResolved
		switch {
		case strings.HasPrefix(r.ref, draftGroupRefPrefix):
			if pendingRefs[r.ref] {
				continue
			}
		case strings.HasPrefix(r.ref, leadAskRefPrefix):
			if pendingNodes[r.about] {
				continue
			}
		case strings.HasPrefix(r.ref, legacyDraftRefPrefix):
			if pendingIDs[r.ref] {
				reason = protocol.AskReasonGrouped
				grouped++
			}
		default:
			continue
		}
		if _, err := s.db.Exec(`UPDATE inbox_items SET status='closed',answer=?,answered_at=? WHERE id=? AND status='open'`, reason, s.now().UnixNano(), r.id); err != nil {
			return err
		}
	}
	if grouped > 0 {
		return s.deskLog(s.db, "desk", "", "", fmt.Sprintf("Folded %d charter draft asks of single fields into %d asks, one for each node", grouped, len(groups)), "", nil)
	}
	return nil
}

// leadSawDrafts says whether the lead of the node's team already got an ask
// that holds every field of the group. A lead that answered is not asked again
// for the same fields; a new field asks again.
func (s *Store) leadSawDrafts(g draftGroup) (bool, error) {
	rows, err := s.db.Query(`SELECT detail FROM inbox_items WHERE kind='draft' AND about_id=? AND status<>'closed' AND source_ref LIKE ?`, g.scope, leadAskRefPrefix+"%")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var detail string
		if err := rows.Scan(&detail); err != nil {
			return false, err
		}
		have := map[string]bool{}
		for _, line := range strings.Split(detail, "\n") {
			have[line] = true
		}
		all := true
		for _, line := range g.fieldLines() {
			if !have[line] {
				all = false
				break
			}
		}
		if all {
			return true, nil
		}
	}
	return false, rows.Err()
}

// nodeAskSession picks the session that asks for a node: the preferred one if
// it is still there, otherwise the latest session of the repo or agent.
func (s *Store) nodeAskSession(node, prefer string) (string, error) {
	var id string
	err := s.db.QueryRow(`SELECT id FROM sessions WHERE (repo_id=? OR agent_id=?) AND worker='' AND archived_at=0 AND harness NOT IN ('owner','cto','bot')
		ORDER BY (id=?) DESC,last_seen DESC,id LIMIT 1`, node, node, prefer).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return id, err
}

// nodeRoutesToLead says whether the ask of a node goes to a team lead: the
// node is a member of a team with asks_to=lead, it is not the lead, and the
// lead has a session.
func (s *Store) nodeRoutesToLead(node, prefer string) (bool, error) {
	session, err := s.nodeAskSession(node, prefer)
	if err != nil || session == "" {
		return false, err
	}
	_, leadAgent, pinned, ok, err := s.askerTeam(session)
	if err != nil || !ok {
		return false, err
	}
	lead, _, err := s.leadAskSession(leadAgent, pinned)
	return lead != "" && lead != session, err
}

// redirectNodeAsk sends the ask of a node to its team lead when the node's
// team has asks_to=lead. It reports false, and does nothing, when the ask
// belongs to the owner. The request to the lead is queued for delivery.
func (s *Store) redirectNodeAsk(node, prefer string, item protocol.InboxItem) (bool, error) {
	session, err := s.nodeAskSession(node, prefer)
	if err != nil || session == "" {
		return false, err
	}
	item.SessionID = session
	red, m, err := s.RedirectOwnerAsk(item)
	if err != nil {
		if _, ok := err.(Rejection); ok {
			return false, nil
		}
		return false, err
	}
	if red == nil {
		return false, nil
	}
	if m.State != protocol.StateHeld {
		s.leadOutMu.Lock()
		s.leadOut = append(s.leadOut, m)
		s.leadOutMu.Unlock()
	}
	return true, nil
}

// TakeLeadRequests returns the requests that the asks reconcile sent to team
// leads and that still need delivery. The gateway dispatches them.
func (s *Store) TakeLeadRequests() []protocol.Message {
	s.leadOutMu.Lock()
	defer s.leadOutMu.Unlock()
	out := s.leadOut
	s.leadOut = nil
	return out
}

// pendingDraftGroup finds the pending drafts of the group that a source key
// names. field narrows it to one "level:attr" field.
func (s *Store) pendingDraftGroup(ref, field string) ([]protocol.CharterDraft, error) {
	key := strings.TrimPrefix(ref, draftGroupRefPrefix)
	drafts, err := s.PendingCharterDrafts()
	if err != nil {
		return nil, err
	}
	var out []protocol.CharterDraft
	for _, d := range drafts {
		if draftGroupKey(d) == key && (field == "" || draftFieldLabel(d) == field) {
			out = append(out, d)
		}
	}
	return out, nil
}

// answerDraftGroup applies an answer to the grouped ask of a node: "accept" or
// "reject" for every pending field, or "accept level:attr" for one. The ask
// closes when no field is left; otherwise it stays with the fields that remain.
func (s *Store) answerDraftGroup(item protocol.InboxItem, ref, answer, actor string, validateOnly bool) (protocol.InboxItem, error) {
	verb, field, _ := strings.Cut(strings.TrimSpace(answer), " ")
	field = strings.TrimSpace(field)
	if verb != "accept" && verb != "reject" {
		return item, reject("a charter draft answer must be accept or reject")
	}
	targets, err := s.pendingDraftGroup(ref, field)
	if err != nil {
		return item, err
	}
	if len(targets) == 0 {
		if field != "" {
			return item, reject("no pending field %q in this charter draft", field)
		}
		return item, reject("charter draft is not pending")
	}
	item.Answer = answer
	item.AnsweredAt = s.now().UTC()
	if validateOnly {
		item.Status = "answered"
		return item, nil
	}
	for _, d := range targets {
		if err := s.DecideCharterDraft(d.ID, verb, actor); err != nil {
			return item, err
		}
	}
	rest, err := s.pendingDraftGroup(ref, "")
	if err != nil {
		return item, err
	}
	item.Status = "open"
	if len(rest) == 0 {
		item.Status = "answered"
		if err := s.closeAnsweredItem(item, answer); err != nil {
			return item, err
		}
	}
	return item, s.reconcileLearnedAsks()
}

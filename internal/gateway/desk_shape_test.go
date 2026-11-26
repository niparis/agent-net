package gateway

import (
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The Desk item shape of decision 0150, plan step (a).

// deskRig is a chief rig with a CTO session on the chief's machine.
func deskRig(t *testing.T) (s *Store, c *clock, chief, asker, cto, node string) {
	t.Helper()
	s, c, chief, asker, node = chiefRig(t)
	var err error
	cto, _, err = s.upsertSession(s.db, node, protocol.SessionInfo{Harness: "cto", NativeID: "cto", Name: "CTO"}, true)
	if err != nil {
		t.Fatal(err)
	}
	return s, c, chief, asker, cto, node
}

// carolSession adds a third live agent session on node.
func carolSession(t *testing.T, s *Store, node string) string {
	t.Helper()
	id, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "codex", NativeID: "nc", Name: "carol"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE sessions SET online=1 WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	return id
}

// itemRow reads the Desk fields of one item.
func itemRow(t *testing.T, s *Store, id string) (status, owner, state, category, dupOf string) {
	t.Helper()
	var next string
	if err := s.db.QueryRow(`SELECT status,owner,next_who,category FROM inbox_items WHERE id=?`, id).Scan(&status, &owner, &next, &category); err != nil {
		t.Fatal(err)
	}
	return status, owner, deskState(status, owner, next), category, ""
}

func trySort(s *Store, node, chief string, a protocol.AskSortArgs) (protocol.AskSortResult, error) {
	res, _, err := s.SortAsk(node, protocol.AskSortRequest{SessionID: chief, AskSortArgs: a})
	return res, err
}

func TestOwnerBoundItemWithoutOptionsIsRefused(t *testing.T) {
	s, _, chief, asker, node := chiefRig(t)
	item, err := s.CreateInboxItem(protocol.InboxItem{Kind: "check", SessionID: asker, Node: "demo", Text: "bob is blocked on review"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = trySort(s, node, chief, protocol.AskSortArgs{Ask: item.ID, Result: protocol.AskSortOwner, Reason: "needs him", Priority: 2, WhyYou: "only you"})
	if err == nil || !strings.Contains(err.Error(), "options") {
		t.Fatalf("owner sort without options: err = %v, want a refusal that names options", err)
	}
	if _, _, state, _, _ := itemRow(t, s, item.ID); state != protocol.DeskVerifying {
		t.Fatalf("refused sort moved the item: state %q", state)
	}
	if _, err := trySort(s, node, chief, protocol.AskSortArgs{Ask: item.ID, Result: protocol.AskSortOwner, Reason: "needs him", Priority: 2, WhyYou: "only you",
		Options: []string{"wait", "reassign"}, Recommendation: "wait"}); err != nil {
		t.Fatal(err)
	}
	full, err := s.askFull(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(full.Options, ",") != "wait,reassign" || full.Recommendation != "wait" {
		t.Fatalf("owner item = options %v recommendation %q", full.Options, full.Recommendation)
	}
	if _, _, state, _, _ := itemRow(t, s, item.ID); state != protocol.DeskReady {
		t.Fatalf("owner-bound item state %q, want ready", state)
	}
	// An agent's own ask that names the owner as next is refused the same way.
	_, err = s.CreateInboxItem(protocol.InboxItem{Kind: "question", SessionID: asker, Node: "demo", Text: "Ship it?", NextWho: protocol.AskNextOwner})
	if err == nil || !strings.Contains(err.Error(), "options") {
		t.Fatalf("owner-bound ask without options: err = %v", err)
	}
}

func TestMultiQuestionAskIsSplit(t *testing.T) {
	s, _, _, asker, _ := chiefRig(t)
	item, err := s.CreateInboxItem(protocol.InboxItem{Kind: "question", SessionID: asker, Node: "demo",
		Text: "Two things on the release.\n1. Should I merge the picker change?\n2. Should I deploy to the Mac?", Options: []string{"yes", "no"}})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.db.Query(`SELECT text FROM inbox_items WHERE session_id=? AND status='open' ORDER BY created_at,rowid`, asker)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var texts []string
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			t.Fatal(err)
		}
		texts = append(texts, text)
	}
	want := []string{"Two things on the release.\nShould I merge the picker change?", "Two things on the release.\nShould I deploy to the Mac?"}
	if strings.Join(texts, "|") != strings.Join(want, "|") {
		t.Fatalf("split asks = %q, want %q", texts, want)
	}
	if item.Text != want[0] {
		t.Fatalf("returned item %q, want the first part", item.Text)
	}
}

func TestDuplicateBySourceJoinsOpenItem(t *testing.T) {
	s, _, _, asker, node := chiefRig(t)
	other := carolSession(t, s, node)
	first, err := s.CreateInboxItem(protocol.InboxItem{Kind: "question", SessionID: asker, Node: "demo", Text: "Merge the picker change?", Options: []string{"yes", "no"}, SourceRef: "change:ch_1"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.CreateInboxItem(protocol.InboxItem{Kind: "question", SessionID: other, Node: "demo", Text: "Merge the picker change?", Options: []string{"yes", "no"}, SourceRef: "change:ch_1"})
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID {
		t.Fatalf("duplicate by source made item %s, want it to join %s", second.ID, first.ID)
	}
	joined, err := joinedAskers(s.db, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(joined) != 1 || joined[0] != other {
		t.Fatalf("joined askers = %v, want [%s]", joined, other)
	}
}

func TestDuplicateByNormalizedTextJoinsWithin24Hours(t *testing.T) {
	s, c, _, asker, _ := chiefRig(t)
	first, err := s.CreateInboxItem(protocol.InboxItem{Kind: "question", SessionID: asker, Node: "demo", Text: "Merge change ch_1a2b3c4d5e now?", Options: []string{"yes", "no"}})
	if err != nil {
		t.Fatal(err)
	}
	c.advance(time.Hour)
	again, err := s.CreateInboxItem(protocol.InboxItem{Kind: "approval", SessionID: asker, Node: "demo", Text: "  merge CHANGE   ch_9f8e7d6c5b NOW?"})
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != first.ID {
		t.Fatalf("same normalized text made item %s, want %s", again.ID, first.ID)
	}
	c.advance(24 * time.Hour)
	later, err := s.CreateInboxItem(protocol.InboxItem{Kind: "approval", SessionID: asker, Node: "demo", Text: "Merge change ch_1a2b3c4d5e now?"})
	if err != nil {
		t.Fatal(err)
	}
	if later.ID == first.ID {
		t.Fatal("an ask 25 hours later joined the old item; the fallback holds for 24 hours")
	}
}

func TestEveryJoinedAskerGetsTheAnswer(t *testing.T) {
	s, _, _, asker, node := chiefRig(t)
	other := carolSession(t, s, node)
	first, err := s.CreateInboxItem(protocol.InboxItem{Kind: "question", SessionID: asker, Node: "demo", Text: "Deploy?", Options: []string{"yes", "no"}, SourceRef: "change:ch_2"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateInboxItem(protocol.InboxItem{Kind: "question", SessionID: other, Node: "demo", Text: "Deploy?", Options: []string{"yes", "no"}, SourceRef: "change:ch_2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AnswerInbox(first.ID, "yes"); err != nil {
		t.Fatal(err)
	}
	for _, who := range []string{asker, other} {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE recipient=? AND body='yes'`, who).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("asker %s got %d answers, want 1", who, n)
		}
	}
	if status, _, _, _, _ := itemRow(t, s, first.ID); status != "answered" {
		t.Fatalf("item status %q, want answered", status)
	}
}

// The live case of i_16a5a8022de2c7d6: the chief routed an ask to the CTO and
// was then refused when it closed it.
func TestChiefClosesAskRoutedToCTOAndItsDuplicates(t *testing.T) {
	s, _, chief, asker, cto, node := deskRig(t)
	other := carolSession(t, s, node)
	item, err := s.CreateInboxItem(protocol.InboxItem{Kind: "question", SessionID: asker, Node: "demo", Text: "Which model for the judge?", Options: []string{"a", "b"}, SourceRef: "judge:1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateInboxItem(protocol.InboxItem{Kind: "question", SessionID: other, Node: "demo", Text: "Which model for the judge?", Options: []string{"a", "b"}, SourceRef: "judge:1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := trySort(s, node, chief, protocol.AskSortArgs{Ask: item.ID, Result: protocol.AskSortSend, To: cto, Reason: "a model choice"}); err != nil {
		t.Fatal(err)
	}
	if _, owner, state, _, _ := itemRow(t, s, item.ID); owner != protocol.DeskOwnerCTO || state != protocol.DeskWithCTO {
		t.Fatalf("routed item owner %q state %q, want cto with_cto", owner, state)
	}
	if _, err := trySort(s, node, chief, protocol.AskSortArgs{Ask: item.ID, Result: protocol.AskSortClose, Reason: "settled in chat"}); err != nil {
		t.Fatalf("chief close of an ask it routed to the CTO: %v", err)
	}
	if status, _, _, _, _ := itemRow(t, s, item.ID); status != "closed" {
		t.Fatalf("item status %q, want closed", status)
	}
}

func TestCTOClosesRoutedAskAndItsDuplicates(t *testing.T) {
	s, _, chief, asker, cto, node := deskRig(t)
	other := carolSession(t, s, node)
	item, err := s.CreateInboxItem(protocol.InboxItem{Kind: "question", SessionID: asker, Node: "demo", Text: "Raise the quota floor?", Options: []string{"yes", "no"}, SourceRef: "quota:1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateInboxItem(protocol.InboxItem{Kind: "question", SessionID: other, Node: "demo", Text: "Raise the quota floor?", Options: []string{"yes", "no"}, SourceRef: "quota:1"}); err != nil {
		t.Fatal(err)
	}
	if joined, err := joinedAskers(s.db, item.ID); err != nil || len(joined) != 1 {
		t.Fatalf("joined askers before the close = %v (%v), want 1", joined, err)
	}
	if _, err := trySort(s, node, chief, protocol.AskSortArgs{Ask: item.ID, Result: protocol.AskSortSend, To: cto, Reason: "quota is the CTO's"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AnswerInboxAs(item.ID, "dismiss", "cto"); err != nil {
		t.Fatal(err)
	}
	if status, _, _, _, _ := itemRow(t, s, item.ID); status != "dismissed" {
		t.Fatalf("status after CTO dismiss = %q, want dismissed", status)
	}
}

func TestCategoryIsSetAtIntakeAndChiefCorrectsIt(t *testing.T) {
	s, _, chief, asker, node := chiefRig(t)
	signIn, err := s.createInboxItem(s.db, protocol.InboxItem{Kind: protocol.AskKindSignIn, SessionID: gatewayAsker, Text: "Sign in to codex", FromKind: protocol.AskFromGateway, NextWho: protocol.AskNextOwner})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, category, _ := itemRow(t, s, signIn.ID); category != protocol.CategorySecurity {
		t.Fatalf("sign_in category %q, want security", category)
	}
	q := chiefAsk(t, s, asker, "Buy more tokens?")
	if _, _, _, category, _ := itemRow(t, s, q); category != protocol.CategoryNormal {
		t.Fatalf("question category %q, want normal", category)
	}
	if _, err := trySort(s, node, chief, protocol.AskSortArgs{Ask: q, Result: protocol.AskSortOwner, Reason: "spend", Priority: 1, WhyYou: "money", Category: protocol.CategoryMoney}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, category, _ := itemRow(t, s, q); category != protocol.CategoryMoney {
		t.Fatalf("corrected category %q, want money", category)
	}
}

// scopedRule answers an ask with a scope and returns the rule it created.
func scopedRule(t *testing.T, s *Store, id, answer string, scope protocol.RuleScope) protocol.StandingOrder {
	t.Helper()
	if _, err := s.AnswerInboxScoped(id, answer, "owner", scope); err != nil {
		t.Fatal(err)
	}
	rules, err := s.FindJudgments(protocol.JudgmentsFindRequest{Kind: protocol.StandingKindRule, Ref: id})
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 {
		t.Fatalf("rules for ask %s = %d, want 1", id, len(rules))
	}
	return rules[0]
}

func TestScopedAnswerCreatesRuleWithUndo(t *testing.T) {
	s, _, _, asker, _ := chiefRig(t)
	id := chiefAsk(t, s, asker, "Merge the picker change?")
	rule := scopedRule(t, s, id, "yes", protocol.RuleScope{Area: "repo:demo", Action: "Merge"})
	if rule.Area != "repo:demo" || rule.Action != "merge" {
		t.Fatalf("rule area %q action %q, want repo:demo merge", rule.Area, rule.Action)
	}
	log, err := s.DeskLog(5)
	if err != nil {
		t.Fatal(err)
	}
	var undo []string
	for _, e := range log {
		if strings.Contains(e.Text, rule.ID) {
			undo = e.Options
		}
	}
	if len(undo) != 2 || undo[0] != protocol.OpStandingOrders || !strings.Contains(undo[1], rule.ID) {
		t.Fatalf("Desk log undo of the rule = %q", undo)
	}
}

func TestRuleAppliesOnlyOnExactMatchAndCitesItsID(t *testing.T) {
	s, _, chief, asker, node := chiefRig(t)
	first := chiefAsk(t, s, asker, "Merge the picker change?")
	rule := scopedRule(t, s, first, "yes", protocol.RuleScope{Area: "repo:demo", Action: "merge"})

	next := chiefAskKind(t, s, asker, "approval", "Merge the judge change?")
	for _, miss := range []string{"merge to main", "Merge it"} {
		if _, err := trySort(s, node, chief, protocol.AskSortArgs{Ask: next, Result: protocol.AskSortClose, Reason: "standing rule", Rule: rule.ID, Action: miss}); err == nil {
			t.Fatalf("near miss action %q applied the rule", miss)
		}
	}
	other := chiefAskKind(t, s, asker, "feature_idea", "Merge in another repo?")
	if _, err := s.db.Exec(`UPDATE inbox_items SET about_id='demo2' WHERE id=?`, other); err != nil {
		t.Fatal(err)
	}
	if _, err := trySort(s, node, chief, protocol.AskSortArgs{Ask: other, Result: protocol.AskSortClose, Reason: "standing rule", Rule: rule.ID, Action: "merge"}); err == nil {
		t.Fatal("a rule for repo:demo applied to an ask in demo2")
	}
	if _, err := trySort(s, node, chief, protocol.AskSortArgs{Ask: next, Result: protocol.AskSortClose, Reason: "standing rule", Rule: rule.ID, Action: " MERGE "}); err != nil {
		t.Fatalf("exact match did not apply: %v", err)
	}
	var cited, answer string
	if err := s.db.QueryRow(`SELECT rule_id FROM ask_sorts WHERE ask_id=? ORDER BY id DESC LIMIT 1`, next).Scan(&cited); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT answer FROM inbox_items WHERE id=?`, next).Scan(&answer); err != nil {
		t.Fatal(err)
	}
	if cited != rule.ID || !strings.Contains(answer, rule.ID) {
		t.Fatalf("application cites rule %q, answer %q; want %s in both", cited, answer, rule.ID)
	}
}

func TestUndoneRuleNoLongerApplies(t *testing.T) {
	s, _, chief, asker, node := chiefRig(t)
	first := chiefAsk(t, s, asker, "Clean the Go cache?")
	rule := scopedRule(t, s, first, "yes", protocol.RuleScope{Area: "repo:demo", Action: "data cleanup"})
	if _, err := s.RemoveStandingOrder(rule.ID); err != nil {
		t.Fatal(err)
	}
	next := chiefAskKind(t, s, asker, "approval", "Clean the Go cache again?")
	if _, err := trySort(s, node, chief, protocol.AskSortArgs{Ask: next, Result: protocol.AskSortClose, Reason: "standing rule", Rule: rule.ID, Action: "data cleanup"}); err == nil {
		t.Fatal("an undone rule still applied")
	}
}

func TestNetworkRuleRefusedInOwnerOnlyCategory(t *testing.T) {
	s, _, _, asker, _ := chiefRig(t)
	id := chiefAsk(t, s, asker, "Rotate the npm token?")
	if _, err := s.db.Exec(`UPDATE inbox_items SET category='security' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	_, err := s.AnswerInboxScoped(id, "yes", "owner", protocol.RuleScope{Area: "network", Action: "rotate token"})
	if err == nil || !strings.Contains(err.Error(), "never the network") {
		t.Fatalf("network rule in security: err = %v", err)
	}
	var status string
	if err := s.db.QueryRow(`SELECT status FROM inbox_items WHERE id=?`, id).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "open" {
		t.Fatalf("refused scoped answer still answered the ask: status %q", status)
	}
	if _, err := s.AnswerInboxScoped(id, "yes", "owner", protocol.RuleScope{Area: "repo:demo", Action: "rotate token"}); err != nil {
		t.Fatalf("repo rule in security: %v", err)
	}
}

func TestJudgmentRecordByChiefAndCTOOnly(t *testing.T) {
	s, _, chief, asker, cto, node := deskRig(t)
	args := protocol.JudgmentRecordArgs{Text: "Always merge docs-only changes in demo.", Area: "repo:demo", Action: "merge docs"}
	o, err := s.RecordJudgment(node, protocol.JudgmentRecordRequest{SessionID: chief, JudgmentRecordArgs: args})
	if err != nil {
		t.Fatal(err)
	}
	if o.Kind != protocol.StandingKindRule || o.Area != "repo:demo" || o.Action != "merge docs" {
		t.Fatalf("recorded judgment = %+v", o)
	}
	once, err := s.RecordJudgment(node, protocol.JudgmentRecordRequest{SessionID: cto, JudgmentRecordArgs: protocol.JudgmentRecordArgs{Text: "Skip the Mac build today."}})
	if err != nil {
		t.Fatal(err)
	}
	if once.Kind != protocol.StandingKindAnswer {
		t.Fatalf("unscoped record kind %q, want answer", once.Kind)
	}
	var askerNode string
	if err := s.db.QueryRow(`SELECT node_id FROM sessions WHERE id=?`, asker).Scan(&askerNode); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordJudgment(askerNode, protocol.JudgmentRecordRequest{SessionID: asker, JudgmentRecordArgs: args}); err == nil {
		t.Fatal("a session that is neither the chief nor the CTO recorded an owner judgment")
	}
}

func TestCheckItemHasNoGeneratedRecommendation(t *testing.T) {
	s, _, _, asker, _ := chiefRig(t)
	if err := s.checkInboxItem(asker, "demo", "bob is blocked"); err != nil {
		t.Fatal(err)
	}
	var rec string
	if err := s.db.QueryRow(`SELECT recommendation FROM inbox_items WHERE session_id=? AND kind='check'`, asker).Scan(&rec); err != nil {
		t.Fatal(err)
	}
	if rec != "" {
		t.Fatalf("check item recommendation %q, want none", rec)
	}
}

// A short wait for the owner who works in the session makes no ask (decision
// 0150, Desk item 10): the ask of a waiting session comes only once it has
// waited the debounce. With the owner away there is no debounce (plan step c2).
func TestWaitingAskWaitsForTheDebounce(t *testing.T) {
	s, c := newTestStore(t)
	node, _, _ := s.CreateNode("box", "linux")
	info := protocol.SessionInfo{Harness: "claude", NativeID: "w", Name: "waiter"}
	id, err := s.UpsertSession(node, info)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordOwnerTyped(id); err != nil {
		t.Fatal(err)
	}
	info.Waiting = &protocol.Waiting{Kind: protocol.WaitingInput, Text: "Which database?", Since: c.t.Unix()}
	if _, err := s.UpsertSession(node, info); err != nil {
		t.Fatal(err)
	}
	if asks, _ := s.ListAsks(); len(askByKind(asks, "waiting")) != 0 {
		t.Fatalf("a wait of 0s made an ask: %+v", asks)
	}
	c.advance(waitingAskDebounce - time.Second)
	if err := s.reconcileAsks(); err != nil {
		t.Fatal(err)
	}
	if asks, _ := s.ListAsks(); len(askByKind(asks, "waiting")) != 0 {
		t.Fatalf("a wait shorter than the debounce made an ask: %+v", asks)
	}
	c.advance(2 * time.Second)
	if err := s.reconcileAsks(); err != nil {
		t.Fatal(err)
	}
	if asks, _ := s.ListAsks(); len(askByKind(asks, "waiting")) != 1 {
		t.Fatalf("a wait past the debounce made no ask: %+v", asks)
	}
}

// "Discuss" on a chief item sends the chief a message about the ask (which
// wakes it when it is offline) and names its session for the owner to open.
// On a CTO item it only says to open the CTO chat: the owner is there.
func TestDiscussOpensTheOwnersThread(t *testing.T) {
	s, _, chief, asker, cto, node := deskRig(t)
	id := chiefAsk(t, s, asker, "Which region for the new box?")
	res, m, err := s.discussAsk(id)
	if err != nil {
		t.Fatal(err)
	}
	if res.Open != protocol.DeskOwnerChief || res.Session != chief || m.To != chief || !strings.Contains(m.Body, id) || !strings.Contains(m.Body, "Which region") {
		t.Fatalf("discuss of a chief item = %+v, message to %s: %q", res, m.To, m.Body)
	}
	if _, err := trySort(s, node, chief, protocol.AskSortArgs{Ask: id, Result: protocol.AskSortSend, To: cto, Reason: "infra is the CTO's"}); err != nil {
		t.Fatal(err)
	}
	before := len(chiefMessages(t, s, chief))
	res, m, err = s.discussAsk(id)
	if err != nil {
		t.Fatal(err)
	}
	if res.Open != protocol.DeskOwnerCTO || m.ID != "" || len(chiefMessages(t, s, chief)) != before {
		t.Fatalf("discuss of a CTO item = %+v, message %+v", res, m)
	}
}

// The chief reports that an answer was carried out: the item moves from
// answered to carried_out, and every asker, joined ones included, is told.
func TestChiefReportsAnswerCarriedOut(t *testing.T) {
	s, _, chief, asker, node := chiefRig(t)
	other := carolSession(t, s, node)
	item, err := s.CreateInboxItem(protocol.InboxItem{Kind: "question", SessionID: asker, Node: "demo", Text: "Rotate the deploy key?", Options: []string{"yes", "no"}, SourceRef: "key:1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateInboxItem(protocol.InboxItem{Kind: "question", SessionID: other, Node: "demo", Text: "Rotate the deploy key?", Options: []string{"yes", "no"}, SourceRef: "key:1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := trySort(s, node, chief, protocol.AskSortArgs{Ask: item.ID, Result: protocol.AskSortDone, Reason: "rotated"}); err == nil {
		t.Fatal("an open ask was marked carried out")
	}
	if _, err := s.AnswerInbox(item.ID, "yes"); err != nil {
		t.Fatal(err)
	}
	if _, _, state, _, _ := itemRow(t, s, item.ID); state != protocol.DeskAnswered {
		t.Fatalf("answered item state %q", state)
	}
	if _, err := trySort(s, node, chief, protocol.AskSortArgs{Ask: item.ID, Result: protocol.AskSortDone, Reason: "the key is rotated on every machine"}); err != nil {
		t.Fatal(err)
	}
	if status, _, state, _, _ := itemRow(t, s, item.ID); status != protocol.DeskCarriedOut || state != protocol.DeskCarriedOut {
		t.Fatalf("item status %q state %q, want carried_out", status, state)
	}
	for _, who := range []string{asker, other} {
		got := false
		for _, body := range chiefMessages(t, s, who) {
			got = got || (strings.Contains(body, "carried out") && strings.Contains(body, "the key is rotated"))
		}
		if !got {
			t.Fatalf("asker %s was not told the answer was carried out", who)
		}
	}
}

// The answer and its rule are one write: when the rule cannot be stored, the
// answer is not stored either and the ask stays open.
func TestScopedAnswerIsOneWrite(t *testing.T) {
	s, _, _, asker, _ := chiefRig(t)
	id := chiefAsk(t, s, asker, "Merge the picker change?")
	if _, err := s.db.Exec(`CREATE TRIGGER fail_rule BEFORE INSERT ON standing_orders WHEN NEW.kind='rule' BEGIN SELECT RAISE(ABORT,'rule store down'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AnswerInboxScoped(id, "yes", "owner", protocol.RuleScope{Area: "repo:demo", Action: "merge"}); err == nil {
		t.Fatal("the scoped answer went through with no rule")
	}
	if status, _, _, _, _ := itemRow(t, s, id); status != "open" {
		t.Fatalf("a failed rule left the ask %q", status)
	}
	var sent int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE recipient=? AND body='yes'`, asker).Scan(&sent); err != nil || sent != 0 {
		t.Fatalf("a failed rule still sent the answer: %d (%v)", sent, err)
	}
}

// Only ids are folded out of the duplicate key: numbers stay.
func TestSameTextKeepsNumbersApart(t *testing.T) {
	s, _, _, asker, _ := chiefRig(t)
	a, err := s.CreateInboxItem(protocol.InboxItem{Kind: "question", SessionID: asker, Node: "demo", Text: "Merge 100 files?", Options: []string{"yes", "no"}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateInboxItem(protocol.InboxItem{Kind: "approval", SessionID: asker, Node: "demo", Text: "Merge 900 files?"})
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == b.ID {
		t.Fatal("two asks that differ in a number were joined as duplicates")
	}
}

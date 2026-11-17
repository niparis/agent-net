package gateway

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Property test for the ask and wait lifecycle (docs/design/ask-wait-lifecycle.md).
//
// It applies random event sequences to a real store on a fake clock and
// checks the invariants of the chart after every event. The bugs that gave it
// its shape were each found live, one at a time:
//
//   - an announce closed approvals (ch_b1ef03bb)
//   - an identical wait raised an ask every hour (ch_383b1a13, ch_1df063de)
//   - a blocked answer made a check row for a wait that had its ask
//
// A failing run prints the seed, the step and a short event list. Paste the
// list into TestAskLifecycleReplay to keep it as a regular test.
//
// Environment:
//
//	AGENT_NET_PROPERTY_LONG=1   many more seeds and steps
//	AGENT_NET_PROPERTY_SEED=n   run only seed n (a printed failing seed)

// lifeEvent is one step. It names its targets by number, never by id, so a
// list stays valid when events are removed (the shrinker) or pasted.
type lifeEvent struct {
	Op   string
	Sess int // which of the sessions
	Arg  int // a text, an ask ordinal, a time step
	Flag int // a variant of the op
}

func (e lifeEvent) String() string {
	return fmt.Sprintf("{%q, %d, %d, %d}", e.Op, e.Sess, e.Arg, e.Flag)
}

// The ops. Each is one thing that happens to asks or to a session's wait.
const (
	opAsk      = "ask"      // a session raises a question (Flag 0) or an approval (Flag 1) with text Arg
	opAnswer   = "answer"   // the owner answers ask Arg: Flag 0 approve, 1 refuse, 2 dismiss, 3 free text
	opWithdraw = "withdraw" // the asker takes ask Arg back
	opChief    = "chief"    // the chief closes ask Arg (and with it a waiting ask)
	opAnnounce = "announce" // an announce; Flag is the task (see announceTasks), Arg the wait text or -1
	opWait     = "wait"     // the daemon reports wait Arg again (Flag 1: without its start)
	opBusy     = "busy"     // a busy turn: the daemon reports no wait
	opCheck    = "check"    // the hourly done-check goes out
	opEnd      = "end"      // the session is archived
	opTyped    = "typed"    // the owner types in the asking session: the reply is his decision
	opTick     = "tick"     // time passes (Arg picks the step) and the minute reconcile runs
)

// announceTasks are the task lines an announce can carry. A check answer is
// read from the task line (parseCheckAnswer).
var announceTasks = []string{"working on the next thing", "blocked on the owner's decision", "done", "more: the review is left"}

const (
	taskPlain = iota
	taskBlocked
	taskDone
	taskMore
)

var (
	lifeQuestions = []string{"Which database should the pilot use?", "Use port 8080 for the demo?"}
	lifeApprovals = []string{"Approve merge #935 at 2143e3af", "Approve the deploy of v1.2"}
	lifeWaits     = []string{"Delete worktree w_1?", "owner's yes on the TRUNCATE, then deploy", "Merge #817?"}
	lifeSteps     = []time.Duration{5 * time.Second, 90 * time.Second, 3 * time.Minute, 11 * time.Minute, time.Hour, 25 * time.Hour}
)

const lifeSessions = 3

// genLife makes a list of n events from a seed. It holds no state: the
// numbers are reduced to what exists when the event is applied.
func genLife(seed int64, n int) []lifeEvent {
	rng := rand.New(rand.NewSource(seed))
	pick := func(k int) int { return rng.Intn(k) }
	out := make([]lifeEvent, 0, n)
	for len(out) < n {
		e := lifeEvent{Sess: pick(lifeSessions)}
		switch r := pick(100); {
		case r < 14:
			e.Op, e.Flag, e.Arg = opAsk, pick(2), pick(2)
		case r < 19:
			e.Op, e.Arg, e.Flag = opAnswer, pick(50), pick(4)
		case r < 23:
			e.Op, e.Arg = opWithdraw, pick(50)
		case r < 27:
			e.Op, e.Arg = opChief, pick(50)
		case r < 47:
			e.Op, e.Flag, e.Arg = opAnnounce, pick(len(announceTasks)), pick(len(lifeWaits)+1)-1
		case r < 62:
			e.Op, e.Arg, e.Flag = opWait, pick(len(lifeWaits)), pick(2)
		case r < 72:
			e.Op = opBusy
		case r < 77:
			e.Op = opCheck
		case r < 81:
			e.Op = opTyped
		case r < 83:
			e.Op = opEnd
		default:
			e.Op, e.Arg = opTick, pick(len(lifeSteps))
		}
		out = append(out, e)
	}
	return out
}

// lifeEpoch is one stretch in which a session reports the same wait. The
// daemon keeps its start across a busy turn, so the start does not move
// while the text stays the same. A clear or another text ends it.
type lifeEpoch struct {
	sess    int
	text    string
	since   int64
	startNs int64
	live    bool // false after a clear or a new text
}

// lifeRow is one inbox row as the checks read it.
type lifeRow struct {
	id, kind, session, status, answer, ref, text string
	created                                      int64
}

type lifeRig struct {
	t    testing.TB
	s    *Store
	c    *clock
	node string
	sess [lifeSessions]string

	ended   [lifeSessions]bool       // archived, and not yet resumed
	current [lifeSessions]*lifeEpoch // the wait the daemon holds now (nil: none)
	cleared [lifeSessions]*lifeEpoch // the wait a busy turn cleared and may come back
	epochs  []*lifeEpoch
	taskN   int
	// seen counts the situations a run reached, so a run that never reaches
	// one is told apart from a run that found nothing wrong.
	seen   map[string]int
	before []lifeRow
	steps  int
}

func newLifeRig(t testing.TB) *lifeRig {
	s, c := newTestStore(t)
	node, _, err := s.CreateNode("box", "linux")
	if err != nil {
		t.Fatal(err)
	}
	r := &lifeRig{t: t, s: s, c: c, node: node, seen: map[string]int{}}
	for i := range r.sess {
		id, err := s.UpsertSession(node, r.info(i, "", nil))
		if err != nil {
			t.Fatal(err)
		}
		r.sess[i] = id
	}
	// The one-time cleanup of asks that sessions left behind runs at the first
	// reconcile. Run it now, so it is not mistaken for an event.
	if err := s.reconcileAsks(); err != nil {
		t.Fatal(err)
	}
	return r
}

func (r *lifeRig) info(i int, status string, w *protocol.Waiting) protocol.SessionInfo {
	return protocol.SessionInfo{Harness: "claude", NativeID: "life" + strconv.Itoa(i), Name: "life" + strconv.Itoa(i), Status: status, Waiting: w}
}

func (r *lifeRig) rows() []lifeRow {
	rows, err := r.s.db.Query(`SELECT id,kind,session_id,status,answer,source_ref,text,created_at FROM inbox_items ORDER BY rowid`)
	if err != nil {
		r.t.Fatal(err)
	}
	defer rows.Close()
	var out []lifeRow
	for rows.Next() {
		var x lifeRow
		if err := rows.Scan(&x.id, &x.kind, &x.session, &x.status, &x.answer, &x.ref, &x.text, &x.created); err != nil {
			r.t.Fatal(err)
		}
		out = append(out, x)
	}
	if err := rows.Err(); err != nil {
		r.t.Fatal(err)
	}
	return out
}

func (r *lifeRig) must(err error, what string) {
	if err != nil {
		r.t.Fatalf("%s: %v", what, err)
	}
}

// startWait is the daemon reporting a wait: the same text as the wait it holds
// or a busy turn cleared keeps its start, anything else starts a new one.
func (r *lifeRig) startWait(i int, text string) *lifeEpoch {
	for _, e := range []*lifeEpoch{r.current[i], r.cleared[i]} {
		if e != nil && e.text == text {
			r.current[i], r.cleared[i] = e, nil
			return e
		}
	}
	if r.current[i] != nil {
		r.current[i].live = false
	}
	e := &lifeEpoch{sess: i, text: text, since: r.c.now().Unix(), startNs: r.c.now().UnixNano(), live: true}
	r.current[i], r.cleared[i] = e, nil
	r.epochs = append(r.epochs, e)
	return e
}

// endWait is the daemon clearing the wait, or the session moving on.
func (r *lifeRig) endWait(i int) {
	if r.current[i] != nil {
		r.current[i].live = false
	}
	r.current[i], r.cleared[i] = nil, nil
}

func waitOf(text string, since int64) *protocol.Waiting {
	return &protocol.Waiting{Kind: protocol.WaitingInput, Text: text, Since: since}
}

// apply runs one event. It returns the violation it found, or "".
func (r *lifeRig) apply(e lifeEvent) string {
	r.steps++
	i := ((e.Sess % lifeSessions) + lifeSessions) % lifeSessions
	sid := r.sess[i]
	// Every event happens in a later second than the one before it, so two
	// events never share a created_at and a wait start stays distinct.
	r.c.t = r.c.t.Add(time.Duration(1+r.steps%3) * time.Second)
	r.before = r.rows()
	nowNs := r.c.now().UnixNano()
	r.count(e, i)
	// An archived session raises and reports nothing: a session that does
	// has been resumed, and its next report brings it back.
	if r.ended[i] && (e.Op == opAsk || e.Op == opWait || e.Op == opBusy || e.Op == opCheck || e.Op == opAnnounce) {
		var w *protocol.Waiting
		if cur := r.current[i]; cur != nil {
			w = waitOf(cur.text, cur.since)
		}
		_, err := r.s.UpsertSession(r.node, r.info(i, "", w))
		r.must(err, "resume")
		r.ended[i] = false
	}

	switch e.Op {
	case opAsk:
		kind, texts := protocol.AskKindQuestion, lifeQuestions
		if e.Flag == 1 {
			kind, texts = protocol.AskKindApproval, lifeApprovals
		}
		item := protocol.InboxItem{Kind: kind, SessionID: sid, FromKind: protocol.AskFromSession, Text: texts[e.Arg%len(texts)]}
		// A shared subject lets a repeat from another session join the ask.
		if e.Arg%2 == 0 {
			item.AboutKind, item.AboutID = protocol.AskAboutProject, "pilot"
		}
		_, err := r.s.CreateInboxItem(item)
		r.must(err, "ask")

	case opAnswer, opWithdraw, opChief:
		id := r.askAt(e.Arg)
		if id == "" {
			return ""
		}
		switch e.Op {
		case opAnswer:
			answer := []string{"approve", "refuse", "dismiss", "go with the first one"}[e.Flag%4]
			_, err := r.s.AnswerInbox(id, answer)
			r.rejected(err, "answer")
		case opWithdraw:
			var asker string
			r.must(r.s.db.QueryRow(`SELECT session_id FROM inbox_items WHERE id=?`, id).Scan(&asker), "read asker")
			_, _, err := r.s.SortAsk(r.node, protocol.AskSortRequest{SessionID: asker, AskSortArgs: protocol.AskSortArgs{
				Ask: id, Result: protocol.AskSortWithdraw, Reason: "the head moved"}})
			r.rejected(err, "withdraw")
		case opChief:
			// What a chief close writes (SortAskWith, result close).
			_, err := r.s.db.Exec(`UPDATE inbox_items SET status='closed', answer='chief: same wait', answered_at=? WHERE id=? AND status='open'`, nowNs, id)
			r.must(err, "chief close")
		}

	case opWait:
		e2 := r.startWait(i, lifeWaits[e.Arg%len(lifeWaits)])
		since := e2.since
		// A daemon that restarted reports the text without its start; the
		// gateway then keeps the start it stored.
		if e.Flag == 1 && r.storedWait(sid) != nil {
			since = 0
		}
		_, err := r.s.UpsertSession(r.node, r.info(i, "", waitOf(e2.text, since)))
		r.must(err, "wait")

	case opBusy:
		// The daemon clears the wait for the turn and remembers it.
		if r.current[i] != nil {
			r.cleared[i], r.current[i] = r.current[i], nil
		}
		_, err := r.s.UpsertSession(r.node, r.info(i, "busy", nil))
		r.must(err, "busy")

	case opCheck:
		_, err := r.s.db.Exec(`INSERT INTO session_checks(session_id,check_active,check_waiting) VALUES(?,?,1)
			ON CONFLICT(session_id) DO UPDATE SET check_waiting=1`, sid, r.c.now().Unix())
		r.must(err, "check")

	case opAnnounce:
		return r.announce(i, e)

	case opTyped:
		r.must(r.s.closeOwnerRepliedAsks(sid, "yes, go ahead"), "typed")

	case opEnd:
		r.must(r.s.CloseArchivedSessionInbox(r.node, sid), "end")
		_, err := r.s.db.Exec(`UPDATE sessions SET archived_at=? WHERE id=?`, nowNs, sid)
		r.must(err, "archive")
		r.ended[i] = true

	case opTick:
		r.c.t = r.c.t.Add(lifeSteps[e.Arg%len(lifeSteps)])
		r.must(r.s.reconcileAsks(), "reconcile")

	default:
		r.t.Fatalf("unknown op %q", e.Op)
	}
	return r.check(e)
}

// count notes which situations an event meets. Only the first line of a
// situation is counted; the checks do not read it.
func (r *lifeRig) count(e lifeEvent, i int) {
	sid := r.sess[i]
	switch e.Op {
	case opAnnounce, opBusy, opTick:
		for _, x := range r.before {
			if x.kind == protocol.AskKindApproval && x.status == "open" {
				r.seen["event with an open approval ("+e.Op+")"]++
				break
			}
		}
	}
	if e.Op == opWait || e.Op == opAnnounce && e.Arg >= 0 {
		text := lifeWaits[((e.Arg%len(lifeWaits))+len(lifeWaits))%len(lifeWaits)]
		if r.liveEpoch(i, text) != nil {
			switch r.latestWaitingStatus(sid) {
			case "open":
				r.seen["same wait reported again, ask open"]++
			case "":
			default:
				r.seen["same wait reported again, ask settled"]++
			}
		}
	}
}

func (r *lifeRig) latestWaitingStatus(sid string) string {
	var st string
	err := r.s.db.QueryRow(`SELECT status FROM inbox_items WHERE source_ref=? AND kind='waiting' AND status<>'joined' ORDER BY created_at DESC, rowid DESC LIMIT 1`, "waiting:"+sid).Scan(&st)
	if err != nil && err != sql.ErrNoRows {
		r.t.Fatal(err)
	}
	return st
}

// rejected allows the refusals a stale target gets, and fails on any other error.
func (r *lifeRig) rejected(err error, what string) {
	if err == nil {
		return
	}
	if _, ok := err.(Rejection); ok || strings.Contains(err.Error(), "not open") || strings.Contains(err.Error(), "already answered") {
		return
	}
	r.t.Fatalf("%s: %v", what, err)
}

// announce is the daemon's announce path (local.go): the gateway hears the
// announce first, then the daemon sets or clears the wait and reports it.
func (r *lifeRig) announce(i int, e lifeEvent) string {
	sid := r.sess[i]
	task := announceTasks[e.Flag%len(announceTasks)]
	if e.Flag%len(announceTasks) == taskPlain {
		r.taskN++
		task += " " + strconv.Itoa(r.taskN)
	}
	var carried *protocol.Waiting
	if e.Arg >= 0 {
		text := lifeWaits[e.Arg%len(lifeWaits)]
		since := int64(0)
		if ep := r.liveEpoch(i, text); ep != nil {
			since = ep.since
		}
		carried = waitOf(text, since)
	}
	// What the gateway should do with a blocked answer is read from the rows
	// before it, not from its own code: the wait the answer repeats has an ask
	// (open or settled) when the latest waiting row of the session says the
	// same thing.
	armed := r.checkArmed(sid)
	repeated := ""
	switch {
	case carried != nil:
		repeated = carried.Text
	case r.current[i] != nil:
		repeated = r.current[i].text
	}
	covered := armed && e.Flag%len(announceTasks) == taskBlocked && repeated != "" && r.latestWaitingText(sid) != "" &&
		protocol.SameAskText(r.latestWaitingText(sid), repeated)
	checkBefore := r.openChecks(sid)
	if covered {
		r.seen["blocked answer repeating a wait that has an ask"]++
	}

	_, err := r.s.Announce(r.node, protocol.ProfileAnnounce{SessionID: sid, Task: task, Waiting: carried})
	r.must(err, "announce")

	// The daemon: a wait in the announce replaces the wait; none clears it,
	// except for a blocked answer, which keeps the wait it still holds.
	var next *protocol.Waiting
	switch {
	case carried != nil:
		ep := r.startWait(i, carried.Text)
		next = waitOf(ep.text, ep.since)
	case e.Flag%len(announceTasks) == taskBlocked && r.current[i] != nil:
		next = waitOf(r.current[i].text, r.current[i].since)
	default:
		r.endWait(i)
	}
	_, err = r.s.UpsertSession(r.node, r.info(i, "", next))
	r.must(err, "report after announce")

	if covered && r.openChecks(sid) != checkBefore {
		return "a blocked answer repeating a wait that has an ask changed the check rows (" + checkBefore + " -> " + r.openChecks(sid) + ")"
	}
	return r.check(e)
}

func (r *lifeRig) liveEpoch(i int, text string) *lifeEpoch {
	for _, e := range []*lifeEpoch{r.current[i], r.cleared[i]} {
		if e != nil && e.text == text {
			return e
		}
	}
	return nil
}

func (r *lifeRig) checkArmed(sid string) bool {
	var n int
	err := r.s.db.QueryRow(`SELECT COALESCE((SELECT check_waiting FROM session_checks WHERE session_id=?),0)`, sid).Scan(&n)
	r.must(err, "read check")
	return n == 1
}

func (r *lifeRig) latestWaitingText(sid string) string {
	var text string
	err := r.s.db.QueryRow(`SELECT text FROM inbox_items WHERE source_ref=? AND kind='waiting' AND status<>'joined' ORDER BY created_at DESC, rowid DESC LIMIT 1`, "waiting:"+sid).Scan(&text)
	if err == sql.ErrNoRows {
		return ""
	}
	r.must(err, "read waiting")
	return text
}

// openChecks is the open check rows of a session as one string: id, text and
// time, so a row that is changed in place shows too.
func (r *lifeRig) openChecks(sid string) string {
	rows, err := r.s.db.Query(`SELECT id,text,created_at FROM inbox_items WHERE session_id=? AND kind='check' AND status='open' ORDER BY rowid`, sid)
	r.must(err, "read checks")
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id, text string
		var at int64
		r.must(rows.Scan(&id, &text, &at), "scan checks")
		out = append(out, fmt.Sprintf("%s|%s|%d", id, text, at))
	}
	return strings.Join(out, ";")
}

func (r *lifeRig) storedWait(sid string) *protocol.Waiting {
	var raw string
	r.must(r.s.db.QueryRow(`SELECT waiting FROM sessions WHERE id=?`, sid).Scan(&raw), "read wait")
	if raw == "" {
		return nil
	}
	var w protocol.Waiting
	r.must(json.Unmarshal([]byte(raw), &w), "decode wait")
	return &w
}

// check runs the invariants after an event. It returns the first violation.
func (r *lifeRig) check(e lifeEvent) string {
	after := r.rows()
	prev := map[string]lifeRow{}
	for _, x := range r.before {
		prev[x.id] = x
	}
	approvalCloser := e.Op == opAnswer || e.Op == opWithdraw || e.Op == opChief || e.Op == opEnd || e.Op == opTyped
	openWaiting := map[string]int{}
	waitingRows := map[string][]lifeRow{}
	openChecks := map[string]int{}
	for _, x := range after {
		old, existed := prev[x.id]
		// 1. A withdrawn or answered ask never reopens.
		if existed && old.status != "open" && old.status != "joined" && x.status == "open" {
			return fmt.Sprintf("ask %s (%s) went from %s back to open", x.id, x.kind, old.status)
		}
		// 2. An approval is ended only by an owner answer, the asker's
		// withdraw, the chief, or the end of its session. Never by an announce.
		if existed && x.kind == protocol.AskKindApproval && old.status == "open" && x.status != "open" && !approvalCloser {
			return fmt.Sprintf("approval %s was closed (%s) by %s", x.id, x.answer, e.Op)
		}
		// 3. Every closed ask says why, a duplicate that joined one included.
		if x.status == "closed" && strings.TrimSpace(x.answer) == "" {
			return fmt.Sprintf("ask %s (%s) is closed with no reason", x.id, x.kind)
		}
		if x.kind == protocol.AskKindWaiting && x.status != "joined" {
			waitingRows[x.session] = append(waitingRows[x.session], x)
			if x.status == "open" {
				openWaiting[x.session]++
			}
		}
		if x.kind == protocol.AskKindCheck && x.status == "open" {
			openChecks[x.session]++
		}
	}
	for sess, n := range openWaiting {
		// 4. One open waiting ask per session wait.
		if n > 1 {
			return fmt.Sprintf("session %s has %d open waiting asks", sess, n)
		}
	}
	for sess, n := range openChecks {
		// 5. One open check row per session, so a repeat updates the row.
		if n > 1 {
			return fmt.Sprintf("session %s has %d open check rows", sess, n)
		}
	}
	// 6. A wait the session reports again, with the start it had, never makes
	// a second ask once the first is settled: at most one waiting ask is
	// created in one epoch of a session.
	for i, sid := range r.sess {
		perEpoch := map[*lifeEpoch]int{}
		for _, row := range waitingRows[sid] {
			if ep := r.epochOf(i, row.created); ep != nil {
				perEpoch[ep]++
				if perEpoch[ep] > 1 {
					return fmt.Sprintf("session %s raised %d waiting asks for one wait (%q, since %d)", sid, perEpoch[ep], ep.text, ep.since)
				}
			}
		}
		// 7. The wait the gateway holds has the start the daemon holds.
		if w, cur := r.storedWait(sid), r.current[i]; w != nil && cur != nil && w.Text == cur.text && w.Since != cur.since {
			return fmt.Sprintf("session %s: the stored wait start moved from %d to %d while its text stayed %q", sid, cur.since, w.Since, w.Text)
		}
	}
	return ""
}

// askAt is the n-th row (modulo the count) that is an ask of a session, in
// creation order. Closed rows count: answering one must be refused.
func (r *lifeRig) askAt(n int) string {
	var ids []string
	for _, x := range r.rows() {
		if x.status != "joined" {
			ids = append(ids, x.id)
		}
	}
	if len(ids) == 0 {
		return ""
	}
	return ids[((n%len(ids))+len(ids))%len(ids)]
}

// epochOf is the latest epoch of a session that began at or before t.
func (r *lifeRig) epochOf(i int, t int64) *lifeEpoch {
	var best *lifeEpoch
	for _, e := range r.epochs {
		if e.sess == i && e.startNs <= t && (best == nil || e.startNs >= best.startNs) {
			best = e
		}
	}
	return best
}

// lifeFailure is a violation with the events that led to it.
type lifeFailure struct {
	step   int // the event that broke a rule
	found  int // the same step before the events were cut down
	msg    string
	events []lifeEvent
}

// runLife applies the events to a new store. stats, when given, gets the
// situations the run reached.
func runLife(t testing.TB, events []lifeEvent, stats map[string]int) *lifeFailure {
	r := newLifeRig(t)
	defer func() {
		for k, n := range r.seen {
			if stats != nil {
				stats[k] += n
			}
		}
	}()
	for i, e := range events {
		if msg := r.apply(e); msg != "" {
			return &lifeFailure{step: i + 1, found: i + 1, msg: msg, events: events[:i+1]}
		}
	}
	return nil
}

// shrinkLife drops events one at a time while the run still fails, so the
// replay that is printed is short.
func shrinkLife(t *testing.T, f *lifeFailure) *lifeFailure {
	best := f
	for i := len(best.events) - 2; i >= 0; i-- {
		try := append(append([]lifeEvent{}, best.events[:i]...), best.events[i+1:]...)
		if g := runLife(t, try, nil); g != nil {
			g.found = best.found
			best = g
		}
	}
	return best
}

func (f *lifeFailure) replay(seed int64) string {
	var b strings.Builder
	fmt.Fprintf(&b, "seed %d, found at step %d, cut down to %d events: %s\nreplay (paste into TestAskLifecycleReplay):\n", seed, f.found, len(f.events), f.msg)
	for _, e := range f.events {
		b.WriteString("\t" + e.String() + ",\n")
	}
	return b.String()
}

// lifePlan is the seeds and the length of one run. The fixed seeds are the
// default run; the long run adds many more.
func lifePlan(t *testing.T) (seeds []int64, steps int) {
	seeds = []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	steps = 120
	if only := os.Getenv("AGENT_NET_PROPERTY_SEED"); only != "" {
		n, err := strconv.ParseInt(only, 10, 64)
		if err != nil {
			t.Fatalf("AGENT_NET_PROPERTY_SEED: %v", err)
		}
		return []int64{n}, steps
	}
	if os.Getenv("AGENT_NET_PROPERTY_LONG") != "" {
		for s := int64(13); s <= 400; s++ {
			seeds = append(seeds, s)
		}
		steps = 400
	}
	// One random seed in every default run. It is printed with a failure.
	return append(seeds, time.Now().UnixNano()%1_000_000), steps
}

func TestAskLifecycleProperty(t *testing.T) {
	seeds, steps := lifePlan(t)
	stats := map[string]int{}
	for _, seed := range seeds {
		t.Run("seed"+strconv.FormatInt(seed, 10), func(t *testing.T) {
			if f := runLife(t, genLife(seed, steps), stats); f != nil {
				t.Fatal(shrinkLife(t, f).replay(seed))
			}
		})
	}
	t.Logf("situations reached over %d seeds of %d steps: %v", len(seeds), steps, stats)
	if len(seeds) < 2 || t.Failed() {
		return
	}
	// A run that never meets the situations the rules are about proves nothing.
	for _, want := range []string{
		"event with an open approval (announce)",
		"event with an open approval (busy)",
		"event with an open approval (tick)",
		"same wait reported again, ask open",
		"same wait reported again, ask settled",
		"blocked answer repeating a wait that has an ask",
	} {
		if stats[want] == 0 {
			t.Errorf("no run reached %q: the property is not tested there", want)
		}
	}
}

// TestAskLifecycleReplay holds the sequences that failed once. Each entry
// is the list a failing run printed.
func TestAskLifecycleReplay(t *testing.T) {
	cases := map[string][]lifeEvent{
		// An announce closed an approval (ch_b1ef03bb).
		"announce does not close an approval": {
			{opAsk, 0, 0, 1},
			{opAnnounce, 0, -1, taskPlain},
			{opAnnounce, 0, -1, taskPlain},
		},
		// A blocked answer repeating a wait that has an ask made a check row.
		"blocked answer repeating a wait makes no check row": {
			{opWait, 0, 0, 0},
			{opTick, 0, 1, 0},
			{opChief, 0, 0, 0},
			{opCheck, 0, 0, 0},
			{opAnnounce, 0, 0, taskBlocked},
		},
		// A duplicate that joined an ask the owner dismissed was closed with
		// no reason: the dismissal has no answer, and the join copied it (F1).
		"a duplicate of a dismissed ask is closed with a reason": {
			{opAsk, 0, 0, 0},
			{opAsk, 1, 0, 0},
			{opWait, 0, 0, 1},
			{opAsk, 2, 1, 0},
			{opAnswer, 0, 3, 2},
		},
	}
	for name, events := range cases {
		t.Run(name, func(t *testing.T) {
			if f := runLife(t, events, nil); f != nil {
				t.Fatal(f.replay(0))
			}
		})
	}
}

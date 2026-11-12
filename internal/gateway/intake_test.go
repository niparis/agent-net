package gateway

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/constant"
	"go/parser"
	"go/token"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// protocolConsts returns the string constants of the protocol package whose
// names start with prefix, read from the source, so a new kind is seen the
// moment it is declared.
func protocolConsts(t *testing.T, prefix string) map[string]string {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, "../protocol", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			for _, decl := range f.Decls {
				gd, ok := decl.(*ast.GenDecl)
				if !ok || gd.Tok != token.CONST {
					continue
				}
				for _, spec := range gd.Specs {
					vs := spec.(*ast.ValueSpec)
					for i, name := range vs.Names {
						if !strings.HasPrefix(name.Name, prefix) || i >= len(vs.Values) {
							continue
						}
						lit, ok := vs.Values[i].(*ast.BasicLit)
						if !ok || lit.Kind != token.STRING {
							continue
						}
						out[name.Name] = constant.StringVal(constant.MakeFromLiteral(lit.Value, lit.Kind, 0))
					}
				}
			}
		}
	}
	return out
}

// Every ask kind and every intake kind has a route (decision 0152). A new
// kind fails here until the table names its receiver.
func TestRouteTableCoversEveryKind(t *testing.T) {
	asks := protocolConsts(t, "AskKind")
	if len(asks) < 18 {
		t.Fatalf("read only %d ask kinds: %v", len(asks), asks)
	}
	for name, kind := range asks {
		if _, ok := routeOf(routeAsk, kind); !ok {
			t.Errorf("ask kind %s (%q) has no route", name, kind)
		}
	}
	intake := protocolConsts(t, "Intake")
	if len(intake) < 10 {
		t.Fatalf("read only %d intake kinds: %v", len(intake), intake)
	}
	for name, kind := range intake {
		r, ok := routeOf(routeIntake, kind)
		if !ok {
			t.Errorf("intake kind %s (%q) has no route", name, kind)
			continue
		}
		if r.To != routeCTO || (r.Wake != wakeNever && r.Wake != wakeAtOnce && r.Wake != wakeRepeatHour) {
			t.Errorf("intake kind %q: route %+v", kind, r)
		}
	}
	for _, kind := range []string{"step_task", "request_reminder", "idle_check", "worker_notice", "undelivered", "tools_disconnected", "member_blocked", "charter_request", "chief_batch"} {
		if _, ok := routeOf(routeMessage, kind); !ok {
			t.Errorf("message kind %q has no route", kind)
		}
	}
}

// Nothing automatic reaches the owner except sign-in and OAuth asks.
func TestNothingAutomaticReachesOwnerButSignInAndOAuth(t *testing.T) {
	for _, r := range routes {
		if r.Auto && r.To == routeOwner && r.Kind != protocol.AskKindSignIn && r.Kind != routeOAuth {
			t.Errorf("automatic %s %q goes to the owner", r.Class, r.Kind)
		}
	}
	// An OAuth consent skips a live chief; a CTO alert never goes to the owner.
	s, c, chief, _ := briefRig(t)
	_ = chief
	item, err := s.CreateInboxItem(protocol.InboxItem{Kind: protocol.AskKindApproval, SessionID: gatewayAsker, Node: "apps", FromKind: protocol.AskFromApps,
		SourceRef: "app-consent:x1", Text: "Allow Example to use agent-net?", Options: []string{"approve", "refuse"}})
	if err != nil {
		t.Fatal(err)
	}
	if next := askNext(t, s, item.ID); next != protocol.AskNextOwner {
		t.Fatalf("OAuth consent next = %q, want owner", next)
	}
	node, _, err := s.CreateNode("build", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.NodeLinkDown(node, c.t); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkSilentNodes(c.t.Add(16 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	// The chief's timeout keeps the alert.
	if err := s.releaseChiefAsks(c.t.Add(2 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	var owner int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM inbox_items WHERE status='open' AND next_who IN ('','owner') AND kind<>?`, protocol.AskKindApproval).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if owner != 0 {
		t.Fatalf("%d automatic items reached the owner", owner)
	}
}

// A repeat of the same kind and subject raises the count and the last seen
// time of one row.
func TestIntakeDeduplicatesByKindAndSubject(t *testing.T) {
	s, c := newTestStore(t)
	ev := intakeEvent{Kind: protocol.IntakePhonePaired, Subject: "phone-1", Text: "Paired a phone"}
	first, err := s.RecordIntake(ev)
	if err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(5 * time.Minute)
	ev.Text = "Paired a phone again"
	second, err := s.RecordIntake(ev)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID || second.Count != 2 || !second.FirstSeen.Equal(first.FirstSeen) || !second.LastSeen.Equal(c.t) || second.Text != "Paired a phone again" {
		t.Fatalf("repeat = %+v, first %+v", second, first)
	}
	if second.Severity != protocol.IntakeSeverityDigest || second.Alert != "" {
		t.Fatalf("a digest line woke the CTO: %+v", second)
	}
	var rows int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM cto_intake`).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("%d rows, %v", rows, err)
	}
	// Another subject is another row.
	if _, err := s.RecordIntake(intakeEvent{Kind: protocol.IntakePhonePaired, Subject: "phone-2", Text: "Paired"}); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM cto_intake`).Scan(&rows); err != nil || rows != 2 {
		t.Fatalf("%d rows, %v", rows, err)
	}
}

// The CTO's read returns the open entries, newest first.
func TestCTOIntakeReadsOpenEntriesNewestFirst(t *testing.T) {
	s, c := newTestStore(t)
	for _, subject := range []string{"old", "mid", "new"} {
		if _, err := s.RecordIntake(intakeEvent{Kind: protocol.IntakePhonePaired, Subject: subject, Text: subject}); err != nil {
			t.Fatal(err)
		}
		c.t = c.t.Add(time.Minute)
	}
	if err := s.CloseIntake(protocol.IntakePhonePaired, "mid", intakeResolved); err != nil {
		t.Fatal(err)
	}
	got, err := s.CTOIntake(0)
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 2 || len(got.Entries) != 2 || got.Entries[0].Subject != "new" || got.Entries[1].Subject != "old" {
		t.Fatalf("read = %+v", got)
	}
}

// intakeRig is a store with the agent-net repo and its lead, and a project
// shop with its own lead, so an alert can name the lead that owns the cause.
func intakeRig(t *testing.T) (*Store, *clock) {
	t.Helper()
	s, c := newTestStore(t)
	if _, err := s.db.Exec(`INSERT INTO agents(id,display_name,created_at,updated_at) VALUES('a-net','agent-net lead',0,0),('a-shop','shop lead',0,0);
		INSERT INTO repos(id,display_name,created_at,updated_at,owner_agent_id) VALUES('agent-net','agent-net',0,0,'a-net'),('shop','shop',0,0,'a-shop')`); err != nil {
		t.Fatal(err)
	}
	return s, c
}

func messageCount(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// requireOneCTOAlert checks the wake rule's result: exactly one open P1 CTO
// alert in the chief's queue, naming the lead, for the entry of kind and
// subject, and no message sent (so no CTO turn).
func requireOneCTOAlert(t *testing.T, s *Store, kind, subject, lead string, messagesBefore int) {
	t.Helper()
	rows, err := s.db.Query(`SELECT id,next_who,priority,why_you,detail FROM inbox_items WHERE kind=? AND status='open'`, protocol.AskKindCTOAlert)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var n int
	var id, next, why, detail string
	var priority int
	for rows.Next() {
		n++
		if err := rows.Scan(&id, &next, &priority, &why, &detail); err != nil {
			t.Fatal(err)
		}
	}
	if n != 1 {
		t.Fatalf("%d open CTO alerts, want 1", n)
	}
	if next != protocol.AskNextChief || priority != 1 || !strings.Contains(why, lead) {
		t.Fatalf("alert next=%q priority=%d why=%q, want chief, P1, lead %q", next, priority, why, lead)
	}
	e, err := openIntake(s.db, kind, subject)
	if err != nil {
		t.Fatalf("no open %s entry for %s: %v", kind, subject, err)
	}
	if e.Alert != id || e.Severity != protocol.IntakeSeverityWake || !strings.Contains(detail, e.ID) {
		t.Fatalf("entry %+v does not link alert %s (%q)", e, id, detail)
	}
	if got := messageCount(t, s); got != messagesBefore {
		t.Fatalf("%d messages sent; a wake is a chief item, never a turn", got-messagesBefore)
	}
}

func noCTOAlert(t *testing.T, s *Store) {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM inbox_items WHERE kind=?`, protocol.AskKindCTOAlert).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d CTO alerts, want none yet", n)
	}
}

func TestWakeMachineSilent(t *testing.T) {
	s, c := intakeRig(t)
	node, _, err := s.CreateNode("build", "linux")
	if err != nil {
		t.Fatal(err)
	}
	before := messageCount(t, s)
	if err := s.NodeLinkDown(node, c.t); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkSilentNodes(c.t.Add(14 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	noCTOAlert(t, s)
	if _, err := s.MarkSilentNodes(c.t.Add(16 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	requireOneCTOAlert(t, s, protocol.IntakeMachineSilent, node, "agent-net lead", before)
	// The machine returns: the entry and its alert close.
	if err := s.NodeLinkUp(node); err != nil {
		t.Fatal(err)
	}
	if _, err := openIntake(s.db, protocol.IntakeMachineSilent, node); err == nil {
		t.Fatal("entry still open after the machine returned")
	}
}

// A machine that reported sleep raises nothing (decision 0137).
func TestSleepingMachineRaisesNothing(t *testing.T) {
	s, c := intakeRig(t)
	node, _, err := s.CreateNode("mac", "darwin")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.NodeLinkDown(node, c.t); err != nil {
		t.Fatal(err)
	}
	if _, err := s.NodeSlept(node, c.t.Add(-time.Minute), time.Time{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkSilentNodes(c.t.Add(3 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	var entries int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM cto_intake`).Scan(&entries); err != nil || entries != 0 {
		t.Fatalf("%d intake entries for a sleeping machine: %v", entries, err)
	}
	noCTOAlert(t, s)
}

// reportTwiceOverAnHour sends the same daemon report twice, an hour and a
// minute apart, and checks that only the repeat wakes the CTO.
func reportTwiceOverAnHour(t *testing.T, kind, subject string) {
	t.Helper()
	s, c := intakeRig(t)
	node, _, err := s.CreateNode("research", "linux")
	if err != nil {
		t.Fatal(err)
	}
	before := messageCount(t, s)
	r := protocol.IntakeReport{Kind: kind, Subject: subject, Text: subject + " failed"}
	if err := s.ReportIntake(node, r); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(30 * time.Minute)
	if err := s.ReportIntake(node, r); err != nil {
		t.Fatal(err)
	}
	noCTOAlert(t, s)
	c.t = c.t.Add(31 * time.Minute)
	if err := s.ReportIntake(node, r); err != nil {
		t.Fatal(err)
	}
	requireOneCTOAlert(t, s, kind, "research/"+subject, "agent-net lead", before)
	// More repeats keep the one alert.
	c.t = c.t.Add(time.Hour)
	if err := s.ReportIntake(node, r); err != nil {
		t.Fatal(err)
	}
	requireOneCTOAlert(t, s, kind, "research/"+subject, "agent-net lead", before)
}

func TestWakeHarnessUpdateFailureRepeatsOverAnHour(t *testing.T) {
	reportTwiceOverAnHour(t, protocol.IntakeHarnessUpdate, "codex")
}

func TestWakeCompactionFailureRepeatsOverAnHour(t *testing.T) {
	reportTwiceOverAnHour(t, protocol.IntakeCompaction, "claude")
}

// A failed MCP repair reaches the queue from the daemon's ask; a repeat of
// the same ask is not counted, a new failure is.
func TestWakeMCPRepairFailureRepeatsOverAnHour(t *testing.T) {
	s, c := intakeRig(t)
	node, _, err := s.CreateNode("research", "linux")
	if err != nil {
		t.Fatal(err)
	}
	before := messageCount(t, s)
	open := func(ref string) {
		t.Helper()
		if _, err := s.OpenExternalAsk(node, protocol.AskOpen{Kind: protocol.AskKindCheck, Ref: "mcp:" + ref, From: "agent-net",
			Question: "The agent-net tools could not be connected in session " + ref + " after a restart."}); err != nil {
			t.Fatal(err)
		}
	}
	open("one")
	open("one")
	e, err := openIntake(s.db, protocol.IntakeMCPRepair, "research/agent-net")
	if err != nil || e.Count != 1 {
		t.Fatalf("entry %+v, %v", e, err)
	}
	c.t = c.t.Add(61 * time.Minute)
	open("two")
	requireOneCTOAlert(t, s, protocol.IntakeMCPRepair, "research/agent-net", "agent-net lead", before)
}

func TestWakeQuotaRepeatsOverAnHour(t *testing.T) {
	s, c := intakeRig(t)
	before := messageCount(t, s)
	ev := intakeEvent{Kind: protocol.IntakeQuota, Subject: "zai", Text: "zai ran out"}
	if _, err := s.RecordIntake(ev); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(61 * time.Minute)
	if _, err := s.RecordIntake(ev); err != nil {
		t.Fatal(err)
	}
	requireOneCTOAlert(t, s, protocol.IntakeQuota, "zai", "agent-net lead", before)
}

// Struggle is a CTO matter only across workers: two struggling workers stay
// with their callers, the third on the same model wakes the CTO.
func TestWakeStruggleAcrossWorkers(t *testing.T) {
	s, c := intakeRig(t)
	node, _, err := s.CreateNode("research", "linux")
	if err != nil {
		t.Fatal(err)
	}
	before := messageCount(t, s)
	run := func(session, model string) {
		t.Helper()
		if err := s.RecordWorkerRun(protocol.WorkerRun{NodeID: node, SessionID: session, Harness: "opencode", Model: model, Outcome: protocol.OutcomePartial,
			StartedAt: c.t.Add(-time.Hour).Unix(), EndedAt: c.t.Unix(), Struggle: &protocol.Struggle{Trace: "messages", Failed: 5}}); err != nil {
			t.Fatal(err)
		}
	}
	run("w1", "zai/glm-5")
	run("w1", "zai/glm-5") // the same worker again is not a second worker
	run("w2", "zai/glm-5")
	run("w3", "other/model")
	noCTOAlert(t, s)
	run("w4", "zai/glm-5")
	requireOneCTOAlert(t, s, protocol.IntakeStruggle, "zai/glm-5", "agent-net lead", before)
}

// The merge queue detector: K = 3 failed runs in a row on the train tip. Every
// change of a train cites the same run, and a flake does not count.
func TestWakeMergeQueueThreeFailedTrainRuns(t *testing.T) {
	s, c := intakeRig(t)
	before := messageCount(t, s)
	report := func(run, result string, flake bool) {
		t.Helper()
		e := protocol.Evidence{StepRef: protocol.StepRef{Step: protocol.StepTest}, Result: result, Commit: "abc", Fields: map[string]string{protocol.FieldRun: run}}
		if flake {
			e.Fields[protocol.FieldFlake] = protocol.FieldYes
		}
		tx, err := s.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if err := s.trainRunFeed(tx, "shop", e, c.t); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	report("ci/1", protocol.StepResultFail, false)
	report("ci/1", protocol.StepResultFail, false) // the second change of the same train
	report("ci/2", protocol.StepResultFail, true)  // a known flake
	report("ci/3", protocol.StepResultFail, false)
	noCTOAlert(t, s)
	// A pass in between starts the count again.
	report("ci/4", protocol.StepResultPass, false)
	report("ci/5", protocol.StepResultFail, false)
	report("ci/6", protocol.StepResultFail, false)
	noCTOAlert(t, s)
	report("ci/7", protocol.StepResultFail, false)
	requireOneCTOAlert(t, s, protocol.IntakeMergeQueue, "shop", "shop lead", before)
	// The next pass closes the entry and its alert.
	report("ci/8", protocol.StepResultPass, false)
	if _, err := openIntake(s.db, protocol.IntakeMergeQueue, "shop"); err == nil {
		t.Fatal("entry still open after a pass")
	}
	var open int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM inbox_items WHERE kind=? AND status='open'`, protocol.AskKindCTOAlert).Scan(&open); err != nil || open != 0 {
		t.Fatalf("%d alerts open after the pass: %v", open, err)
	}
}

// A digest kind never wakes the CTO, however often it repeats.
func TestDigestKindsNeverWake(t *testing.T) {
	s, c := intakeRig(t)
	for _, kind := range []string{protocol.IntakeClaudeMouse, protocol.IntakePhonePaired, protocol.IntakePickerOverride} {
		for i := 0; i < 3; i++ {
			if _, err := s.RecordIntake(intakeEvent{Kind: kind, Subject: "x", Text: "x"}); err != nil {
				t.Fatal(err)
			}
			c.t = c.t.Add(time.Hour)
		}
	}
	noCTOAlert(t, s)
}

// One quota exhaustion event is one thread: the chief's stuck-work item links
// the provider's CTO entry, and closing the cause closes both.
func TestQuotaExhaustionIsOneThread(t *testing.T) {
	s, c := intakeRig(t)
	stuck := func() protocol.IntakeEntry {
		t.Helper()
		tx, err := s.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		e, err := s.quotaStuck(tx, "zai", "zai is exhausted (5h; resets 14:00)", c.t)
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		return e
	}
	first := stuck()
	c.t = c.t.Add(61 * time.Minute)
	second := stuck()
	if second.ID != first.ID || second.Stuck != first.Stuck || first.Stuck == "" {
		t.Fatalf("two threads: %+v then %+v", first, second)
	}
	var kind, next, detail string
	if err := s.db.QueryRow(`SELECT kind,next_who,detail FROM inbox_items WHERE id=?`, first.Stuck).Scan(&kind, &next, &detail); err != nil {
		t.Fatal(err)
	}
	if kind != protocol.AskKindQuotaStuck || next != protocol.AskNextChief || !strings.Contains(detail, first.ID) {
		t.Fatalf("stuck item kind=%q next=%q detail=%q", kind, next, detail)
	}
	var items int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM inbox_items WHERE kind=?`, protocol.AskKindQuotaStuck).Scan(&items); err != nil || items != 1 {
		t.Fatalf("%d stuck items, want 1: %v", items, err)
	}
	if second.Alert == "" {
		t.Fatal("an exhaustion that repeats for over an hour did not wake the CTO")
	}
	// The provider has quota again: the entry, the stuck item and the alert close.
	if err := s.CloseIntake(protocol.IntakeQuota, "zai", intakeResolved); err != nil {
		t.Fatal(err)
	}
	var open int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM inbox_items WHERE id IN (?,?) AND status='open'`, second.Stuck, second.Alert).Scan(&open); err != nil || open != 0 {
		t.Fatalf("%d thread items still open: %v", open, err)
	}
	if _, err := openIntake(s.db, protocol.IntakeQuota, "zai"); err == nil {
		t.Fatal("entry still open")
	}
}

// The quota feed reads each usage report: an exhausted provider opens its
// entry and a reading with room again closes it.
func TestQuotaFeedFollowsUsageReports(t *testing.T) {
	s, _ := newTestStore(t)
	g := &Gateway{store: s}
	nc := &nodeConn{id: "research"}
	send := func(exhausted bool) {
		t.Helper()
		rep := protocol.UsageReport{Quotas: &protocol.Quotas{Providers: []protocol.QuotaProvider{{Provider: "zai",
			Windows: []protocol.QuotaWindow{{ID: "5h", Kind: "rolling", PercentUsed: 40, Exhausted: exhausted}}}}}}
		raw, _ := json.Marshal(rep)
		if _, err := g.handle(context.Background(), nc, "node.usage", raw); err != nil {
			t.Fatal(err)
		}
	}
	send(true)
	if _, err := openIntake(g.store.db, protocol.IntakeQuota, "zai"); err != nil {
		t.Fatalf("no quota entry after an exhausted reading: %v", err)
	}
	send(false)
	if _, err := openIntake(g.store.db, protocol.IntakeQuota, "zai"); err == nil {
		t.Fatal("quota entry still open after a reading with room")
	}
}

// A daemon report takes the machine into its subject; a kind the gateway
// detects itself is refused.
func TestIntakeReportFrame(t *testing.T) {
	s, _ := newTestStore(t)
	g := &Gateway{store: s}
	node, _, err := g.store.CreateNode("research", "linux")
	if err != nil {
		t.Fatal(err)
	}
	nc := &nodeConn{id: node}
	raw, _ := json.Marshal(protocol.IntakeReport{Kind: protocol.IntakeHarnessUpdate, Subject: "codex", Text: "codex update failed"})
	if _, err := g.handle(context.Background(), nc, "intake.report", raw); err != nil {
		t.Fatal(err)
	}
	out, err := g.handle(context.Background(), nc, protocol.FrameIntakeFind, nil)
	if err != nil {
		t.Fatal(err)
	}
	res := out.(protocol.IntakeFindResult)
	if res.Total != 1 || res.Entries[0].Subject != "research/codex" {
		t.Fatalf("read = %+v", res)
	}
	raw, _ = json.Marshal(protocol.IntakeReport{Kind: protocol.IntakeMachineSilent, Subject: "x", Text: "x"})
	if _, err := g.handle(context.Background(), nc, "intake.report", raw); err == nil {
		t.Fatal("a daemon reported a kind only the gateway detects")
	}
	raw, _ = json.Marshal(protocol.IntakeReport{Kind: protocol.IntakeHarnessUpdate, Subject: "codex", Resolved: true})
	if _, err := g.handle(context.Background(), nc, "intake.report", raw); err != nil {
		t.Fatal(err)
	}
	if res, _ := g.store.CTOIntake(0); res.Total != 0 {
		t.Fatalf("resolved report left %d entries", res.Total)
	}
}

// The morning brief gains one system-health line and the day's recorded
// decisions.
func TestBriefCarriesHealthLineAndDecisions(t *testing.T) {
	s, c, chief, _ := briefRig(t)
	if _, err := s.RecordIntake(intakeEvent{Kind: protocol.IntakePhonePaired, Subject: "p", Text: "Paired"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO standing_orders(id,text,saved_at,source,kind) VALUES
		('so_1','Q: merge the picker fix? / A: yes',?,'owner','answer'),
		('so_2','an old decision',?,'owner','answer'),
		('so_3','Never use the paid search',?,'owner','order')`,
		c.t.Add(-2*time.Hour).Unix(), c.t.Add(-30*time.Hour).Unix(), c.t.Add(-time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	res, err := s.putOwnerBrief(chief, protocol.OwnerBriefArgs{Body: "Two fixes shipped.", Date: "2026-10-28"}, c.t)
	if err != nil {
		t.Fatal(err)
	}
	var text string
	if err := s.db.QueryRow(`SELECT text FROM inbox_items WHERE id=?`, res.ItemID).Scan(&text); err != nil {
		t.Fatal(err)
	}
	want := "Two fixes shipped.\n\nSystem health: 1 open alert, none woke the CTO (phone paired).\nDecisions recorded today:\n- Q: merge the picker fix? / A: yes"
	if text != want {
		t.Fatalf("brief =\n%s\nwant\n%s", text, want)
	}
}

// Work that fails on quota after it started joins the provider's thread: the
// same quota_stuck item, linked to the same queue entry, never a second item.
func TestQuotaFailureMidRunJoinsTheProviderThread(t *testing.T) {
	s, c := intakeRig(t)
	node, _, err := s.CreateNode("research", "linux")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	refused, err := s.quotaStuck(tx, "zai", "zai is exhausted (5h; resets 14:00)", c.t)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(10 * time.Minute)
	for _, session := range []string{"w1", "w2"} {
		if err := s.RecordWorkerRun(protocol.WorkerRun{NodeID: node, SessionID: session, Harness: "opencode", Model: "zai/glm-5",
			Outcome: protocol.OutcomeFailed, Cause: protocol.CauseQuotaAuth, EndedAt: c.t.Unix()}); err != nil {
			t.Fatal(err)
		}
	}
	e, err := openIntake(s.db, protocol.IntakeQuota, "zai")
	if err != nil {
		t.Fatal(err)
	}
	if e.ID != refused.ID || e.Stuck != refused.Stuck || e.Count != 3 {
		t.Fatalf("mid-run failures did not join the thread: %+v, first %+v", e, refused)
	}
	var items int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM inbox_items WHERE kind=?`, protocol.AskKindQuotaStuck).Scan(&items); err != nil || items != 1 {
		t.Fatalf("%d stuck items, want 1: %v", items, err)
	}
	// A failure for another reason is not a quota thread.
	if err := s.RecordWorkerRun(protocol.WorkerRun{NodeID: node, SessionID: "w3", Harness: "opencode", Model: "other/model",
		Outcome: protocol.OutcomeFailed, Cause: protocol.CauseHarness, EndedAt: c.t.Unix()}); err != nil {
		t.Fatal(err)
	}
	if res, _ := s.CTOIntake(0); res.Total != 1 {
		t.Fatalf("%d entries, want only the zai thread", res.Total)
	}
}

// A kind the route table does not list keeps today's default: the chief.
func TestUnroutedAskKindGoesToTheChief(t *testing.T) {
	s, _, chief, other := briefRig(t)
	_ = chief
	saved := routeIndex[routeAsk+":"+protocol.AskKindHeld]
	delete(routeIndex, routeAsk+":"+protocol.AskKindHeld)
	t.Cleanup(func() { routeIndex[routeAsk+":"+protocol.AskKindHeld] = saved })
	item, err := s.CreateInboxItem(protocol.InboxItem{Kind: protocol.AskKindHeld, SessionID: other, Text: "held request"})
	if err != nil {
		t.Fatalf("an unrouted kind was refused: %v", err)
	}
	if next := askNext(t, s, item.ID); next != protocol.AskNextChief {
		t.Fatalf("unrouted kind next = %q, want chief", next)
	}
}

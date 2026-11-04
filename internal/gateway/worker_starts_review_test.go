package gateway

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Finding 1: Unbound queued start.
// verifyStart with an unbound tracked Ref must not accept an arbitrary session for daemons that track binding (>= v0.4.131).
func TestUnboundQueuedStartRefused(t *testing.T) {
	f, worker := newRelayFixture(t, func(f *startsFixture, w string) func(protocol.Header, []byte) error {
		return func(h protocol.Header, raw []byte) error { return nil }
	})
	if err := f.s.SetNodeHello(f.nodeA, "nodeA", "v0.4.131"); err != nil {
		t.Fatal(err)
	}

	// Track a start that has no session bound yet.
	if err := f.s.trackWorkerStart("q_unbound", f.nodeA, f.b, "shop", "codex", ""); err != nil {
		t.Fatal(err)
	}

	// Calling queued_start with an unbound Ref must be refused.
	err := f.frame(t, f.nodeA, "worker.queued_start", f.queuedStart(worker, "q_unbound"))
	if err == nil || !errors.As(err, new(Rejection)) {
		t.Fatalf("unbound queued start was accepted: %v", err)
	}

	// Once bound to this worker session, it succeeds.
	if err := f.s.startSession(f.nodeA, "q_unbound", worker); err != nil {
		t.Fatal(err)
	}
	if err := f.frame(t, f.nodeA, "worker.queued_start", f.queuedStart(worker, "q_unbound")); err != nil {
		t.Fatalf("bound queued start was refused: %v", err)
	}
}

// A v0.4.127 daemon has no StartRef on WorkerInfo and queued launch answers have an empty SessionID.
// verifyStart must accept the start on v0.4.127 when rs=="" and close it.
func TestQueuedStartAcceptedForV04127DaemonWithoutStartRef(t *testing.T) {
	relayed := false
	f, worker := newRelayFixture(t, func(f *startsFixture, w string) func(protocol.Header, []byte) error {
		return func(h protocol.Header, raw []byte) error {
			relayed = true
			return nil
		}
	})
	if err := f.s.SetNodeHello(f.nodeA, "nodeA", "v0.4.127"); err != nil {
		t.Fatal(err)
	}

	// Gateway tracks a queued start (session_id is empty).
	if err := f.s.trackWorkerStart("q_127", f.nodeA, f.b, "shop", "codex", ""); err != nil {
		t.Fatal(err)
	}

	// v0.4.127 daemon sends worker.queued_start with tracked Ref and worker session ID.
	if err := f.frame(t, f.nodeA, "worker.queued_start", f.queuedStart(worker, "q_127")); err != nil {
		t.Fatalf("v0.4.127 queued_start refused: %v", err)
	}
	if !relayed {
		t.Fatalf("task was not relayed")
	}

	var state, sessID string
	f.s.db.QueryRow(`SELECT state, session_id FROM worker_starts WHERE ref='q_127'`).Scan(&state, &sessID)
	if state != "closed" {
		t.Errorf("start q_127 state = %q, want closed", state)
	}
	if sessID != worker {
		t.Errorf("start q_127 session_id = %q, want %q", sessID, worker)
	}
}

// A v0.4.129 daemon has no StartRef on WorkerInfo and queued launch answers have an empty SessionID.
// verifyStart must accept the start on v0.4.129 when rs=="" and close it.
func TestQueuedStartAcceptedForV04129DaemonWithoutStartRef(t *testing.T) {
	relayed := false
	f, worker := newRelayFixture(t, func(f *startsFixture, w string) func(protocol.Header, []byte) error {
		return func(h protocol.Header, raw []byte) error {
			relayed = true
			return nil
		}
	})
	if err := f.s.SetNodeHello(f.nodeA, "nodeA", "v0.4.129"); err != nil {
		t.Fatal(err)
	}

	// Gateway tracks a queued start (session_id is empty).
	if err := f.s.trackWorkerStart("q_129", f.nodeA, f.b, "shop", "codex", ""); err != nil {
		t.Fatal(err)
	}

	// v0.4.129 daemon sends worker.queued_start with tracked Ref and worker session ID.
	if err := f.frame(t, f.nodeA, "worker.queued_start", f.queuedStart(worker, "q_129")); err != nil {
		t.Fatalf("v0.4.129 queued_start refused: %v", err)
	}
	if !relayed {
		t.Fatalf("task was not relayed")
	}

	var state, sessID string
	f.s.db.QueryRow(`SELECT state, session_id FROM worker_starts WHERE ref='q_129'`).Scan(&state, &sessID)
	if state != "closed" {
		t.Errorf("start q_129 state = %q, want closed", state)
	}
	if sessID != worker {
		t.Errorf("start q_129 session_id = %q, want %q", sessID, worker)
	}
}

// A v0.4.130 daemon has no StartRef on WorkerInfo and queued launch answers have an empty SessionID.
// verifyStart must accept the start on v0.4.130 when rs=="" and close it.
func TestQueuedStartAcceptedForV04130DaemonWithoutStartRef(t *testing.T) {
	relayed := false
	f, worker := newRelayFixture(t, func(f *startsFixture, w string) func(protocol.Header, []byte) error {
		return func(h protocol.Header, raw []byte) error {
			relayed = true
			return nil
		}
	})
	if err := f.s.SetNodeHello(f.nodeA, "nodeA", "v0.4.130"); err != nil {
		t.Fatal(err)
	}

	// Gateway tracks a queued start (session_id is empty).
	if err := f.s.trackWorkerStart("q_130", f.nodeA, f.b, "shop", "codex", ""); err != nil {
		t.Fatal(err)
	}

	// v0.4.130 daemon sends worker.queued_start with tracked Ref and worker session ID.
	if err := f.frame(t, f.nodeA, "worker.queued_start", f.queuedStart(worker, "q_130")); err != nil {
		t.Fatalf("v0.4.130 queued_start refused: %v", err)
	}
	if !relayed {
		t.Fatalf("task was not relayed")
	}

	var state, sessID string
	f.s.db.QueryRow(`SELECT state, session_id FROM worker_starts WHERE ref='q_130'`).Scan(&state, &sessID)
	if state != "closed" {
		t.Errorf("start q_130 state = %q, want closed", state)
	}
	if sessID != worker {
		t.Errorf("start q_130 session_id = %q, want %q", sessID, worker)
	}
}

// Finding 2: Untracked Ref fallback.
// A node whose daemon version tracks Refs must not use the untracked fallback.
func TestNewDaemonCannotUseUntrackedRefFallback(t *testing.T) {
	f, worker := newRelayFixture(t, func(f *startsFixture, w string) func(protocol.Header, []byte) error {
		return func(h protocol.Header, raw []byte) error { return nil }
	})
	// New daemon version that tracks starts
	if err := f.s.SetNodeHello(f.nodeA, "nodeA", "v0.4.127"); err != nil {
		t.Fatal(err)
	}

	// Gateway tracks an open start for this machine and owner.
	if err := f.s.trackWorkerStart("q_open", f.nodeA, f.b, "shop", "codex", ""); err != nil {
		t.Fatal(err)
	}

	// Empty ref must be refused for a new-version node.
	err := f.frame(t, f.nodeA, "worker.queued_start", f.queuedStart(worker, ""))
	if err == nil || !errors.As(err, new(Rejection)) {
		t.Fatalf("empty ref accepted for v0.4.127 daemon: %v", err)
	}

	// Untracked ref must be refused for a new-version node.
	err = f.frame(t, f.nodeA, "worker.queued_start", f.queuedStart(worker, "q_untracked"))
	if err == nil || !errors.As(err, new(Rejection)) {
		t.Fatalf("untracked ref accepted for v0.4.127 daemon: %v", err)
	}

	// An older daemon (v0.4.126) is allowed to fall back.
	if err := f.s.SetNodeHello(f.nodeA, "nodeA", "v0.4.126"); err != nil {
		t.Fatal(err)
	}
	if err := f.frame(t, f.nodeA, "worker.queued_start", f.queuedStart(worker, "q_untracked")); err != nil {
		t.Fatalf("untracked ref refused for legacy daemon: %v", err)
	}
}

// Finding 3: Legacy daemons.
// Keep mapping from gateway Ref to legacy queue ID, reconcile records without Ref,
// and close unmatched records after bounded time without duplicate notice.
func TestLegacyDaemonQueueMappingAndReconciliation(t *testing.T) {
	f, worker := newRelayFixture(t, func(f *startsFixture, w string) func(protocol.Header, []byte) error {
		return func(h protocol.Header, raw []byte) error { return nil }
	})
	if err := f.s.SetNodeHello(f.nodeA, "nodeA", "v0.4.126"); err != nil {
		t.Fatal(err)
	}

	// 3a. Mapping legacy queue ID and closing via queued_start with legacy ID or empty Ref.
	if err := f.s.trackWorkerStart("q_gw_1", f.nodeA, f.b, "shop", "codex", ""); err != nil {
		t.Fatal(err)
	}
	if err := f.s.recordLegacyQueueID(f.nodeA, "q_gw_1", "q_legacy_1"); err != nil {
		t.Fatal(err)
	}
	// Older daemon sends queued_start without Ref. It reconciles by node, owner, project and closes.
	if err := f.frame(t, f.nodeA, "worker.queued_start", f.queuedStart(worker, "")); err != nil {
		t.Fatalf("legacy queued_start without ref: %v", err)
	}
	var state string
	f.s.db.QueryRow(`SELECT state FROM worker_starts WHERE ref='q_gw_1'`).Scan(&state)
	if state != "closed" {
		t.Errorf("start q_gw_1 state = %q, want closed", state)
	}

	// 3b. Legacy queue cancel via legacy ID.
	if err := f.s.trackWorkerStart("q_gw_2", f.nodeA, f.b, "shop", "codex", ""); err != nil {
		t.Fatal(err)
	}
	if err := f.s.recordLegacyQueueID(f.nodeA, "q_gw_2", "q_legacy_2"); err != nil {
		t.Fatal(err)
	}
	if err := f.s.abandonStart(f.nodeA, "q_legacy_2"); err != nil {
		t.Fatal(err)
	}
	f.s.db.QueryRow(`SELECT state FROM worker_starts WHERE ref='q_gw_2'`).Scan(&state)
	if state != "not_started" {
		t.Errorf("cancelled start q_gw_2 state = %q, want not_started", state)
	}

	// 3c. worker.not_started without Ref reconciles and closes without duplicate notice.
	if err := f.s.trackWorkerStart("q_gw_3", f.nodeA, f.b, "shop", "codex", ""); err != nil {
		t.Fatal(err)
	}
	if err := f.frame(t, f.nodeA, "worker.not_started", protocol.WorkerNotStarted{Owner: f.b, Project: "shop", Harness: "codex", Reason: "failed"}); err != nil {
		t.Fatal(err)
	}
	f.s.db.QueryRow(`SELECT state FROM worker_starts WHERE ref='q_gw_3'`).Scan(&state)
	if state != "not_started" {
		t.Errorf("start q_gw_3 state = %q, want not_started", state)
	}
	if ns := f.notices(t, f.b); len(ns) != 0 {
		t.Errorf("legacy not_started report produced unexpected notice: %v", ns)
	}

	// 3d. Unmatched legacy start closes after bounded time without notice.
	if err := f.s.trackWorkerStart("q_gw_stale", f.nodeA, f.b, "shop", "codex", ""); err != nil {
		t.Fatal(err)
	}
	if err := f.s.recordLegacyQueueID(f.nodeA, "q_gw_stale", "q_legacy_stale"); err != nil {
		t.Fatal(err)
	}
	f.clock.t = f.clock.t.Add(25 * time.Hour)
	notices, err := f.s.ExpireStarts(0, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(notices) != 0 {
		t.Errorf("unmatched legacy expiry produced notice: %v", notices)
	}
	f.s.db.QueryRow(`SELECT state FROM worker_starts WHERE ref='q_gw_stale'`).Scan(&state)
	if state != "not_started" {
		t.Errorf("stale legacy start state = %q, want not_started", state)
	}
}

// Legacy starts expiry based on semver vs lexicographical string comparison.
// Node versions v0.4.99 and 0.4.120 expire after the legacy timeout without a notice,
// while v0.4.127 does not.
func TestLegacyStartsVersionExpiry(t *testing.T) {
	f := newStartsFixture(t)
	node99, _, err := f.s.CreateNode("h99", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.SetNodeHello(node99, "h99", "v0.4.99"); err != nil {
		t.Fatal(err)
	}

	node120, _, err := f.s.CreateNode("h120", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.SetNodeHello(node120, "h120", "0.4.120"); err != nil {
		t.Fatal(err)
	}

	node127, _, err := f.s.CreateNode("h127", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.SetNodeHello(node127, "h127", "v0.4.127"); err != nil {
		t.Fatal(err)
	}

	if err := f.s.trackWorkerStart("q_99", node99, f.a, "shop", "codex", ""); err != nil {
		t.Fatal(err)
	}
	if err := f.s.trackWorkerStart("q_120", node120, f.a, "shop", "codex", ""); err != nil {
		t.Fatal(err)
	}
	if err := f.s.trackWorkerStart("q_127", node127, f.a, "shop", "codex", ""); err != nil {
		t.Fatal(err)
	}

	f.clock.t = f.clock.t.Add(25 * time.Hour)
	notices, err := f.s.ExpireStarts(0, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(notices) != 0 {
		t.Fatalf("unexpected notices: %v", notices)
	}

	stateOf := func(ref string) string {
		var st string
		f.s.db.QueryRow(`SELECT state FROM worker_starts WHERE ref=?`, ref).Scan(&st)
		return st
	}

	if st := stateOf("q_99"); st != "not_started" {
		t.Errorf("q_99 state = %q, want not_started", st)
	}
	if st := stateOf("q_120"); st != "not_started" {
		t.Errorf("q_120 state = %q, want not_started", st)
	}
	if st := stateOf("q_127"); st != "open" {
		t.Errorf("q_127 state = %q, want open", st)
	}
}

// Finding 4: Launch timeout = refusal.
// A launch call that fails by deadline or disconnect leaves the record open as "unknown"
// until a started or not-started report arrives, or until bounded timeout closes it with one notice.
// A definite refusal closes at once.
func TestLaunchCallTimeoutLeavesStartUnknownUntilReport(t *testing.T) {
	f := newStartsFixture(t)
	// 4a. Definite refusal closes at once without notice.
	if err := f.s.trackWorkerStart("q_refused", f.nodeA, f.a, "shop", "codex", ""); err != nil {
		t.Fatal(err)
	}
	f.g.abandon(f.nodeA, "q_refused")
	var state string
	f.s.db.QueryRow(`SELECT state FROM worker_starts WHERE ref='q_refused'`).Scan(&state)
	if state != "not_started" {
		t.Fatalf("refused start state = %q, want not_started", state)
	}
	if ns := f.notices(t, f.a); len(ns) != 0 {
		t.Errorf("definite refusal produced notice: %v", ns)
	}

	// 4b. Disconnect / timeout leaves start open as unknown.
	if err := f.s.trackWorkerStart("q_timeout", f.nodeA, f.a, "shop", "codex", ""); err != nil {
		t.Fatal(err)
	}
	if err := f.s.markStartUnknown(f.nodeA, "q_timeout"); err != nil {
		t.Fatal(err)
	}
	var launchState string
	f.s.db.QueryRow(`SELECT state, launch_state FROM worker_starts WHERE ref='q_timeout'`).Scan(&state, &launchState)
	if state != "open" || launchState != "unknown" {
		t.Fatalf("timeout start state=%q, launch_state=%q; want open/unknown", state, launchState)
	}

	// A later not_started report finds the open unknown start and sends ONE notice.
	if err := f.frame(t, f.nodeA, "worker.not_started", protocol.WorkerNotStarted{Ref: "q_timeout", Reason: "timed out locally"}); err != nil {
		t.Fatal(err)
	}
	f.s.db.QueryRow(`SELECT state FROM worker_starts WHERE ref='q_timeout'`).Scan(&state)
	if state != "not_started" {
		t.Fatalf("reported unknown start state = %q, want not_started", state)
	}
	if ns := f.notices(t, f.a); len(ns) != 1 {
		t.Fatalf("expected 1 notice, got %d", len(ns))
	}

	// 4c. Unknown start that never reports closes after bounded timeout with ONE notice.
	if err := f.s.trackWorkerStart("q_silent", f.nodeA, f.a, "shop", "codex", ""); err != nil {
		t.Fatal(err)
	}
	if err := f.s.markStartUnknown(f.nodeA, "q_silent"); err != nil {
		t.Fatal(err)
	}
	f.clock.t = f.clock.t.Add(15 * time.Minute)
	notices, err := f.s.ExpireStarts(10*time.Minute, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(notices) != 1 {
		t.Fatalf("expected 1 expiry notice, got %d", len(notices))
	}
	f.s.db.QueryRow(`SELECT state FROM worker_starts WHERE ref='q_silent'`).Scan(&state)
	if state != "not_started" {
		t.Fatalf("expired silent start state = %q, want not_started", state)
	}
	if ns := f.notices(t, f.a); len(ns) != 2 { // 1 from 4b, 1 from 4c
		t.Fatalf("expected 2 total notices for f.a, got %d", len(ns))
	}
}

// Finding 2 review fix: an unknown start that ExpireStarts declared not_started (with a notice)
// must be refused on a late worker.queued_start, relaying no task and generating no second notice.
func TestExpiredUnknownStartRefusesQueuedStart(t *testing.T) {
	relayed := false
	f, worker := newRelayFixture(t, func(f *startsFixture, w string) func(protocol.Header, []byte) error {
		return func(h protocol.Header, raw []byte) error {
			relayed = true
			return nil
		}
	})
	if err := f.s.SetNodeHello(f.nodeA, "nodeA", "v0.4.131"); err != nil {
		t.Fatal(err)
	}

	// Start begins as unknown launch.
	if err := f.s.trackWorkerStart("q_exp", f.nodeA, f.b, "shop", "codex", ""); err != nil {
		t.Fatal(err)
	}
	if err := f.s.markStartUnknown(f.nodeA, "q_exp"); err != nil {
		t.Fatal(err)
	}

	// Expire the unknown start: it sends one not_started notice.
	f.clock.t = f.clock.t.Add(15 * time.Minute)
	notices, err := f.s.ExpireStarts(10*time.Minute, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(notices) != 1 {
		t.Fatalf("expected 1 notice from ExpireStarts, got %d", len(notices))
	}
	if ns := f.notices(t, f.b); len(ns) != 1 {
		t.Fatalf("expected 1 notice stored for owner, got %d", len(ns))
	}

	// Bind session to simulate late worker start attempt.
	if err := f.s.startSession(f.nodeA, "q_exp", worker); err != nil {
		t.Fatal(err)
	}

	// Late worker.queued_start arrives: must be refused.
	err = f.frame(t, f.nodeA, "worker.queued_start", f.queuedStart(worker, "q_exp"))
	if err == nil || !errors.As(err, new(Rejection)) {
		t.Fatalf("late queued_start on expired start was accepted: %v", err)
	}
	if relayed {
		t.Fatalf("task was unexpectedly relayed for expired start")
	}

	// Daemon reports not_started on refusal.
	if err := f.frame(t, f.nodeA, "worker.not_started", protocol.WorkerNotStarted{Ref: "q_exp", Reason: "refused"}); err != nil {
		t.Fatal(err)
	}

	// Verify exactly one notice in total.
	if ns := f.notices(t, f.b); len(ns) != 1 {
		t.Fatalf("expected exactly 1 notice in total, got %d", len(ns))
	}
}

// Finding 1 review fix (race): claim the start atomically before relay so ExpireStarts
// and declareNotStarted do not interfere while relay is in-flight.
func TestQueuedStartRaceWithExpireStarts(t *testing.T) {
	inTask := make(chan struct{})
	resumeTask := make(chan struct{})
	var relayed atomic.Int32

	f, worker := newRelayFixture(t, func(f *startsFixture, w string) func(protocol.Header, []byte) error {
		return func(h protocol.Header, raw []byte) error {
			relayed.Add(1)
			close(inTask)
			<-resumeTask
			return nil
		}
	})
	if err := f.s.SetNodeHello(f.nodeA, "nodeA", "v0.4.131"); err != nil {
		t.Fatal(err)
	}

	if err := f.s.trackWorkerStart("q_race", f.nodeA, f.b, "shop", "codex", ""); err != nil {
		t.Fatal(err)
	}
	if err := f.s.markStartUnknown(f.nodeA, "q_race"); err != nil {
		t.Fatal(err)
	}
	if err := f.s.startSession(f.nodeA, "q_race", worker); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		done <- f.frame(t, f.nodeA, "worker.queued_start", f.queuedStart(worker, "q_race"))
	}()

	// Wait until relay has started and blocked in worker.task.
	<-inTask

	// Check OpenStarts counts 'starting' as open.
	openCount, err := f.s.OpenStarts()
	if err != nil {
		t.Fatal(err)
	}
	if openCount != 1 {
		t.Errorf("OpenStarts during starting = %d, want 1", openCount)
	}

	// While relay is in-flight ('starting'), ExpireStarts runs.
	f.clock.t = f.clock.t.Add(15 * time.Minute)
	notices, err := f.s.ExpireStarts(10*time.Minute, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(notices) != 0 {
		t.Errorf("ExpireStarts produced notices while start was starting: %v", notices)
	}

	// Let relay finish.
	close(resumeTask)
	if err := <-done; err != nil {
		t.Fatalf("queued_start failed: %v", err)
	}

	// Verify state is closed.
	var state string
	f.s.db.QueryRow(`SELECT state FROM worker_starts WHERE ref='q_race'`).Scan(&state)
	if state != "closed" {
		t.Errorf("state after relay = %q, want closed", state)
	}

	// Verify no not-started notice was emitted.
	if ns := f.notices(t, f.b); len(ns) != 0 {
		t.Errorf("unexpected not-started notices: %v", ns)
	}
}

// Finding 2 review fix (idempotency): a worker.queued_start for a start already 'closed'
// with the same session_id must answer success without relaying again. A different session stays refused.
func TestQueuedStartIdempotentWhenAlreadyClosed(t *testing.T) {
	var relayed atomic.Int32
	f, worker := newRelayFixture(t, func(f *startsFixture, w string) func(protocol.Header, []byte) error {
		return func(h protocol.Header, raw []byte) error {
			relayed.Add(1)
			return nil
		}
	})
	if err := f.s.SetNodeHello(f.nodeA, "nodeA", "v0.4.131"); err != nil {
		t.Fatal(err)
	}

	if err := f.s.trackWorkerStart("q_idem", f.nodeA, f.b, "shop", "codex", ""); err != nil {
		t.Fatal(err)
	}
	if err := f.s.startSession(f.nodeA, "q_idem", worker); err != nil {
		t.Fatal(err)
	}

	// First queued_start: succeeds and closes the start.
	if err := f.frame(t, f.nodeA, "worker.queued_start", f.queuedStart(worker, "q_idem")); err != nil {
		t.Fatalf("first queued_start failed: %v", err)
	}
	if relayed.Load() != 1 {
		t.Fatalf("relayed count = %d, want 1", relayed.Load())
	}

	// Second queued_start with same session_id: answers success without relaying again.
	if err := f.frame(t, f.nodeA, "worker.queued_start", f.queuedStart(worker, "q_idem")); err != nil {
		t.Fatalf("second queued_start with same session_id failed: %v", err)
	}
	if relayed.Load() != 1 {
		t.Fatalf("relayed count after repeat = %d, want 1", relayed.Load())
	}

	// Third queued_start with a DIFFERENT session_id: must be refused!
	diffWorker, err := f.s.UpsertSession(f.nodeA, protocol.SessionInfo{Harness: "codex", NativeID: "w2", Name: "w2",
		Worker: &protocol.WorkerInfo{ID: "w_2", Owner: f.b}})
	if err != nil {
		t.Fatal(err)
	}
	err = f.frame(t, f.nodeA, "worker.queued_start", f.queuedStart(diffWorker, "q_idem"))
	if err == nil || !errors.As(err, new(Rejection)) {
		t.Fatalf("queued_start with different session_id on closed start was accepted: %v", err)
	}
	if relayed.Load() != 1 {
		t.Fatalf("relayed count after different session = %d, want 1", relayed.Load())
	}
}

// A start stuck in 'starting': if the gateway restarts during relayQueuedTask,
// OpenStore sets every 'starting' row back to 'open' so ExpireStarts / a not-started
// report can end it with exactly one notice.
func TestReopenStoreResetsStartingStarts(t *testing.T) {
	dbPath := seededStorePath(t, "restart.db")
	s, err := OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	c := &clock{t: time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)}
	s.now = c.now
	a, b := twoSessions(t, s)
	nodeA := mustNode(t, s, a)

	if err := s.trackWorkerStart("q_stuck", nodeA, b, "shop", "codex", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.markStartUnknown(nodeA, "q_stuck"); err != nil {
		t.Fatal(err)
	}

	// Claim the start so its state becomes 'starting'.
	won, err := s.claimStart(nodeA, "q_stuck")
	if err != nil || !won {
		t.Fatalf("claimStart failed: won=%v err=%v", won, err)
	}

	var state string
	s.db.QueryRow(`SELECT state FROM worker_starts WHERE ref='q_stuck'`).Scan(&state)
	if state != "starting" {
		t.Fatalf("state before reopen = %q, want starting", state)
	}

	// Gateway restarts: close and reopen store.
	s.Close()
	s2, err := OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	s2.now = c.now

	// The row must be reset to 'open'.
	s2.db.QueryRow(`SELECT state FROM worker_starts WHERE ref='q_stuck'`).Scan(&state)
	if state != "open" {
		t.Fatalf("state after reopen = %q, want open", state)
	}

	// ExpireStarts can now end it with exactly one notice.
	c.t = c.t.Add(15 * time.Minute)
	notices, err := s2.ExpireStarts(10*time.Minute, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(notices) != 1 {
		t.Fatalf("expected 1 notice, got %d", len(notices))
	}
	s2.db.QueryRow(`SELECT state FROM worker_starts WHERE ref='q_stuck'`).Scan(&state)
	if state != "not_started" {
		t.Fatalf("state after expire = %q, want not_started", state)
	}
}

// Review item 1: block the relay, send worker.not_started with the same Ref, release a failing relay: state not_started, exactly one notice.
func TestStartingStartReportNotStartedThenRelayFails(t *testing.T) {
	inTask := make(chan struct{})
	resumeTask := make(chan struct{})
	f, worker := newRelayFixture(t, func(f *startsFixture, w string) func(protocol.Header, []byte) error {
		return func(h protocol.Header, raw []byte) error {
			if h.Type == "worker.task" {
				close(inTask)
				<-resumeTask
				return errors.New("relay network failure")
			}
			return nil
		}
	})
	if err := f.s.SetNodeHello(f.nodeA, "nodeA", "v0.4.131"); err != nil {
		t.Fatal(err)
	}

	if err := f.s.trackWorkerStart("q_fail", f.nodeA, f.b, "shop", "codex", ""); err != nil {
		t.Fatal(err)
	}
	if err := f.s.startSession(f.nodeA, "q_fail", worker); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		done <- f.frame(t, f.nodeA, "worker.queued_start", f.queuedStart(worker, "q_fail"))
	}()

	// Wait until relay has started and claimed 'starting'.
	<-inTask

	// While relay is in-flight, daemon reports worker.not_started.
	if err := f.frame(t, f.nodeA, "worker.not_started", protocol.WorkerNotStarted{
		Ref:     "q_fail",
		Owner:   f.b,
		Project: "shop",
		Harness: "codex",
		Reason:  "task send timed out",
	}); err != nil {
		t.Fatalf("worker.not_started failed while starting: %v", err)
	}

	// Release the failing relay.
	close(resumeTask)
	if err := <-done; err == nil {
		t.Fatal("queued_start succeeded, want error")
	}

	// Verify state is not_started.
	var state string
	f.s.db.QueryRow(`SELECT state FROM worker_starts WHERE ref='q_fail'`).Scan(&state)
	if state != "not_started" {
		t.Errorf("state after failing relay = %q, want not_started", state)
	}

	// Verify exactly one not-started notice was emitted.
	ns := f.notices(t, f.b)
	if len(ns) != 1 {
		t.Fatalf("notices = %d, want exactly 1: %v", len(ns), ns)
	}
	if !strings.Contains(ns[0].Body, "status: not started") {
		t.Errorf("notice body: %s", ns[0].Body)
	}
}

// Review item 2: block the relay, send worker.not_started with the same Ref, release a relay that succeeds after the report: still exactly one notice in total and no started notice from the gateway side.
func TestStartingStartReportNotStartedThenRelaySucceeds(t *testing.T) {
	inTask := make(chan struct{})
	resumeTask := make(chan struct{})
	f, worker := newRelayFixture(t, func(f *startsFixture, w string) func(protocol.Header, []byte) error {
		return func(h protocol.Header, raw []byte) error {
			if h.Type == "worker.task" {
				close(inTask)
				<-resumeTask
				return nil
			}
			return nil
		}
	})
	if err := f.s.SetNodeHello(f.nodeA, "nodeA", "v0.4.131"); err != nil {
		t.Fatal(err)
	}

	if err := f.s.trackWorkerStart("q_succ", f.nodeA, f.b, "shop", "codex", ""); err != nil {
		t.Fatal(err)
	}
	if err := f.s.startSession(f.nodeA, "q_succ", worker); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		done <- f.frame(t, f.nodeA, "worker.queued_start", f.queuedStart(worker, "q_succ"))
	}()

	// Wait until relay has started and claimed 'starting'.
	<-inTask

	// While relay is in-flight, daemon reports worker.not_started.
	if err := f.frame(t, f.nodeA, "worker.not_started", protocol.WorkerNotStarted{
		Ref:     "q_succ",
		Owner:   f.b,
		Project: "shop",
		Harness: "codex",
		Reason:  "task send timed out",
	}); err != nil {
		t.Fatalf("worker.not_started failed while starting: %v", err)
	}

	// Release the succeeding relay.
	close(resumeTask)
	if err := <-done; err != nil {
		t.Fatalf("queued_start failed: %v", err)
	}

	// Verify state is not_started (closeStart did not overwrite it to closed).
	var state string
	f.s.db.QueryRow(`SELECT state FROM worker_starts WHERE ref='q_succ'`).Scan(&state)
	if state != "not_started" {
		t.Errorf("state after succeeding relay = %q, want not_started", state)
	}

	// Verify exactly one notice in total (the not-started notice) and no started notice from the gateway side.
	ns := f.notices(t, f.b)
	if len(ns) != 1 {
		t.Fatalf("notices = %d, want exactly 1: %v", len(ns), ns)
	}
	if !strings.Contains(ns[0].Body, "status: not started") {
		t.Errorf("notice body: %s", ns[0].Body)
	}
}

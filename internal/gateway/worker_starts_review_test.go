package gateway

import (
	"errors"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Finding 1: Unbound queued start.
// verifyStart with an unbound tracked Ref must not accept an arbitrary session.
func TestUnboundQueuedStartRefused(t *testing.T) {
	f, worker := newRelayFixture(t, func(f *startsFixture, w string) func(protocol.Header, []byte) error {
		return func(h protocol.Header, raw []byte) error { return nil }
	})
	if err := f.s.SetNodeHello(f.nodeA, "nodeA", "v0.4.127"); err != nil {
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

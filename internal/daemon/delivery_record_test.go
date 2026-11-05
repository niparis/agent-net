package daemon

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// deliveryRig is a gateway, a sender node and a receiving node whose fake
// harness is fb. The message is stored at the gateway, and the test hands it
// to the receiving daemon itself, so it decides when a delivery happens.
type deliveryRig struct {
	g      *testGateway
	recv   *node
	fb     *fakeAdapter
	sender string
	to     string
}

func newDeliveryRig(t *testing.T, fb *fakeAdapter) *deliveryRig {
	t.Helper()
	g := startGateway(t, t.TempDir(), "")
	fb.harness = "fake"
	recv := startNode(t, g, nodeOpts{adapter: fb})
	fb.setSessions(fakeSession("nb", "bob"))
	fa := &fakeAdapter{harness: "fake"}
	snd := startNode(t, g, nodeOpts{adapter: fa})
	fa.setSessions(fakeSession("na", "alice"))
	r := &deliveryRig{g: g, recv: recv, fb: fb}
	eventually(t, "both sessions registered", func() bool {
		r.sender, r.to = snd.d.SessionID("fake", "na"), recv.d.SessionID("fake", "nb")
		return r.sender != "" && r.to != ""
	})
	// onDeliver reports session activity, whose upsert makes the gateway
	// dispatch pending messages. Keep those pushes behind this barrier so
	// only the test's direct calls can deliver. Stop cancels the context and
	// drains the lane; queued pushes then have canceled delivery contexts.
	ctx := recv.d.workerStreamCtx
	ready := make(chan struct{})
	recv.d.enqueue(r.to, func() {
		close(ready)
		<-ctx.Done()
	})
	<-ready
	return r
}

func (r *deliveryRig) store(t *testing.T, key string) protocol.Message {
	t.Helper()
	m, _, err := r.g.gw.Store().StoreMessage(protocol.MsgSend{Sender: r.sender, Recipient: r.to, Body: "hello " + key, IdempotencyKey: key})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// gatewayState is what the sender sees: the state and the detail.
func (r *deliveryRig) gatewayState(id string) (string, string) {
	m, _, err := r.g.gw.Store().Message(id)
	if err != nil {
		return "error: " + err.Error(), ""
	}
	return m.State, m.DeliveryDetail
}

func recordState(d *Daemon, id string) string {
	s, ok := d.st.deliveryState(id)
	if !ok {
		return "(none)"
	}
	return s
}

// A session activity update must not let the gateway take delivery timing
// away from tests that call onDeliver themselves.
func TestDeliveryRigKeepsGatewayPushesPending(t *testing.T) {
	t.Parallel()
	r := newDeliveryRig(t, &fakeAdapter{})
	m := r.store(t, "manual-delivery")
	r.recv.d.pushSession(r.recv.d.sessionByID(r.to))
	eventually(t, "gateway push received or delivered", func() bool {
		if recordState(r.recv.d, m.ID) != "(none)" {
			return true
		}
		d := r.recv.d
		d.lanesMu.Lock()
		l := d.lanes[r.to]
		d.lanesMu.Unlock()
		if l == nil {
			return false
		}
		l.mu.Lock()
		defer l.mu.Unlock()
		return len(l.q) > 0
	})
	if got := recordState(r.recv.d, m.ID); got != "(none)" {
		t.Fatalf("gateway push wrote delivery record %q before manual delivery", got)
	}
	if n := len(r.fb.delivered()); n != 0 {
		t.Fatalf("gateway push reached harness %d times before manual delivery", n)
	}
}

// (a) The daemon dies after it wrote "injecting" and while it wrote to the
// harness. After a restart, the same message is not written again, and the
// sender learns that it may have arrived.
func TestDeliveryCrashWhileInjectingIsNotReinjected(t *testing.T) {
	t.Parallel()
	fb := &fakeAdapter{crash: true}
	r := newDeliveryRig(t, fb)
	m := r.store(t, "crash")

	done := make(chan struct{})
	go func() {
		defer close(done)
		r.recv.d.onDeliver(context.Background(), m)
	}()
	<-done
	if n := len(fb.delivered()); n != 1 {
		t.Fatalf("harness saw %d writes before the crash, want 1", n)
	}
	if s := recordState(r.recv.d, m.ID); s != deliveryInjecting {
		t.Fatalf("record after the crash = %q, want %q", s, deliveryInjecting)
	}
	if s, _ := r.gatewayState(m.ID); s == protocol.StateDelivered || s == protocol.StateFailed {
		t.Fatalf("the crashed daemon reported %q", s)
	}

	fb.mu.Lock()
	fb.crash = false
	fb.mu.Unlock()
	r.recv.restart()
	fb.setSessions(fakeSession("nb", "bob"))
	// The gateway pushes the message again on reconnect; push it once more
	// by hand so the test does not depend on that timing.
	r.recv.d.onDeliver(context.Background(), m)

	eventually(t, "sender sees may-have-arrived", func() bool {
		s, detail := r.gatewayState(m.ID)
		return s == protocol.StateFailed && detail == detailStoppedWhileDelivering
	})
	if n := len(fb.delivered()); n != 1 {
		t.Fatalf("message written %d times, want 1 (no re-injection after restart)", n)
	}
	if s := recordState(r.recv.d, m.ID); s != deliveryInjecting {
		t.Fatalf("record after the restart = %q, want it kept as %q", s, deliveryInjecting)
	}
	if !strings.Contains(detailStoppedWhileDelivering, "may have arrived") {
		t.Fatalf("detail %q does not say the message may have arrived", detailStoppedWhileDelivering)
	}
}

// (b) A write that fails after connecting is failed, not delivered, and the
// message is not written again on a second push.
func TestDeliveryWriteErrorAfterConnectingIsFailed(t *testing.T) {
	t.Parallel()
	fb := &fakeAdapter{uncertain: "write failed after connecting; the message may have arrived"}
	r := newDeliveryRig(t, fb)
	m := r.store(t, "write-error")

	r.recv.d.onDeliver(context.Background(), m)
	s, detail := r.gatewayState(m.ID)
	if s != protocol.StateFailed || detail != fb.uncertain {
		t.Fatalf("sender sees %q %q, want failed with the adapter's detail", s, detail)
	}
	if got := recordState(r.recv.d, m.ID); got != deliveryUnconfirmed {
		t.Fatalf("record = %q, want %q", got, deliveryUnconfirmed)
	}
	if r.recv.d.st.isDelivered(m.ID) {
		t.Fatal("a failed write counts as delivered")
	}
	r.recv.d.onDeliver(context.Background(), m)
	if n := len(fb.delivered()); n != 1 {
		t.Fatalf("message written %d times, want 1", n)
	}
}

// A known failure (nothing reached the harness) is recorded as failed, and a
// later push may try again.
func TestDeliveryKnownFailureIsRecordedAndMayBeTriedAgain(t *testing.T) {
	t.Parallel()
	fb := &fakeAdapter{fail: "socket missing"}
	r := newDeliveryRig(t, fb)
	m := r.store(t, "known-failure")

	r.recv.d.onDeliver(context.Background(), m)
	if s, detail := r.gatewayState(m.ID); s != protocol.StateFailed || detail != "socket missing" {
		t.Fatalf("sender sees %q %q", s, detail)
	}
	if got := recordState(r.recv.d, m.ID); got != deliveryFailed {
		t.Fatalf("record = %q, want %q", got, deliveryFailed)
	}
	fb.mu.Lock()
	fb.fail = ""
	fb.mu.Unlock()
	r.recv.d.onDeliver(context.Background(), m)
	if n := len(fb.delivered()); n != 2 {
		t.Fatalf("message written %d times, want 2 (a known failure may be tried again)", n)
	}
	if got := recordState(r.recv.d, m.ID); got != deliveryDelivered {
		t.Fatalf("record after the second try = %q, want %q", got, deliveryDelivered)
	}
}

func TestDeliveryConcurrentPushDoesNotRetryInFlight(t *testing.T) {
	t.Parallel()
	fb := &fakeAdapter{fail: "socket missing"}
	r := newDeliveryRig(t, fb)
	m := r.store(t, "concurrent-failure")

	// Hold the first attempt at the harness until the duplicate has returned.
	fb.mu.Lock()
	locked := true
	defer func() {
		if locked {
			fb.mu.Unlock()
		}
	}()
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.recv.d.onDeliver(context.Background(), m)
	}()
	eventually(t, "first attempt injecting", func() bool {
		return recordState(r.recv.d, m.ID) == deliveryInjecting
	})
	r.recv.d.onDeliver(context.Background(), m)
	if got := recordState(r.recv.d, m.ID); got != deliveryInjecting {
		t.Fatalf("in-flight record = %q, want injecting", got)
	}
	fb.mu.Unlock()
	locked = false
	select {
	case <-done:
	case <-time.After(scaled(20 * time.Second)):
		t.Fatal("first attempt did not finish")
	}
	if got := recordState(r.recv.d, m.ID); got != deliveryFailed {
		t.Fatalf("record after concurrent failure = %q, want failed", got)
	}
	if n := len(fb.delivered()); n != 1 {
		t.Fatalf("concurrent pushes wrote %d times, want 1", n)
	}
	if s, detail := r.gatewayState(m.ID); s != protocol.StateFailed || detail != "socket missing" {
		t.Fatalf("sender sees %q %q", s, detail)
	}
}

// (c) A normal delivery ends delivered, and a second push of the same message
// is answered "duplicate" without a second write.
func TestDeliveryNormalEndsDeliveredAndDuplicateStaysDuplicate(t *testing.T) {
	t.Parallel()
	fb := &fakeAdapter{}
	r := newDeliveryRig(t, fb)
	m := r.store(t, "normal")

	r.recv.d.onDeliver(context.Background(), m)
	if s, _ := r.gatewayState(m.ID); s != protocol.StateDelivered {
		t.Fatalf("sender sees %q, want delivered", s)
	}
	if got := recordState(r.recv.d, m.ID); got != deliveryDelivered {
		t.Fatalf("record = %q, want %q", got, deliveryDelivered)
	}
	r.recv.d.onDeliver(context.Background(), m)
	if n := len(fb.delivered()); n != 1 {
		t.Fatalf("message written %d times, want 1", n)
	}
	eventually(t, "duplicate reported", func() bool {
		_, detail := r.gatewayState(m.ID)
		return detail == "duplicate"
	})
	if s, _ := r.gatewayState(m.ID); s != protocol.StateDelivered {
		t.Fatalf("sender sees %q after the duplicate, want delivered", s)
	}
}

// (d) Rows written before the state column existed still count as delivered.
func TestDeliveredRowsFromBeforeTheMigrationStayDelivered(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.db")
	old, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`CREATE TABLE delivered (message_id TEXT PRIMARY KEY, at INTEGER NOT NULL);
INSERT INTO delivered (message_id, at) VALUES ('m_old', ?)`, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	old.Close()

	fa := &fakeAdapter{harness: "fake"}
	d, err := New(Config{StateDir: dir, SocketPath: filepath.Join(shortDir(t), "d.sock"), Adapters: []Adapter{fa}})
	if err != nil {
		t.Fatal(err)
	}
	stopped := false
	defer func() {
		if !stopped {
			d.Stop()
		}
	}()
	fa.host = d
	if !d.st.isDelivered("m_old") {
		t.Fatal("an old row no longer counts as delivered")
	}
	if got := recordState(d, "m_old"); got != deliveryDelivered {
		t.Fatalf("old row state = %q, want %q", got, deliveryDelivered)
	}
	d.sessions[key("fake", "na")] = &localSession{info: fakeSession("na", "alice"), sessionID: "s_a"}
	d.onDeliver(context.Background(), protocol.Message{ID: "m_old", To: "s_a", From: "s_b", Body: "hi"})
	if n := len(fa.delivered()); n != 0 {
		t.Fatalf("an old delivered message was written again (%d)", n)
	}
	// Opening the database again does not run the migration twice.
	stopped = true
	d.Stop()
	st, err := openState(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	if !st.isDelivered("m_old") {
		t.Fatal("old row lost on the second open")
	}
}

// (e) The hourly prune removes old records in every state and keeps recent
// ones, so an injecting record of a live message keeps blocking a re-write.
func TestPruneDeliveredRemovesOldRecordsInEveryState(t *testing.T) {
	t.Parallel()
	st, err := openState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	now := time.Now()
	cutoff := now.Add(-(protocol.DefaultTTL + deliveredMargin))
	for _, row := range []struct {
		id, state string
		at        time.Time
	}{
		{"old-injecting", deliveryInjecting, cutoff.Add(-time.Hour)},
		{"old-failed", deliveryFailed, cutoff.Add(-time.Hour)},
		{"old-unconfirmed", deliveryUnconfirmed, cutoff.Add(-time.Hour)},
		{"new-injecting", deliveryInjecting, now.Add(-time.Hour)},
		{"new-delivered", deliveryDelivered, now.Add(-time.Hour)},
	} {
		if _, err := st.db.Exec(`INSERT INTO delivered (message_id, at, state) VALUES (?,?,?)`, row.id, row.at.Unix(), row.state); err != nil {
			t.Fatal(err)
		}
	}
	n, err := st.pruneDelivered(now)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("pruned %d rows, want 3", n)
	}
	if s, ok := st.deliveryState("new-injecting"); !ok || s != deliveryInjecting {
		t.Fatalf("recent injecting record = %q %v", s, ok)
	}
	if !st.isDelivered("new-delivered") {
		t.Fatal("recent delivered record pruned")
	}
}

// The record goes from injecting to delivered, never back: a late failure
// report cannot undo a delivered record.
func TestDeliveryRecordNeverLeavesDelivered(t *testing.T) {
	t.Parallel()
	st, err := openState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	if err := st.beginDelivery("m_1"); err != nil {
		t.Fatal(err)
	}
	if s, _ := st.deliveryState("m_1"); s != deliveryInjecting {
		t.Fatalf("state %q", s)
	}
	if err := st.markDelivered("m_1"); err != nil {
		t.Fatal(err)
	}
	for _, f := range []func(string) error{st.beginDelivery, st.markDeliveryFailed, st.markDeliveryUnconfirmed, st.clearDelivery} {
		if err := f("m_1"); err != nil {
			t.Fatal(err)
		}
	}
	if !st.isDelivered("m_1") {
		t.Fatal("a delivered record was changed")
	}
}

func TestDeliveryCannotWriteWithoutDurableIntent(t *testing.T) {
	t.Parallel()
	r := newDeliveryRig(t, &fakeAdapter{})
	m := r.store(t, "storage-failure")
	if _, err := r.recv.d.st.db.Exec(`CREATE TRIGGER reject_delivery BEFORE INSERT ON delivered BEGIN SELECT RAISE(FAIL, 'disk failure'); END`); err != nil {
		t.Fatal(err)
	}
	r.recv.d.onDeliver(context.Background(), m)
	if n := len(r.fb.delivered()); n != 0 {
		t.Fatalf("harness saw %d writes without a durable record", n)
	}
	if s, detail := r.gatewayState(m.ID); s != protocol.StateFailed || !strings.Contains(detail, "cannot record delivery intent") {
		t.Fatalf("sender sees %q %q", s, detail)
	}
}

func TestDeliveryQueuedWorkerWaitsForHarnessConfirmation(t *testing.T) {
	t.Parallel()
	r := newDeliveryRig(t, &fakeAdapter{})
	m := r.store(t, "queued-worker")
	w := &worker{id: "w_test", harness: "opencode", sessionID: r.to, owner: r.sender, turnActive: true}
	r.recv.d.wmu.Lock()
	r.recv.d.workers[w.id] = w
	r.recv.d.wmu.Unlock()
	defer func() {
		r.recv.d.wmu.Lock()
		delete(r.recv.d.workers, w.id)
		r.recv.d.wmu.Unlock()
	}()
	for range 2 {
		r.recv.d.onDeliver(context.Background(), m)
	}
	if s, _ := r.gatewayState(m.ID); s != protocol.StateDispatched {
		t.Fatalf("queued message state = %q, want dispatched", s)
	}
	if r.recv.d.st.isDelivered(m.ID) || len(r.fb.delivered()) != 0 {
		t.Fatal("in-memory queue counted as harness delivery")
	}
	r.recv.d.wmu.Lock()
	n := len(w.turnQueue)
	w.turnQueue = nil
	r.recv.d.wmu.Unlock()
	if n != 1 {
		t.Fatalf("duplicate push queued %d copies, want 1", n)
	}
	r.recv.d.deliverQueuedWorkerTurn(m)
	if s, _ := r.gatewayState(m.ID); s != protocol.StateDelivered || !r.recv.d.st.isDelivered(m.ID) {
		t.Fatalf("confirmed queued message state = %q", s)
	}
	if n := len(r.fb.delivered()); n != 1 {
		t.Fatalf("harness saw %d writes, want 1", n)
	}
}

func TestDeliveryConfirmationStorageFailureKeepsIntent(t *testing.T) {
	t.Parallel()
	r := newDeliveryRig(t, &fakeAdapter{})
	m := r.store(t, "confirmation-storage-failure")
	if _, err := r.recv.d.st.db.Exec(`CREATE TRIGGER reject_confirmation BEFORE UPDATE ON delivered WHEN NEW.state = 'delivered' BEGIN SELECT RAISE(FAIL, 'disk failure'); END`); err != nil {
		t.Fatal(err)
	}
	r.recv.d.onDeliver(context.Background(), m)
	if s, detail := r.gatewayState(m.ID); s != protocol.StateFailed || !strings.Contains(detail, "may have arrived") {
		t.Fatalf("sender sees %q %q", s, detail)
	}
	if s := recordState(r.recv.d, m.ID); s != deliveryInjecting {
		t.Fatalf("record = %q, want injecting", s)
	}
	r.recv.d.onDeliver(context.Background(), m)
	if n := len(r.fb.delivered()); n != 1 {
		t.Fatalf("harness saw %d writes, want 1", n)
	}
}

func TestDeliveryDuplicateWhileQueuedTurnIsStarting(t *testing.T) {
	t.Parallel()
	r := newDeliveryRig(t, &fakeAdapter{})
	m := r.store(t, "queued-start-duplicate")
	d := r.recv.d
	w := &worker{id: "w_test", harness: "opencode", sessionID: r.to, owner: r.sender, turnActive: true}
	d.wmu.Lock()
	d.workers[w.id] = w
	d.wmu.Unlock()
	defer func() {
		d.wmu.Lock()
		delete(d.workers, w.id)
		d.wmu.Unlock()
	}()
	d.onDeliver(context.Background(), m)

	// Pause the background delivery after the idle handler dequeues the
	// message, but before it can claim the durable harness write.
	d.bgMu.Lock()
	blocked := true
	defer func() {
		if blocked {
			d.bgMu.Unlock()
		}
	}()
	idleDone := make(chan bool, 1)
	go func() { idleDone <- d.openCodeTurnIdle(r.to) }()
	eventually(t, "queued turn dequeued", func() bool {
		d.wmu.Lock()
		defer d.wmu.Unlock()
		return w.turnAwaitingStart && len(w.turnQueue) == 0
	})
	d.onDeliver(context.Background(), m)
	d.wmu.Lock()
	queued := len(w.turnQueue)
	d.wmu.Unlock()
	d.bgMu.Unlock()
	blocked = false
	<-idleDone
	if queued != 0 {
		t.Fatalf("duplicate re-entered the queue while its original was starting: %d queued", queued)
	}
	eventually(t, "queued turn confirmed", func() bool { return d.st.isDelivered(m.ID) })
	d.wmu.Lock()
	w.turnAwaitingStart = false // the harness reports that the turn started
	d.wmu.Unlock()
	if d.openCodeTurnIdle(r.to) {
		t.Fatal("completed turn still has pending work")
	}
	if n := len(r.fb.delivered()); n != 1 {
		t.Fatalf("harness writes = %d, want 1", n)
	}
}

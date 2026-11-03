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

// (a) The daemon dies after it wrote "injecting" and while it wrote to the
// harness. After a restart, the same message is not written again, and the
// sender learns that it may have arrived.
func TestDeliveryCrashWhileInjectingIsNotReinjected(t *testing.T) {
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

// (c) A normal delivery ends delivered, and a second push of the same message
// is answered "duplicate" without a second write.
func TestDeliveryNormalEndsDeliveredAndDuplicateStaysDuplicate(t *testing.T) {
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
		return len(r.g.logs.lines("msg.delivered", m.ID, "duplicate")) == 1
	})
	if s, _ := r.gatewayState(m.ID); s != protocol.StateDelivered {
		t.Fatalf("sender sees %q after the duplicate, want delivered", s)
	}
}

// (d) Rows written before the state column existed still count as delivered.
func TestDeliveredRowsFromBeforeTheMigrationStayDelivered(t *testing.T) {
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

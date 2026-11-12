package gateway

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// busyStatus reports a session's status the way a daemon does, keeping its
// harness and native ID so the upsert finds the same row.
func busyStatus(t *testing.T, s *Store, id, status string) {
	t.Helper()
	node := mustNode(t, s, id)
	var harness, native string
	if err := s.db.QueryRow(`SELECT harness, native_id FROM sessions WHERE id=?`, id).Scan(&harness, &native); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertSession(node, protocol.SessionInfo{Harness: harness, NativeID: native, Status: status}); err != nil {
		t.Fatal(err)
	}
}

// Part 2: a busy session holds its messages at the gateway, and the first
// ready report delivers them as one newest-first batch.
func TestBusySessionHoldsThenDeliversOneBatch(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	busyStatus(t, s, b, "busy")
	var ids []string
	for i := 0; i < 3; i++ {
		c.t = c.t.Add(time.Minute)
		m, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Type: protocol.TypeRequest,
			Body: "body-" + strconv.Itoa(i), IdempotencyKey: "k-" + strconv.Itoa(i)})
		if err != nil {
			t.Fatal(err)
		}
		if !s.HoldForBusy(m) {
			t.Fatalf("message %d was not held for a busy session", i)
		}
		ids = append(ids, m.ID)
	}
	if _, n, err := releaseOne(t, s, b, false); err != nil || n != 0 {
		t.Fatalf("released while still busy: n=%d err=%v", n, err)
	}
	busyStatus(t, s, b, "idle")
	carrier, n, err := releaseOne(t, s, b, false)
	if err != nil || n != 3 {
		t.Fatalf("release: n=%d err=%v", n, err)
	}
	if len(carrier.Batch) != 3 {
		t.Fatalf("batch carries %d original messages, want 3", len(carrier.Batch))
	}
	for i, want := range []string{ids[2], ids[1], ids[0]} {
		if carrier.Batch[i].ID != want {
			t.Fatalf("batch[%d] = %s, want newest first %s", i, carrier.Batch[i].ID, want)
		}
	}
	if !strings.Contains(carrier.Body, "3 messages arrived while you were busy. Act on the newest; read the older ones as context.") {
		t.Fatalf("batch header missing:\n%s", carrier.Body)
	}
	if strings.Index(carrier.Body, ids[2]) > strings.Index(carrier.Body, ids[0]) {
		t.Fatalf("the newest message is not first:\n%s", carrier.Body)
	}
	pending, err := s.Pending(mustNode(t, s, b), false)
	if err != nil || len(pending) != 1 || pending[0].ID != carrier.ID {
		t.Fatalf("pending = %+v, %v; want only the batch %s", pending, err, carrier.ID)
	}
	if err := s.ApplyDelivery(carrier.ID, protocol.StateDelivered, ""); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		m, ok, _ := s.Message(id)
		if !ok || m.State != protocol.StateDelivered {
			t.Fatalf("original %s is %q, want delivered", id, m.State)
		}
	}
	// A reply still names the original message it answers.
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, ParentID: ids[1],
		Type: protocol.TypeReply, Body: "answer", IdempotencyKey: "reply"}); err != nil {
		t.Fatal(err)
	}
	if m, _, _ := s.Message(ids[1]); m.State != protocol.StateReplied {
		t.Fatalf("the answered original is %q, want replied", m.State)
	}
}

// The batch carrier carries the newest sender's display name and harness, so
// an adapter's provenance line says who wrote it instead of falling back to
// the raw session ID with harness "unknown".
func TestBusyBatchCarrierKeepsSenderNameAndHarness(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	busyStatus(t, s, b, "busy")
	for i := 0; i < 3; i++ {
		c.t = c.t.Add(time.Minute)
		m, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Type: protocol.TypeRequest,
			Body: "body-" + strconv.Itoa(i), IdempotencyKey: "name-" + strconv.Itoa(i)})
		if err != nil {
			t.Fatal(err)
		}
		if !s.HoldForBusy(m) {
			t.Fatalf("message %d was not held", i)
		}
	}
	busyStatus(t, s, b, "idle")
	carrier, n, err := releaseOne(t, s, b, false)
	if err != nil || n != 3 {
		t.Fatalf("release: n=%d err=%v", n, err)
	}
	if carrier.FromName != "alice" || carrier.FromHarness != "claude" {
		t.Fatalf("carrier sender = %q (%q), want alice (claude)", carrier.FromName, carrier.FromHarness)
	}
	got, ok, err := s.Message(carrier.ID)
	if err != nil || !ok || got.FromName != "alice" || got.FromHarness != "claude" {
		t.Fatalf("stored carrier sender = %q (%q), %v", got.FromName, got.FromHarness, err)
	}
}

// A single held message goes as it is, with its own ID.
func TestBusySessionSingleMessageKeepsItsID(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	busyStatus(t, s, b, "busy")
	m, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Type: protocol.TypeNotification,
		Body: "one", IdempotencyKey: "one"})
	if err != nil {
		t.Fatal(err)
	}
	if !s.HoldForBusy(m) {
		t.Fatal("not held")
	}
	busyStatus(t, s, b, "idle")
	carrier, n, err := releaseOne(t, s, b, false)
	if err != nil || n != 1 || carrier.ID != m.ID || len(carrier.Batch) != 0 {
		t.Fatalf("single release = %+v n=%d err=%v, want the original", carrier, n, err)
	}
}

// Part 2 and part c: a session that stays busy gets its held messages after
// BusyHoldLimit, and the release is logged.
func TestBusyHoldFallbackAfterLimit(t *testing.T) {
	s, c := newTestStore(t)
	var logs []string
	s.logf = func(kind string, kv ...any) { logs = append(logs, kind) }
	a, b := twoSessions(t, s)
	busyStatus(t, s, b, "busy")
	m, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Type: protocol.TypeNotification,
		Body: "late", IdempotencyKey: "late"})
	if err != nil {
		t.Fatal(err)
	}
	if !s.HoldForBusy(m) {
		t.Fatal("not held")
	}
	c.t = c.t.Add(BusyHoldLimit - time.Minute)
	if stuck, err := s.StuckBusySessions(); err != nil || len(stuck) != 0 {
		t.Fatalf("stuck before the limit = %v, %v", stuck, err)
	}
	c.t = c.t.Add(2 * time.Minute)
	stuck, err := s.StuckBusySessions()
	if err != nil || len(stuck) != 1 || stuck[0] != b {
		t.Fatalf("stuck after the limit = %v, %v", stuck, err)
	}
	carrier, n, err := releaseOne(t, s, b, true)
	if err != nil || n != 1 || carrier.ID != m.ID {
		t.Fatalf("fallback release = %+v n=%d err=%v", carrier, n, err)
	}
	if !containsLine(logs, "msg.held_busy") || !containsLine(logs, "msg.released_busy") {
		t.Fatalf("stage log lines = %v, want held and released", logs)
	}
}

func containsLine(lines []string, want string) bool {
	for _, line := range lines {
		if line == want {
			return true
		}
	}
	return false
}

// Part 3: the inbox limit refuses at five by default.
func TestInboxLimitRefusesAtFive(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	for i := 0; i < DefaultInboxLimit; i++ {
		if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Type: protocol.TypeNotification,
			Body: "w", IdempotencyKey: "w-" + strconv.Itoa(i)}); err != nil {
			t.Fatalf("message %d: %v", i, err)
		}
	}
	_, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Type: protocol.TypeNotification,
		Body: "one too many", IdempotencyKey: "over"})
	if err == nil {
		t.Fatal("the sixth message was accepted")
	}
	for _, want := range []string{"recipient has 5 waiting", "oldest from you at", "withdraw one or wait"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal %q does not say %q", err.Error(), want)
		}
	}
}

// Part 3: the owner and the CTO with the owner's voice skip the limit; the
// CTO writing normally does not.
func TestInboxLimitExemptsOwnerAndCTOOwnerVoice(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	for i := 0; i < DefaultInboxLimit; i++ {
		if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Type: protocol.TypeNotification,
			Body: "w", IdempotencyKey: "w-" + strconv.Itoa(i)}); err != nil {
			t.Fatal(err)
		}
	}
	owner, err := s.UpsertSession(mustNode(t, s, a), protocol.SessionInfo{
		Harness: protocol.OwnerHarness, NativeID: protocol.OwnerNativeID, Name: "you"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: owner, Recipient: b, Type: protocol.TypeNotification,
		Body: "owner", IdempotencyKey: "owner"}); err != nil {
		t.Fatalf("the owner was refused: %v", err)
	}
	node := mustNode(t, s, a)
	cto, _, err := s.upsertSession(s.db, node, protocol.SessionInfo{Harness: "cto", NativeID: "cto", Name: "CTO"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO owner_settings(key,value,updated_at) VALUES('cto_session',?,0)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, cto); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: cto, Recipient: b, FromCTO: true, OwnerApproved: true,
		Type: protocol.TypeNotification, Body: "as owner", IdempotencyKey: "cto-owner"}); err != nil {
		t.Fatalf("the CTO in the owner's voice was refused: %v", err)
	}
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: cto, Recipient: b, FromCTO: true,
		Type: protocol.TypeNotification, Body: "as itself", IdempotencyKey: "cto"}); err == nil {
		t.Fatal("the CTO writing normally skipped the limit")
	}
}

// Part 3: messages.inbox_limit resolves at repo and agent level.
func TestInboxLimitResolvesAtRepoAndAgentLevel(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	nodeB := mustNode(t, s, b)
	var harness, native string
	if err := s.db.QueryRow(`SELECT harness,native_id FROM sessions WHERE id=?`, b).Scan(&harness, &native); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertSession(nodeB, protocol.SessionInfo{Harness: harness, NativeID: native,
		Repo: protocol.RepoInfo{Project: "demo"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO setting_values(key,level,scope,item,value,revision)
		VALUES('messages.inbox_limit','repo','demo','','2',1)`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Type: protocol.TypeNotification,
			Body: "w", IdempotencyKey: "repo-" + strconv.Itoa(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Type: protocol.TypeNotification,
		Body: "over", IdempotencyKey: "repo-over"}); err == nil || !strings.Contains(err.Error(), "has 2 waiting") {
		t.Fatalf("repo limit: %v", err)
	}
	if _, err := s.db.Exec(`UPDATE sessions SET agent_id='helper' WHERE id=?`, b); err != nil {
		t.Fatal(err)
	}
	// A wider agent value overrides the repo value, and a narrower one
	// overrides it the other way.
	if _, err := s.db.Exec(`INSERT INTO setting_values(key,level,scope,item,value,revision)
		VALUES('messages.inbox_limit','agent','helper','','5',2)`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Type: protocol.TypeNotification,
		Body: "allowed", IdempotencyKey: "agent-allowed"}); err != nil {
		t.Fatalf("agent limit 5 did not override repo 2: %v", err)
	}
	if _, err := s.db.Exec(`UPDATE setting_values SET value='1',revision=3
		WHERE key='messages.inbox_limit' AND level='agent' AND scope='helper'`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Type: protocol.TypeNotification,
		Body: "over", IdempotencyKey: "agent-over"}); err == nil || !strings.Contains(err.Error(), "has 3 waiting") {
		t.Fatalf("agent limit: %v", err)
	}
}

// Part 3: urgent messages jump the batch order but still count toward the
// limit.
func TestUrgentJumpsTheBatchButCounts(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	busyStatus(t, s, b, "busy")
	var ids []string
	for i := 0; i < 3; i++ {
		c.t = c.t.Add(time.Minute)
		m, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Type: protocol.TypeRequest,
			Body: "m", IdempotencyKey: "u-" + strconv.Itoa(i), Urgent: i == 1})
		if err != nil {
			t.Fatal(err)
		}
		if !s.HoldForBusy(m) {
			t.Fatal("not held")
		}
		ids = append(ids, m.ID)
	}
	busyStatus(t, s, b, "idle")
	carrier, n, err := releaseOne(t, s, b, false)
	if err != nil || n != 3 {
		t.Fatalf("release: %d %v", n, err)
	}
	if carrier.Batch[0].ID != ids[1] {
		t.Fatalf("batch starts with %s, want the urgent %s", carrier.Batch[0].ID, ids[1])
	}
	// An urgent message is not exempt from the limit.
	d, err := s.UpsertSession(mustNode(t, s, b), protocol.SessionInfo{Harness: "pi", NativeID: "nd", Name: "d"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < DefaultInboxLimit; i++ {
		if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: d, Type: protocol.TypeNotification,
			Body: "w", IdempotencyKey: "d-" + strconv.Itoa(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: d, Type: protocol.TypeNotification,
		Body: "urgent over", IdempotencyKey: "urgent-over", Urgent: true}); err == nil {
		t.Fatal("an urgent message skipped the full inbox")
	}
}

// Part 2 and part 5: msg.send holds for a busy session and reports the
// position, the wait and the busy time; the ready upsert releases the batch.
func TestSendToBusySessionIsHeldAndReportsQueue(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	busyStatus(t, s, b, "busy")
	busySince := c.t
	c.t = c.t.Add(5 * time.Minute)
	g := &Gateway{store: s, conns: map[string]*nodeConn{}}
	nodeA := mustNode(t, s, a)
	sendOne := func(key string) map[string]any {
		t.Helper()
		raw, err := json.Marshal(protocol.MsgSend{Sender: a, Recipient: b, Type: protocol.TypeRequest,
			Body: key, IdempotencyKey: key})
		if err != nil {
			t.Fatal(err)
		}
		data, err := g.handle(context.Background(), &nodeConn{id: nodeA}, "msg.send", raw)
		if err != nil {
			t.Fatal(err)
		}
		return data.(map[string]any)
	}
	first := sendOne("first")["message"].(protocol.Message)
	if !first.HeldForBusy || first.State != protocol.StateStored {
		t.Fatalf("first send = %+v, want held at the gateway", first)
	}
	if first.QueueWaiting != 1 || first.QueuePosition != 1 {
		t.Fatalf("queue = %d of %d, want 1 of 1", first.QueuePosition, first.QueueWaiting)
	}
	if first.BusyForSeconds != int64((c.t.Sub(busySince))/time.Second) {
		t.Fatalf("busy for %ds, want %ds", first.BusyForSeconds, int64(c.t.Sub(busySince)/time.Second))
	}
	second := sendOne("second")
	m2 := second["message"].(protocol.Message)
	// The newest message is first in the batch, so it is at position 1.
	if m2.QueuePosition != 1 || m2.QueueWaiting != 2 {
		t.Fatalf("second queue = %d of %d", m2.QueuePosition, m2.QueueWaiting)
	}
	if q, _ := second["queue"].(string); !strings.Contains(q, "queued behind 0, session busy for 5 min") {
		t.Fatalf("queue line = %q", q)
	}
	if a, _, _ := s.Agent(b); a.GatewayHeld != 2 || a.InSession != 0 {
		t.Fatalf("session counts = gw %d, in %d; want gw 2, in 0", a.GatewayHeld, a.InSession)
	}
	// The ready report sends the batch, and the originals stop waiting.
	nodeB := mustNode(t, s, b)
	upsert, _ := json.Marshal(protocol.SessionUpsert{Session: protocol.SessionInfo{Harness: "pi", NativeID: "nb", Status: "idle"}})
	if _, err := g.handle(context.Background(), &nodeConn{id: nodeB}, "session.upsert", upsert); err != nil {
		t.Fatal(err)
	}
	pending, err := s.Pending(nodeB, true)
	if err != nil || len(pending) != 1 || len(pending[0].Batch) != 2 {
		t.Fatalf("pending after ready = %+v, %v; want one batch of 2", pending, err)
	}
	if m, _, _ := s.Message(first.ID); m.HeldForBusy {
		t.Fatalf("the original %s is still marked held", first.ID)
	}
	// There is no node connection in this test, so the batch waits one more
	// step at the gateway; either way the originals no longer wait.
	if a, _, _ := s.Agent(b); a.GatewayHeld != 1 || a.InSession != 0 {
		t.Fatalf("session counts after ready = gw %d, in %d; want gw 1, in 0", a.GatewayHeld, a.InSession)
	}
}

// A file message that waits for a busy session is released on its own. The
// daemon collects files only from a message it is handed with its manifest,
// so a batch of messages with files would leave the files on the machine.
func TestBusyReleaseKeepsFilesOutOfTheBatch(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	busyStatus(t, s, b, "busy")
	files := []protocol.FileEntry{{ID: "f1", Name: "verify-attach.txt", Size: 90, SHA256: filesTestSum}}
	var fileID string
	var plainIDs []string
	for i := 0; i < 3; i++ {
		c.t = c.t.Add(time.Minute)
		send := protocol.MsgSend{Sender: a, Recipient: b, Type: protocol.TypeRequest,
			Body: "body-" + strconv.Itoa(i), IdempotencyKey: "k-" + strconv.Itoa(i)}
		if i == 1 {
			send.Files = files
		}
		m, _, err := s.StoreMessage(send)
		if err != nil {
			t.Fatal(err)
		}
		if !s.HoldForBusy(m) {
			t.Fatalf("message %d was not held for a busy session", i)
		}
		if i == 1 {
			fileID = m.ID
		} else {
			plainIDs = append(plainIDs, m.ID)
		}
	}
	busyStatus(t, s, b, "idle")
	carriers, n, err := s.ReleaseBusyHold(b, false)
	if err != nil || n != 3 {
		t.Fatalf("release: n=%d err=%v", n, err)
	}
	if len(carriers) != 2 {
		t.Fatalf("released %d messages, want the file message alone and one batch of the rest", len(carriers))
	}
	var alone, batch protocol.Message
	for _, carrier := range carriers {
		if len(carrier.Batch) == 0 {
			alone = carrier
		} else {
			batch = carrier
		}
	}
	if alone.ID != fileID || len(alone.Files) != 1 {
		t.Fatalf("file message released as %q with %d files, want %q alone with its file", alone.ID, len(alone.Files), fileID)
	}
	if len(batch.Batch) != 2 || len(batch.Files) != 0 {
		t.Fatalf("batch carries %d messages and %d files, want 2 plain messages and no files", len(batch.Batch), len(batch.Files))
	}
	for _, m := range batch.Batch {
		if len(m.Files) != 0 {
			t.Fatalf("batch member %s has files, want none", m.ID)
		}
	}
	if batch.Batch[0].ID != plainIDs[1] || batch.Batch[1].ID != plainIDs[0] {
		t.Fatalf("batch order = %s, %s; want newest first", batch.Batch[0].ID, batch.Batch[1].ID)
	}
}

// releaseOne releases a session's held messages for a test that expects them
// to go as one carrier, which holds when no held message has files.
func releaseOne(t *testing.T, s *Store, id string, force bool) (protocol.Message, int, error) {
	t.Helper()
	carriers, n, err := s.ReleaseBusyHold(id, force)
	if len(carriers) > 1 {
		t.Fatalf("released %d carriers, want one", len(carriers))
	}
	if len(carriers) == 0 {
		return protocol.Message{}, n, err
	}
	return carriers[0], n, err
}

package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func testSession(t *testing.T, n *node, native string) *localSession {
	t.Helper()
	n.d.mu.Lock()
	defer n.d.mu.Unlock()
	ls := n.d.sessions[key("pi", native)]
	if ls == nil {
		t.Fatalf("session %s is not known", native)
	}
	return ls
}

func TestSessionLastActiveSurvivesRestart(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{adapter: &fakeAdapter{harness: "pi"}})
	n.d.SetSessions("pi", []protocol.SessionInfo{{NativeID: "s1", Status: "idle", Cwd: "/work/x"}})

	active := time.Now().Add(-3 * time.Hour).Truncate(time.Second)
	n.d.touchSession(testSession(t, n, "s1"), active)
	if got := n.d.infoOf(testSession(t, n, "s1")).LastActive; got != active.Unix() {
		t.Fatalf("last active before restart: %d, want %d", got, active.Unix())
	}

	n.restart()
	n.d.SetSessions("pi", []protocol.SessionInfo{{NativeID: "s1", Status: "idle", Cwd: "/work/x"}})
	ls := testSession(t, n, "s1")
	if got := n.d.infoOf(ls).LastActive; got != active.Unix() {
		t.Fatalf("last active after restart: %d, want %d", got, active.Unix())
	}

	// The same discovery report again must not move it, even though the
	// daemon was restarted between the two.
	before := n.d.infoOf(ls).LastActive
	n.d.SetSessions("pi", []protocol.SessionInfo{{NativeID: "s1", Status: "idle", Cwd: "/work/x"}})
	if got := n.d.infoOf(testSession(t, n, "s1")).LastActive; got != before {
		t.Fatalf("a re-report moved last active from %d to %d", before, got)
	}

	// A real status change is activity.
	n.d.SetSessions("pi", []protocol.SessionInfo{{NativeID: "s1", Status: "busy", Cwd: "/work/x"}})
	if got := n.d.infoOf(testSession(t, n, "s1")).LastActive; got <= before {
		t.Fatalf("a status change did not move last active: %d, still %d", got, before)
	}
}

// A local caller alias can share the owner's gateway ID. Message activity must
// not upsert that alias as a new session and change which owner its workers see.
func TestMessageActivityKeepsSharedGatewayID(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{adapter: &fakeAdapter{harness: "pi"}})
	eventually(t, "owner session", func() bool { return n.d.ownerSessionID() != "" })
	owner := n.d.ownerSessionID()
	n.d.SetSessions("pi", []protocol.SessionInfo{{NativeID: "recipient", Status: "idle"}})
	recipient := n.d.SessionID("pi", "recipient")
	alias := &localSession{info: protocol.SessionInfo{Harness: "claude", NativeID: "caller-alias"}, sessionID: owner}
	n.d.mu.Lock()
	n.d.sessions[key("claude", "caller-alias")] = alias
	n.d.mu.Unlock()
	if got := n.d.activitySession(owner); got != nil {
		t.Fatal("ambiguous gateway ID selected a session for activity")
	}
	for range 16 {
		if err := n.d.Send(context.Background(), protocol.MsgSend{Sender: owner, Recipient: recipient,
			Type: protocol.TypeNotification, Body: "activity"}); err != nil {
			t.Fatal(err)
		}
		n.d.mu.Lock()
		got := alias.sessionID
		n.d.mu.Unlock()
		if got != owner {
			t.Fatalf("message activity re-registered caller alias: %s, want %s", got, owner)
		}
	}
}

func TestRepeatedWaitKeepsActivity(t *testing.T) {
	d := &Daemon{sessions: map[string]*localSession{}}
	ls := &localSession{info: protocol.SessionInfo{Harness: "pi", NativeID: "waiting"}}
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	d.setWaiting(ls, &protocol.Waiting{Kind: protocol.WaitingQuestion, Text: "Continue?", Since: old.Unix()})
	// The harness re-reports the existing wait without supplying its start.
	d.mu.Lock()
	ls.lastActive = old
	d.mu.Unlock()
	d.setWaiting(ls, &protocol.Waiting{Kind: protocol.WaitingQuestion, Text: "Continue?"})
	if got := d.infoOf(ls); got.LastActive != old.Unix() || got.Waiting.Since != old.Unix() {
		t.Fatalf("a repeated wait moved activity: %+v", got)
	}
}

// A restart re-registers every session: the plugin says hello, then reports
// the status it had (dormant for an OpenCode session quiet for a day). Neither
// the first status nor a change between idle, dormant and offline is a turn.
// Only becoming busy, or becoming idle after busy, moves the activity time.
func TestRestartWithoutATurnKeepsActivity(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{adapter: &fakeAdapter{harness: "pi"}})
	harnesses := []string{"opencode", "claude", "codex", "pi"}
	statuses := []string{"idle", "dormant"}
	infos := map[string][]protocol.SessionInfo{}
	for _, h := range harnesses {
		for _, s := range statuses {
			infos[h] = append(infos[h], protocol.SessionInfo{NativeID: h + "-" + s, Status: s, Cwd: "/work/" + h})
		}
		n.d.SetSessions(h, infos[h])
	}
	active := time.Now().Add(-3 * time.Hour).Truncate(time.Second)
	for _, h := range harnesses {
		for _, in := range infos[h] {
			n.d.touchSession(testSessionOf(t, n, h, in.NativeID), active)
		}
	}

	n.restart()
	for _, h := range harnesses {
		n.d.SetSessions(h, infos[h])
	}
	for _, h := range harnesses {
		for _, in := range infos[h] {
			ls := testSessionOf(t, n, h, in.NativeID)
			if got := n.d.infoOf(ls).LastActive; got != active.Unix() {
				t.Fatalf("%s %s after restart: last active %d, want %d", h, in.NativeID, got, active.Unix())
			}
		}
	}

	// Quiet sessions may move between idle, dormant and offline without doing
	// anything.
	for _, h := range harnesses {
		var updated []protocol.SessionInfo
		for _, in := range infos[h] {
			next := "dormant"
			if in.Status == "dormant" {
				next = "idle"
			}
			updated = append(updated, protocol.SessionInfo{NativeID: in.NativeID, Status: next, Cwd: in.Cwd})
		}
		n.d.SetSessions(h, updated)
		for _, in := range updated {
			ls := testSessionOf(t, n, h, in.NativeID)
			if got := n.d.infoOf(ls).LastActive; got != active.Unix() {
				t.Fatalf("%s %s -> %s moved last active to %d, want %d", h, in.NativeID, in.Status, got, active.Unix())
			}
		}
	}

	// A turn still counts: busy starts it, idle ends it.
	for _, h := range harnesses {
		native := h + "-idle"
		n.d.SetSessions(h, []protocol.SessionInfo{
			{NativeID: native, Status: "busy", Cwd: "/work/" + h},
			{NativeID: h + "-dormant", Status: "idle", Cwd: "/work/" + h},
		})
		ls := testSessionOf(t, n, h, native)
		busyAt := n.d.infoOf(ls).LastActive
		if busyAt <= active.Unix() {
			t.Fatalf("%s: a turn start did not move last active: %d, still %d", h, busyAt, active.Unix())
		}
		n.d.SetSessions(h, []protocol.SessionInfo{
			{NativeID: native, Status: "idle", Cwd: "/work/" + h},
			{NativeID: h + "-dormant", Status: "idle", Cwd: "/work/" + h},
		})
		if got := n.d.infoOf(testSessionOf(t, n, h, native)).LastActive; got < busyAt {
			t.Fatalf("%s: a turn end did not move last active: %d, before %d", h, got, busyAt)
		}
	}
}

// The OpenCode plugin says hello with idle or busy and then reports dormant
// for a session that has been quiet. That second status must not look like
// activity, on a fresh daemon or after a restart.
func TestPluginDormantStatusIsNotActivity(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{exe: "/opt/x/node_modules/@opencode/cli/bin/opencode.exe"})
	lc, _ := helloOpenCode(t, n, "ses_quiet", "/work/quiet")
	ls := testSessionOf(t, n, "opencode", "ses_quiet")
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	n.d.touchSession(ls, old)
	lc.mustCall("status", map[string]any{"status": "dormant"})
	if got := n.d.infoOf(testSessionOf(t, n, "opencode", "ses_quiet")).LastActive; got != old.Unix() {
		t.Fatalf("idle -> dormant moved last active to %d, want %d", got, old.Unix())
	}
	lc.mustCall("status", map[string]any{"status": "idle"})
	if got := n.d.infoOf(testSessionOf(t, n, "opencode", "ses_quiet")).LastActive; got != old.Unix() {
		t.Fatalf("dormant -> idle moved last active to %d, want %d", got, old.Unix())
	}

	// A restart: the plugin reconnects, says idle in hello, then dormant.
	lc.c.Close()
	n.restart()
	lc = dialLocal(t, n.sock)
	lc.AutoAck = true
	lc.mustCall("hello", map[string]any{"client": "opencode", "harness": "opencode",
		"native_id": "ses_quiet", "cwd": "/work/quiet", "status": "idle"})
	lc.mustCall("status", map[string]any{"status": "dormant"})
	if got := n.d.infoOf(testSessionOf(t, n, "opencode", "ses_quiet")).LastActive; got != old.Unix() {
		t.Fatalf("re-registration moved last active to %d, want %d", got, old.Unix())
	}

	// A real turn moves it.
	lc.mustCall("status", map[string]any{"status": "busy"})
	busyAt := n.d.infoOf(testSessionOf(t, n, "opencode", "ses_quiet")).LastActive
	if busyAt <= old.Unix() {
		t.Fatalf("a turn start did not move last active: %d, still %d", busyAt, old.Unix())
	}
	lc.mustCall("status", map[string]any{"status": "idle"})
	if got := n.d.infoOf(testSessionOf(t, n, "opencode", "ses_quiet")).LastActive; got < busyAt {
		t.Fatalf("a turn end did not move last active: %d, before %d", got, busyAt)
	}
}

func testSessionOf(t *testing.T, n *node, harness, native string) *localSession {
	t.Helper()
	n.d.mu.Lock()
	defer n.d.mu.Unlock()
	ls := n.d.sessions[key(harness, native)]
	if ls == nil {
		t.Fatalf("session %s/%s is not known", harness, native)
	}
	return ls
}

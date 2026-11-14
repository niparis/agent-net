package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// upsertLog records the session.upsert calls a daemon sends. The real gateway
// still answers them.
type upsertLog struct {
	mu    sync.Mutex
	calls []protocol.SessionInfo
}

func (u *upsertLog) hook(_ context.Context, method string, params any) (json.RawMessage, bool, error) {
	if method == "session.upsert" {
		u.mu.Lock()
		u.calls = append(u.calls, params.(protocol.SessionUpsert).Session)
		u.mu.Unlock()
	}
	return nil, false, nil
}

func (u *upsertLog) count() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return len(u.calls)
}

func (u *upsertLog) last() protocol.SessionInfo {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.calls[len(u.calls)-1]
}

func pushSkipNode(t *testing.T, activityEvery, keepalive time.Duration) (*node, *upsertLog, *localSession) {
	t.Helper()
	g := startGateway(t, t.TempDir(), "")
	log := &upsertLog{}
	n := startNode(t, g, nodeOpts{cfg: func(c *Config) {
		c.testGatewayCall = log.hook
		c.testPushActivityEvery, c.testPushKeepalive = activityEvery, keepalive
	}})
	n.d.SetSessions("codex", []protocol.SessionInfo{{NativeID: "t-skip", Cwd: "/work/api", Status: "idle"}})
	eventually(t, "the session is registered", func() bool { return n.d.SessionID("codex", "t-skip") != "" })
	n.d.mu.Lock()
	ls := n.d.sessions[key("codex", "t-skip")]
	n.d.mu.Unlock()
	return n, log, ls
}

// backdateReport makes the gateway's last report look d older, as if that
// much time had passed. Tests use it instead of waiting for an interval.
func backdateReport(ls *localSession, d time.Duration) {
	ls.pushMu.Lock()
	ls.push.at = ls.push.at.Add(-d)
	ls.pushMu.Unlock()
}

func TestPushSessionSkipsUnchangedReport(t *testing.T) {
	n, log, ls := pushSkipNode(t, time.Hour, time.Hour)
	before := log.count()
	for i := 0; i < 5; i++ {
		n.d.pushSessionChanged(ls)
	}
	if got := log.count(); got != before {
		t.Fatalf("%d unchanged reports sent %d session.upsert calls, want 0", 5, got-before)
	}
}

func TestPushSessionSendsChangedReport(t *testing.T) {
	n, log, ls := pushSkipNode(t, time.Hour, time.Hour)
	before := log.count()
	n.d.mu.Lock()
	ls.info.Status = "busy"
	n.d.mu.Unlock()
	n.d.pushSessionChanged(ls)
	if got := log.count(); got != before+1 || log.last().Status != "busy" {
		t.Fatalf("a changed status sent %d calls (last status %q), want one with busy", got-before, log.last().Status)
	}
}

// Output arrives many times a second. Activity time alone is sent at most once
// per interval. The interval is an hour so that the held reports stay held on
// a slow machine; backdateReport stands in for the wait.
func TestPushSessionCoalescesActivity(t *testing.T) {
	n, log, ls := pushSkipNode(t, time.Hour, time.Hour)
	before := log.count()
	var last time.Time
	for i := 0; i < 5; i++ {
		last = time.Now().Add(time.Duration(i+1) * time.Second)
		n.d.mu.Lock()
		ls.harnessActivity, ls.lastActive = last, last
		n.d.mu.Unlock()
		n.d.pushSessionChanged(ls)
	}
	if got := log.count(); got != before {
		t.Fatalf("activity-only reports sent %d calls at once, want 0", got-before)
	}
	backdateReport(ls, time.Hour)
	n.d.pushSessionChanged(ls)
	if got := log.count(); got != before+1 || log.last().HarnessActivity != last.Unix() {
		t.Fatalf("after the interval, %d calls with the newest activity %v, want one with %v", got-before, log.last().HarnessActivity, last.Unix())
	}
	n.d.pushSessionChanged(ls)
	if got := log.count(); got != before+1 {
		t.Fatalf("a second report in the same interval sent %d calls, want 1", got-before)
	}
}

// A held activity time goes out by itself when its interval ends, with no
// further report from the daemon. The test only waits for that send, so a
// slow machine that sends at once still passes.
func TestPushSessionSendsHeldActivityOnItsOwn(t *testing.T) {
	n, log, ls := pushSkipNode(t, 300*time.Millisecond, time.Hour)
	before := log.count()
	last := time.Now().Add(time.Second)
	n.d.mu.Lock()
	ls.harnessActivity, ls.lastActive = last, last
	n.d.mu.Unlock()
	n.d.pushSessionChanged(ls)
	eventually(t, "the held activity time is sent", func() bool {
		return log.count() > before && log.last().HarnessActivity == last.Unix()
	})
}

// Liveness: a session that reports nothing new is still reported on the slow
// keepalive, so the gateway keeps its last_seen fresh. This test waits for
// that report. It does not check for silence before it: at a short interval,
// a slow machine can pass the keepalive before the check runs. Holding back
// before the keepalive is TestPushSessionSkipsUnchangedReport's job.
func TestPushSessionKeepalive(t *testing.T) {
	_, log, _ := pushSkipNode(t, time.Hour, 300*time.Millisecond)
	before := log.count()
	eventually(t, "the keepalive report", func() bool { return log.count() > before })
}

// A new link starts from the gateway's snapshot, so the next report goes out
// even when nothing changed since the last one.
func TestPushSessionSendsAgainAfterReconnect(t *testing.T) {
	n, log, ls := pushSkipNode(t, time.Hour, time.Hour)
	before := log.count()
	n.d.mu.Lock()
	n.d.linkEpoch++
	n.d.mu.Unlock()
	n.d.pushSessionChanged(ls)
	if got := log.count(); got != before+1 {
		t.Fatalf("a report after a new link sent %d calls, want 1", got-before)
	}
}

// A removal makes the gateway forget the session. The report that brings it
// back repeats the last one, and must still go out.
func TestPushSessionSendsAfterRemoval(t *testing.T) {
	n, log, ls := pushSkipNode(t, time.Hour, time.Hour)
	before := log.count()
	if _, err := n.d.call(context.Background(), "session.remove", protocol.SessionRemove{SessionID: n.d.SessionID("codex", "t-skip")}); err != nil {
		t.Fatal(err)
	}
	n.d.pushSessionChanged(ls)
	if got := log.count(); got != before+1 {
		t.Fatalf("a report after a removal sent %d calls, want 1", got-before)
	}
}

// A busy worker's token counters move on every stats tick. They wait for the
// activity interval like the activity time does, and any other worker field
// goes out at once.
func TestPushStateHoldsWorkerTokenCounters(t *testing.T) {
	timing := pushTiming{activity: 30 * time.Second, keepalive: 2 * time.Minute}
	now := time.Now()
	in := protocol.SessionInfo{Harness: "codex", NativeID: "w", Worker: &protocol.WorkerInfo{ID: "w_1", Task: "a"}}
	var p pushState
	p.record(in, 1, now)
	tokens := in
	tokens.Worker = &protocol.WorkerInfo{ID: "w_1", Task: "a", Tokens: 500, ContextTokens: 100}
	if wait, send := p.due(tokens, 1, now.Add(time.Second), timing); send || wait <= 0 {
		t.Fatalf("a token-only change: send=%v wait=%v, want held", send, wait)
	}
	if _, send := p.due(tokens, 1, now.Add(31*time.Second), timing); !send {
		t.Fatal("a token-only change was not sent after the interval")
	}
	task := in
	task.Worker = &protocol.WorkerInfo{ID: "w_1", Task: "b"}
	if _, send := p.due(task, 1, now.Add(time.Second), timing); !send {
		t.Fatal("a changed task was held")
	}
}

// One OpenCode service holds many sessions, and each one's service activity
// time moves with its work. It waits for the activity interval like the other
// activity times, so 12 sessions that report it every second send at most one
// session.upsert each per 30 seconds while nothing else changes.
func TestPushStateHoldsOpenCodeActivity(t *testing.T) {
	timing := pushTiming{activity: 30 * time.Second, keepalive: 2 * time.Minute}
	start := time.Now()
	const sessions, seconds = 12, 90
	states := make([]pushState, sessions)
	infos := make([]protocol.SessionInfo, sessions)
	for i := range infos {
		infos[i] = protocol.SessionInfo{Harness: "opencode", NativeID: fmt.Sprintf("ses_%d", i), Cwd: "/work/api", Status: "busy", OpenCodeActivity: start.UnixMilli()}
		states[i].record(infos[i], 1, start)
	}
	sent := make([]int, sessions)
	for s := 1; s <= seconds; s++ {
		now := start.Add(time.Duration(s) * time.Second)
		for i := range infos {
			infos[i].OpenCodeActivity = now.UnixMilli()
			if _, send := states[i].due(infos[i], 1, now, timing); send {
				states[i].record(infos[i], 1, now)
				sent[i]++
			}
		}
	}
	for i, n := range sent {
		if n > seconds/30 {
			t.Fatalf("session %d sent %d reports in %d s of activity alone, want at most %d", i, n, seconds, seconds/30)
		}
	}
}

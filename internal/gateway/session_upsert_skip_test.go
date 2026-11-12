package gateway

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A daemon reports a session on every activity change. A report that says
// what the gateway already stores must write nothing and must not run the
// delivery sweeps that follow a real change.
type upsertProbe struct {
	g *Gateway
	n string
	c *clock
}

func newUpsertProbe(t *testing.T) *upsertProbe {
	t.Helper()
	g, n, _ := toggleFixture(t)
	c := &clock{t: time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)}
	g.store.now = c.now
	return &upsertProbe{g: g, n: n, c: c}
}

func (p *upsertProbe) report(t *testing.T, in protocol.SessionInfo) string {
	t.Helper()
	raw, _ := json.Marshal(protocol.SessionUpsert{Session: in})
	res, err := p.g.handle(context.Background(), &nodeConn{id: p.n}, "session.upsert", raw)
	if err != nil {
		t.Fatal(err)
	}
	p.g.standingWG.Wait()
	return sessionUpsertID(t, res)
}

func (p *upsertProbe) row(t *testing.T, id string) (lastSeen int64, online int, status string) {
	t.Helper()
	if err := p.g.store.db.QueryRow(`SELECT last_seen,online,status FROM sessions WHERE id=?`, id).Scan(&lastSeen, &online, &status); err != nil {
		t.Fatal(err)
	}
	return
}

func sessionUpsertID(t *testing.T, res any) string {
	t.Helper()
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	var r struct {
		SessionID string `json:"session_id"`
	}
	if json.Unmarshal(raw, &r) != nil || r.SessionID == "" {
		t.Fatalf("upsert result = %s, want a session_id", raw)
	}
	return r.SessionID
}

var probeSession = protocol.SessionInfo{Harness: "claude", NativeID: "s-skip", Cwd: "/repo/one", Status: "idle",
	Repo: protocol.RepoInfo{Root: "/repo/one", Worktree: "/repo/one", Project: "one"}}

func TestSessionUpsertUnchangedWritesNothing(t *testing.T) {
	p := newUpsertProbe(t)
	id := p.report(t, probeSession)
	seen, _, _ := p.row(t, id)
	runs := p.g.stepDispatchRuns.Load()
	if runs == 0 {
		t.Fatal("a new session did not run the step dispatch")
	}
	for i := 0; i < 5; i++ {
		p.c.t = p.c.t.Add(10 * time.Second)
		if got := p.report(t, probeSession); got != id {
			t.Fatalf("session id changed: %s, want %s", got, id)
		}
	}
	if now, _, _ := p.row(t, id); now != seen {
		t.Fatalf("last_seen moved from %d to %d: an unchanged report wrote the row", seen, now)
	}
	if got := p.g.stepDispatchRuns.Load(); got != runs {
		t.Fatalf("step dispatch ran %d times for unchanged reports, want 0", got-runs)
	}
}

func TestSessionUpsertChangedFieldWrites(t *testing.T) {
	p := newUpsertProbe(t)
	id := p.report(t, probeSession)
	seen, _, _ := p.row(t, id)
	runs := p.g.stepDispatchRuns.Load()
	p.c.t = p.c.t.Add(10 * time.Second)
	busy := probeSession
	busy.Status = "busy"
	p.report(t, busy)
	now, _, status := p.row(t, id)
	if now == seen || status != "busy" {
		t.Fatalf("a changed status was not written: last_seen %d -> %d, status %q", seen, now, status)
	}
	if p.g.stepDispatchRuns.Load() == runs {
		t.Fatal("a status change did not run the step dispatch")
	}
	// Activity time alone is stored too, but it is not a reason to sweep.
	runs = p.g.stepDispatchRuns.Load()
	p.c.t = p.c.t.Add(10 * time.Second)
	busy.HarnessActivity = p.c.t.Unix()
	busy.LastActive = p.c.t.Unix()
	p.report(t, busy)
	var activity int64
	if err := p.g.store.db.QueryRow(`SELECT harness_activity FROM sessions WHERE id=?`, id).Scan(&activity); err != nil {
		t.Fatal(err)
	}
	if activity != busy.HarnessActivity*int64(time.Second) {
		t.Fatalf("harness_activity = %d, want it stored", activity)
	}
	if got := p.g.stepDispatchRuns.Load(); got != runs {
		t.Fatalf("an activity-only report ran the step dispatch %d times", got-runs)
	}
}

// Liveness: a session the gateway took offline comes back on the next report,
// even when the report is the same as the last one, and a long quiet session
// still has its last_seen refreshed.
func TestSessionUpsertSameReportStillBringsOfflineSessionBack(t *testing.T) {
	p := newUpsertProbe(t)
	id := p.report(t, probeSession)
	if err := p.g.store.SetSessionOffline(p.n, id); err != nil {
		t.Fatal(err)
	}
	p.c.t = p.c.t.Add(10 * time.Second)
	p.report(t, probeSession)
	if _, online, _ := p.row(t, id); online != 1 {
		t.Fatal("an unchanged report left an offline session offline")
	}
}

func TestSessionUpsertKeepaliveRefreshesLastSeen(t *testing.T) {
	p := newUpsertProbe(t)
	id := p.report(t, probeSession)
	seen, _, _ := p.row(t, id)
	p.c.t = p.c.t.Add(sessionKeepalive + time.Second)
	p.report(t, probeSession)
	if now, _, _ := p.row(t, id); now <= seen {
		t.Fatalf("last_seen = %d after the keepalive, want it past %d", now, seen)
	}
}

// A column that another path changed is put right by the next report, even
// when the report repeats the last one (a Pi name repair relies on this).
func TestSessionUpsertRepairsColumnChangedElsewhere(t *testing.T) {
	p := newUpsertProbe(t)
	id := p.report(t, probeSession)
	var want string
	if err := p.g.store.db.QueryRow(`SELECT name FROM sessions WHERE id=?`, id).Scan(&want); err != nil {
		t.Fatal(err)
	}
	if _, err := p.g.store.db.Exec(`UPDATE sessions SET name='drifted' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	p.c.t = p.c.t.Add(10 * time.Second)
	p.report(t, probeSession)
	var got string
	if err := p.g.store.db.QueryRow(`SELECT name FROM sessions WHERE id=?`, id).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("name = %q after the report, want %q", got, want)
	}
}

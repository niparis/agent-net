package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

type ctoTestClock struct {
	mu sync.Mutex
	t  time.Time
}

func newCTOTestClock() *ctoTestClock {
	return &ctoTestClock{t: time.Unix(1_700_000_000, 0)}
}

func (c *ctoTestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *ctoTestClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type ctoFixture struct {
	n     *node
	cli   *localClient
	id    string
	rec   fakeOCRecord
	clock *ctoTestClock
}

func startCTOCompactionFixture(t *testing.T) *ctoFixture {
	return startCTOCompactionFixtureEvery(t, 0)
}

// startCTOCompactionFixtureEvery runs the daemon's background loop at the
// given interval; zero drives compaction by calling ctoCompactOnce directly.
func startCTOCompactionFixtureEvery(t *testing.T, every time.Duration) *ctoFixture {
	t.Helper()
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{detect: fakeInventory, interval: time.Hour, cfg: func(c *Config) { c.HarnessPaths = map[string]string{"opencode": os.Args[0]} }})
	// Tests drive compaction with a fake clock, one call at a time; the loop
	// test uses a short real interval instead.
	n.d.ctoCompactEvery = every
	clock := newCTOTestClock()
	n.d.ctoNow = clock.Now
	cli := cliClient(t, n)
	cli.mustCall("cto_send", map[string]string{"text": "start"})
	id, err := n.d.ctoSession()
	if err != nil || id == "" {
		t.Fatalf("session: %q %v", id, err)
	}
	return &ctoFixture{n: n, cli: cli, id: id, rec: readFake(t, filepath.Join(n.cfg.StateDir, "cto")), clock: clock}
}

func (f *ctoFixture) writeMessages(t *testing.T, items []map[string]any) {
	t.Helper()
	path := filepath.Join(f.rec.Env["XDG_DATA_HOME"], "fake-session-"+f.id+".json")
	b, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f *ctoFixture) setActive(t *testing.T, active bool) {
	t.Helper()
	path := filepath.Join(f.rec.Env["XDG_DATA_HOME"], "fake-active")
	if !active {
		os.Remove(path)
		return
	}
	if err := os.WriteFile(path, []byte(f.id), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f *ctoFixture) send(t *testing.T, text string) {
	t.Helper()
	f.cli.mustCall("cto_send", map[string]string{"text": text})
}

func (f *ctoFixture) tryCompactCount() (int, bool) {
	data, err := os.ReadFile(filepath.Join(f.n.cfg.StateDir, "cto", "fake-compact.json"))
	if err != nil {
		return 0, false
	}
	var calls []json.RawMessage
	if json.Unmarshal(data, &calls) != nil {
		return 0, false
	}
	return len(calls), true
}

func (f *ctoFixture) compactCount(t *testing.T) int {
	t.Helper()
	if n, ok := f.tryCompactCount(); ok {
		return n
	}
	if _, err := os.Stat(filepath.Join(f.n.cfg.StateDir, "cto", "fake-compact.json")); os.IsNotExist(err) {
		return 0
	}
	t.Fatal("unreadable compaction record")
	return 0
}

func (f *ctoFixture) actions(t *testing.T) []string {
	t.Helper()
	var events protocol.CTOEvents
	if err := json.Unmarshal(f.cli.mustCall("cto_events", map[string]any{"index": 0}), &events); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, a := range events.Actions {
		out = append(out, a.Text)
	}
	return out
}

func ctoAssistant(id string, contextTokens int64) map[string]any {
	return map[string]any{
		"id": id, "type": "assistant",
		"time":   map[string]any{"created": int64(1_700_000_000_000)},
		"tokens": map[string]any{"input": contextTokens, "output": 10, "reasoning": 0, "cache": map[string]any{"read": 0, "write": 0}},
	}
}

func ctoUser(text string) map[string]any {
	return map[string]any{"id": "msg_" + text, "type": "user", "text": text, "time": map[string]any{"created": int64(1_700_000_000_000)}}
}

func ctoCompactionDone() map[string]any {
	return map[string]any{
		"id": "msg_cmp", "type": "compaction", "status": "completed", "reason": "manual",
		"summary": "short", "time": map[string]any{"created": int64(1_700_000_000_500)},
	}
}

func TestCTOCompactionHalfWindow(t *testing.T) {
	f := startCTOCompactionFixture(t)
	ctx := context.Background()
	f.writeMessages(t, []map[string]any{ctoAssistant("msg_49", 490_000), ctoUser("work")})
	if err := f.n.d.ctoCompactOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.compactCount(t); got != 0 {
		t.Fatalf("49%% compacted: %d", got)
	}
	f.writeMessages(t, []map[string]any{ctoAssistant("msg_50", 500_000), ctoUser("work")})
	if err := f.n.d.ctoCompactOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.compactCount(t); got != 1 {
		t.Fatalf("50%% compactions = %d", got)
	}
}

func TestCTOCompactionThreshold(t *testing.T) {
	f := startCTOCompactionFixture(t)
	ctx := context.Background()
	// A completed turn at 800k of a 1M window: above 70%, compact at once.
	f.writeMessages(t, []map[string]any{ctoAssistant("msg_old", 1000), ctoUser("start")})
	f.send(t, "work")
	f.writeMessages(t, []map[string]any{ctoAssistant("msg_big", 800_000), ctoUser("work"), ctoAssistant("msg_old", 1000)})
	if err := f.n.d.ctoCompactOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.compactCount(t); got != 1 {
		t.Fatalf("compactions = %d, want 1", got)
	}
	// The next reply reports the context after compaction.
	f.writeMessages(t, []map[string]any{ctoAssistant("msg_after", 14_000), ctoCompactionDone(), ctoAssistant("msg_big", 800_000), ctoUser("work")})
	if err := f.n.d.ctoCompactOnce(ctx); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(f.actions(t), "|")
	if !strings.Contains(joined, "compacted: 800k -> 14k tokens") {
		t.Fatalf("chat line missing: %s", joined)
	}
}

func TestCTOCompactionLoopRunsAfterAReply(t *testing.T) {
	f := startCTOCompactionFixtureEvery(t, 20*time.Millisecond)
	f.writeMessages(t, []map[string]any{ctoAssistant("msg_old", 1000), ctoUser("start")})
	f.send(t, "work")
	f.writeMessages(t, []map[string]any{ctoAssistant("msg_big", 800_000), ctoUser("work")})
	eventually(t, "the background loop compacts above the threshold", func() bool {
		n, ok := f.tryCompactCount()
		return ok && n == 1
	})
}

func TestCTOCompactionIdleBeforeCacheExpiry(t *testing.T) {
	f := startCTOCompactionFixture(t)
	ctx := context.Background()
	// Record the session's base context: 45k is below the 70% threshold.
	f.writeMessages(t, []map[string]any{ctoAssistant("msg_old", 1000), ctoUser("start")})
	f.send(t, "work")
	f.writeMessages(t, []map[string]any{ctoAssistant("msg_base", 45_000), ctoUser("work")})
	if err := f.n.d.ctoCompactOnce(ctx); err != nil {
		t.Fatal(err)
	}
	// The next turn grows it by 70k, above the default 60k.
	f.send(t, "more")
	f.writeMessages(t, []map[string]any{ctoAssistant("msg_grown", 115_000), ctoUser("more"), ctoAssistant("msg_base", 45_000), ctoUser("work")})
	if err := f.n.d.ctoCompactOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.compactCount(t); got != 0 {
		t.Fatalf("compacted in a turn: %d", got)
	}
	f.clock.Advance(269 * time.Second)
	if err := f.n.d.ctoCompactOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.compactCount(t); got != 0 {
		t.Fatalf("compacted too early: %d", got)
	}
	f.clock.Advance(2 * time.Second)
	if err := f.n.d.ctoCompactOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.compactCount(t); got != 1 {
		t.Fatalf("idle compaction = %d, want 1", got)
	}
}

// The base context of the CTO conversation (instructions, tools, graph) is
// already near the old 40k floor. An idle pause alone must not compact it.
func TestCTOCompactionIdleIgnoresTheBaseContext(t *testing.T) {
	f := startCTOCompactionFixture(t)
	ctx := context.Background()
	f.writeMessages(t, []map[string]any{ctoAssistant("msg_old", 1000), ctoUser("start")})
	f.send(t, "work")
	f.writeMessages(t, []map[string]any{ctoAssistant("msg_base", 45_000), ctoUser("work")})
	if err := f.n.d.ctoCompactOnce(ctx); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(5 * time.Minute)
	if err := f.n.d.ctoCompactOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.compactCount(t); got != 1 {
		t.Fatalf("idle did not compact the base context: %d", got)
	}
}

// Idle compaction now needs at least the growth setting above the context
// recorded at the last compaction (or at session start), and only once per
// idle period.
func TestCTOCompactionIdleAfterGrowth(t *testing.T) {
	f := startCTOCompactionFixture(t)
	ctx := context.Background()
	f.writeMessages(t, []map[string]any{ctoAssistant("msg_old", 1000), ctoUser("start")})
	f.send(t, "work")
	f.writeMessages(t, []map[string]any{ctoAssistant("msg_base", 45_000), ctoUser("work")})
	if err := f.n.d.ctoCompactOnce(ctx); err != nil {
		t.Fatal(err)
	}
	// The next turn grows the context by 70k, above the default 60k.
	f.send(t, "more")
	f.writeMessages(t, []map[string]any{ctoAssistant("msg_grown", 115_000), ctoUser("more"), ctoAssistant("msg_base", 45_000), ctoUser("work")})
	if err := f.n.d.ctoCompactOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.compactCount(t); got != 0 {
		t.Fatalf("compacted before the idle window: %d", got)
	}
	f.clock.Advance(5 * time.Minute)
	if err := f.n.d.ctoCompactOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.compactCount(t); got != 1 {
		t.Fatalf("growth above the baseline did not compact: %d", got)
	}
	f.clock.Advance(5 * time.Minute)
	if err := f.n.d.ctoCompactOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.compactCount(t); got != 1 {
		t.Fatalf("compacted twice in one idle period: %d", got)
	}
}

func TestCTOCompactionCacheExpiryDisabled(t *testing.T) {
	f := startCTOCompactionFixture(t)
	enabled := false
	settings := protocol.CTOCompactionSettings{CacheExpiryEnabled: &enabled}
	f.cli.mustCall("cto_settings", protocol.CTOSettingsRequest{Compaction: &settings})
	f.writeMessages(t, []map[string]any{ctoAssistant("msg", 45_000), ctoUser("work")})
	if err := f.n.d.ctoCompactOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(time.Hour)
	if err := f.n.d.ctoCompactOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := f.compactCount(t); got != 0 {
		t.Fatalf("disabled idle compactions = %d", got)
	}
}

func TestCTOCompactionOncePerIdlePeriod(t *testing.T) {
	f := startCTOCompactionFixture(t)
	ctx := context.Background()
	f.writeMessages(t, []map[string]any{ctoAssistant("msg_old", 1000), ctoUser("start")})
	f.send(t, "work")
	f.writeMessages(t, []map[string]any{ctoAssistant("msg_base", 45_000), ctoUser("work")})
	if err := f.n.d.ctoCompactOnce(ctx); err != nil {
		t.Fatal(err)
	}
	// The first idle period grows by 70k and compacts once.
	f.send(t, "more")
	f.writeMessages(t, []map[string]any{ctoAssistant("msg_grown", 115_000), ctoUser("more"), ctoAssistant("msg_base", 45_000), ctoUser("work")})
	if err := f.n.d.ctoCompactOnce(ctx); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(5 * time.Minute)
	if err := f.n.d.ctoCompactOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.compactCount(t); got != 1 {
		t.Fatalf("first idle compaction = %d, want 1", got)
	}
	f.clock.Advance(5 * time.Minute)
	if err := f.n.d.ctoCompactOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.compactCount(t); got != 1 {
		t.Fatalf("compacted twice in one idle period: %d", got)
	}
	// The reply after the compaction sets the new baseline.
	f.writeMessages(t, []map[string]any{ctoAssistant("msg_after", 14_000), ctoCompactionDone(), ctoAssistant("msg_grown", 115_000), ctoUser("more")})
	if err := f.n.d.ctoCompactOnce(ctx); err != nil {
		t.Fatal(err)
	}
	// A new turn starts a new idle period, and another 70k of growth compacts.
	f.send(t, "again")
	f.writeMessages(t, []map[string]any{ctoAssistant("msg_grown2", 84_000), ctoUser("again"), ctoAssistant("msg_after", 14_000), ctoCompactionDone(), ctoAssistant("msg_grown", 115_000), ctoUser("more")})
	if err := f.n.d.ctoCompactOnce(ctx); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(5 * time.Minute)
	if err := f.n.d.ctoCompactOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.compactCount(t); got != 2 {
		t.Fatalf("new idle period did not compact: %d", got)
	}
}

func TestCTOCompactionNeverDuringTurn(t *testing.T) {
	f := startCTOCompactionFixture(t)
	ctx := context.Background()
	f.writeMessages(t, []map[string]any{ctoAssistant("msg_old", 1000), ctoUser("start")})
	f.send(t, "work")
	f.writeMessages(t, []map[string]any{ctoAssistant("msg_big", 900_000), ctoUser("work")})
	f.setActive(t, true)
	if err := f.n.d.ctoCompactOnce(ctx); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(time.Hour)
	if err := f.n.d.ctoCompactOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.compactCount(t); got != 0 {
		t.Fatalf("compacted during a turn: %d", got)
	}
	f.setActive(t, false)
	if err := f.n.d.ctoCompactOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.compactCount(t); got != 1 {
		t.Fatalf("did not compact after the turn: %d", got)
	}
}

func TestCTOCompactionChatLineWhenTheNextSendComesFirst(t *testing.T) {
	f := startCTOCompactionFixture(t)
	ctx := context.Background()
	f.writeMessages(t, []map[string]any{ctoAssistant("msg_old", 1000), ctoUser("start")})
	f.send(t, "work")
	f.writeMessages(t, []map[string]any{ctoAssistant("msg_base", 45_000), ctoUser("work")})
	if err := f.n.d.ctoCompactOnce(ctx); err != nil {
		t.Fatal(err)
	}
	f.send(t, "more")
	f.writeMessages(t, []map[string]any{ctoAssistant("msg_grown", 115_000), ctoUser("more"), ctoAssistant("msg_base", 45_000), ctoUser("work")})
	if err := f.n.d.ctoCompactOnce(ctx); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(5 * time.Minute)
	if err := f.n.d.ctoCompactOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.compactCount(t); got != 1 {
		t.Fatalf("idle compaction = %d, want 1", got)
	}
	// The owner sends again before the daemon has read the context after the
	// compaction. That reply still reports it, and the line must not be lost
	// when the turn ends.
	f.send(t, "next")
	f.writeMessages(t, []map[string]any{ctoAssistant("msg_after", 14_000), ctoCompactionDone(), ctoAssistant("msg_grown", 115_000), ctoUser("more")})
	if err := f.n.d.ctoCompactOnce(ctx); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(f.actions(t), "|")
	if !strings.Contains(joined, "compacted: 115k -> 14k tokens") {
		t.Fatalf("chat line lost after the next send: %s", joined)
	}
}

func TestCTOCompactionChatLineWaitsForTokens(t *testing.T) {
	f := startCTOCompactionFixture(t)
	ctx := context.Background()
	f.writeMessages(t, []map[string]any{ctoAssistant("msg_old", 1000), ctoUser("start")})
	f.send(t, "work")
	f.writeMessages(t, []map[string]any{ctoAssistant("msg_big", 800_000), ctoUser("work")})
	if err := f.n.d.ctoCompactOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.compactCount(t); got != 1 {
		t.Fatalf("threshold compaction = %d, want 1", got)
	}
	// The next reply is still streaming: no token count yet, so no line and no
	// half-written figure.
	f.writeMessages(t, []map[string]any{ctoAssistant("msg_partial", 0), ctoCompactionDone(), ctoAssistant("msg_big", 800_000), ctoUser("work")})
	if err := f.n.d.ctoCompactOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if joined := strings.Join(f.actions(t), "|"); strings.Contains(joined, "compacted:") {
		t.Fatalf("line written before the tokens were known: %s", joined)
	}
	f.writeMessages(t, []map[string]any{ctoAssistant("msg_after", 14_000), ctoCompactionDone(), ctoAssistant("msg_big", 800_000), ctoUser("work")})
	if err := f.n.d.ctoCompactOnce(ctx); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(f.actions(t), "|")
	if !strings.Contains(joined, "compacted: 800k -> 14k tokens") {
		t.Fatalf("chat line missing: %s", joined)
	}
}

func TestCTOCompactionSettings(t *testing.T) {
	f := startCTOCompactionFixture(t)
	ctx := context.Background()
	settings := protocol.CTOCompactionSettings{
		ThresholdPercent: 90,
		CacheWindowSeconds: map[string]int{
			"fake": 60,
		},
	}
	f.cli.mustCall("cto_settings", protocol.CTOSettingsRequest{Compaction: &settings})
	var saved protocol.CTOSettings
	if err := json.Unmarshal(f.cli.mustCall("cto_settings", nil), &saved); err != nil || saved.Compaction.ThresholdPercent != 90 {
		t.Fatalf("threshold setting did not round-trip: %+v %v", saved.Compaction, err)
	}
	f.writeMessages(t, []map[string]any{ctoAssistant("msg_old", 1000), ctoUser("start")})
	f.send(t, "work")
	f.writeMessages(t, []map[string]any{ctoAssistant("msg_base", 45_000), ctoUser("work")})
	if err := f.n.d.ctoCompactOnce(ctx); err != nil {
		t.Fatal(err)
	}
	// The next turn grows by 25k, above the configured 20k growth.
	f.send(t, "more")
	f.writeMessages(t, []map[string]any{ctoAssistant("msg_grown", 70_000), ctoUser("more"), ctoAssistant("msg_base", 45_000), ctoUser("work")})
	if err := f.n.d.ctoCompactOnce(ctx); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(29 * time.Second)
	if err := f.n.d.ctoCompactOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.compactCount(t); got != 0 {
		t.Fatalf("compacted before the configured window: %d", got)
	}
	f.clock.Advance(2 * time.Second)
	if err := f.n.d.ctoCompactOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.compactCount(t); got != 1 {
		t.Fatalf("configured window did not compact: %d", got)
	}
}

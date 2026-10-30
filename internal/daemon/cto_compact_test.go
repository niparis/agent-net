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
	t.Helper()
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{detect: fakeInventory, interval: time.Hour, cfg: func(c *Config) { c.HarnessPaths = map[string]string{"opencode": os.Args[0]} }})
	// Tests drive compaction with a fake clock, one call at a time.
	n.d.ctoCompactEvery = 0
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

func (f *ctoFixture) compactCount(t *testing.T) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.n.cfg.StateDir, "cto", "fake-compact.json"))
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	var calls []json.RawMessage
	if err := json.Unmarshal(data, &calls); err != nil {
		t.Fatal(err)
	}
	return len(calls)
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

func TestCTOCompactionIdleBeforeCacheExpiry(t *testing.T) {
	f := startCTOCompactionFixture(t)
	ctx := context.Background()
	// 50k is below the 70% threshold but above the 40k floor.
	f.writeMessages(t, []map[string]any{ctoAssistant("msg_old", 1000), ctoUser("start")})
	f.send(t, "work")
	f.writeMessages(t, []map[string]any{ctoAssistant("msg_reply", 50_000), ctoUser("work")})
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

func TestCTOCompactionFloor(t *testing.T) {
	f := startCTOCompactionFixture(t)
	ctx := context.Background()
	f.writeMessages(t, []map[string]any{ctoAssistant("msg_old", 1000), ctoUser("start")})
	f.send(t, "work")
	f.writeMessages(t, []map[string]any{ctoAssistant("msg_reply", 39_000), ctoUser("work")})
	if err := f.n.d.ctoCompactOnce(ctx); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(time.Hour)
	if err := f.n.d.ctoCompactOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.compactCount(t); got != 0 {
		t.Fatalf("compacted below the floor: %d", got)
	}
}

func TestCTOCompactionOncePerIdlePeriod(t *testing.T) {
	f := startCTOCompactionFixture(t)
	ctx := context.Background()
	f.writeMessages(t, []map[string]any{ctoAssistant("msg_old", 1000), ctoUser("start")})
	f.send(t, "work")
	f.writeMessages(t, []map[string]any{ctoAssistant("msg_reply", 50_000), ctoUser("work")})
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
	// A new turn starts a new idle period.
	f.send(t, "again")
	f.writeMessages(t, []map[string]any{ctoAssistant("msg_reply2", 50_000), ctoUser("again")})
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

func TestCTOCompactionSettings(t *testing.T) {
	f := startCTOCompactionFixture(t)
	ctx := context.Background()
	settings := protocol.CTOCompactionSettings{
		ThresholdPercent: 90,
		FloorTokens:      60_000,
		CacheWindowSeconds: map[string]int{
			"fake": 60,
		},
	}
	f.cli.mustCall("cto_settings", protocol.CTOSettingsRequest{Compaction: &settings})
	f.writeMessages(t, []map[string]any{ctoAssistant("msg_old", 1000), ctoUser("start")})
	f.send(t, "work")
	f.writeMessages(t, []map[string]any{ctoAssistant("msg_reply", 70_000), ctoUser("work")})
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

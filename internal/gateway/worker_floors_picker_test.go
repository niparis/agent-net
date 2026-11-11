package gateway

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func setupFloorGateway(t *testing.T) (*Gateway, string, string, string, *clock) {
	t.Helper()
	g, node := scopeGateway(t)
	st := g.store
	c := &clock{t: time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)}
	st.now = c.now
	if err := st.SetNodeHello(node, "nas", "1"); err != nil {
		t.Fatal(err)
	}
	launch := true
	if err := st.SetNodeLaunch(node, &launch); err != nil {
		t.Fatal(err)
	}
	harnesses := []protocol.HarnessInfo{
		{Name: "claude", Models: []string{"claude-opus-5-5"}},
		{Name: "codex", Models: []string{"gpt-6.1-terra"}},
		{Name: "opencode", Models: []string{"deepseek/deepseek-v4.1-flash", "opencode-go/deepseek-v4.1-flash"}},
	}
	if _, err := st.SetNodeHarnesses(node, harnesses); err != nil {
		t.Fatal(err)
	}
	owner, err := st.UpsertSession(node, protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := st.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "agent1"})
	if err != nil {
		t.Fatal(err)
	}
	nc := &nodeConn{id: node}
	g.conns[node] = nc
	t.Cleanup(func() { delete(g.conns, node) })

	// Register profiles
	profiles := []protocol.WorkerProfile{
		{Name: "opus", Harness: "claude", Model: "claude-opus-5-5", Effort: "high"},
		{Name: "terra", Harness: "codex", Model: "gpt-6.1-terra", Effort: "high"},
		{Name: "deepseek", Harness: "opencode", Model: "deepseek/deepseek-v4.1-flash", Effort: "high"},
		{Name: "deepseek opencode", Harness: "opencode", Model: "opencode-go/deepseek-v4.1-flash", Effort: "high"},
	}
	if err := st.saveWorkerProfiles(profiles); err != nil {
		t.Fatal(err)
	}
	scoresJSON := `[{"match":"claude-opus-5-5","score":55},{"match":"gpt-6.1-terra","score":52},{"match":"deepseek/deepseek-v4.1-flash","score":45},{"match":"opencode-go/deepseek-v4.1-flash","score":45}]`
	if _, err := st.db.Exec(`INSERT INTO owner_settings (key, value, updated_at) VALUES (?, ?, 0) ON CONFLICT (key) DO UPDATE SET value=excluded.value`, scoresKey, scoresJSON); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`INSERT INTO owner_settings (key, value, updated_at) VALUES (?, ?, 0) ON CONFLICT (key) DO UPDATE SET value=excluded.value`, workerProvidersKey, `{"providers":["anthropic","openai","deepseek","opencode-go"]}`); err != nil {
		t.Fatal(err)
	}

	return g, node, owner, agent, c
}

func TestWorkerFloorPickerUnderFloorExcluded(t *testing.T) {
	g, node, owner, _, c := setupFloorGateway(t)
	nc := g.conns[node]

	// Anthropic has floor 40%. Quota is 30% left in 5h window.
	// Codex has 80% left in week window.
	if _, err := g.store.db.Exec(`INSERT INTO owner_settings (key, value, updated_at) VALUES (?, ?, 0) ON CONFLICT (key) DO UPDATE SET value=excluded.value`, workerFloorsKey, `{"floors":{"anthropic":40}}`); err != nil {
		t.Fatal(err)
	}

	quotas := &protocol.Quotas{
		GeneratedAt: c.now().Unix(),
		Providers: []protocol.QuotaProvider{
			{
				Provider: "claude",
				Windows:  []protocol.QuotaWindow{{ID: "5h", PercentUsed: 70}}, // 30% left < 40%
			},
			{
				Provider: "codex",
				Windows:  []protocol.QuotaWindow{{ID: "week", PercentUsed: 20}}, // 80% left
			},
		},
	}
	g.usage.set(node, c.now(), protocol.UsageReport{Quotas: quotas})

	// Claude candidates should be excluded under floor.
	_, err := g.workerStart(context.Background(), nc, protocol.WorkerStartFrame{
		Owner:              owner,
		Level:              "hard",
		WorkerStartRequest: protocol.WorkerStartRequest{Project: "alpha", Task: "test", Harness: "claude", Model: "auto"},
	}, true)
	if err == nil {
		t.Fatalf("expected error when claude is under floor, got nil")
	}
	wantNote := "skipped opus: anthropic has 30% left in its 5h window, under your 40% worker floor"
	if !strings.Contains(err.Error(), wantNote) {
		t.Fatalf("error %q does not contain %q", err.Error(), wantNote)
	}

	// Codex is above floor, so start validates successfully.
	_, err = g.workerStart(context.Background(), nc, protocol.WorkerStartFrame{
		Owner:              owner,
		Level:              "hard",
		WorkerStartRequest: protocol.WorkerStartRequest{Project: "alpha", Task: "test", Harness: "codex", Model: "auto"},
	}, true)
	if err != nil {
		t.Fatalf("workerStart codex: %v", err)
	}
}

func TestWorkerFloorPickerAboveFloorKept(t *testing.T) {
	g, node, owner, _, c := setupFloorGateway(t)
	nc := g.conns[node]

	// Anthropic has floor 40%. Quota is 60% left in 5h window.
	if _, err := g.store.db.Exec(`INSERT INTO owner_settings (key, value, updated_at) VALUES (?, ?, 0) ON CONFLICT (key) DO UPDATE SET value=excluded.value`, workerFloorsKey, `{"floors":{"anthropic":40}}`); err != nil {
		t.Fatal(err)
	}

	quotas := &protocol.Quotas{
		GeneratedAt: c.now().Unix(),
		Providers: []protocol.QuotaProvider{
			{
				Provider: "claude",
				Windows:  []protocol.QuotaWindow{{ID: "5h", PercentUsed: 40}}, // 60% left >= 40%
			},
		},
	}
	g.usage.set(node, c.now(), protocol.UsageReport{Quotas: quotas})

	_, err := g.workerStart(context.Background(), nc, protocol.WorkerStartFrame{
		Owner:              owner,
		Level:              "hard",
		WorkerStartRequest: protocol.WorkerStartRequest{Project: "alpha", Task: "test", Harness: "claude", Model: "auto"},
	}, true)
	if err != nil {
		t.Fatalf("workerStart auto: %v", err)
	}
}

func TestWorkerFloorPickerNoReadingKeptWithNote(t *testing.T) {
	g, node, owner, _, c := setupFloorGateway(t)
	nc := g.conns[node]

	// Deepseek has floor 30%. Deepseek has no quota reading.
	if _, err := g.store.db.Exec(`INSERT INTO owner_settings (key, value, updated_at) VALUES (?, ?, 0) ON CONFLICT (key) DO UPDATE SET value=excluded.value`, workerFloorsKey, `{"floors":{"deepseek":30}}`); err != nil {
		t.Fatal(err)
	}

	quotas := &protocol.Quotas{
		GeneratedAt: c.now().Unix(),
		Providers:   []protocol.QuotaProvider{},
	}
	g.usage.set(node, c.now(), protocol.UsageReport{Quotas: quotas})

	_, err := g.workerStart(context.Background(), nc, protocol.WorkerStartFrame{
		Owner:              owner,
		Level:              "hard",
		WorkerStartRequest: protocol.WorkerStartRequest{Project: "alpha", Task: "test", Harness: "opencode", Model: "auto"},
	}, true)
	if err != nil {
		t.Fatalf("workerStart auto: %v", err)
	}
}

func TestWorkerFloorExplicitModelRefused(t *testing.T) {
	g, node, owner, agent, c := setupFloorGateway(t)
	nc := g.conns[node]

	// Anthropic floor 40%, 30% left
	if _, err := g.store.db.Exec(`INSERT INTO owner_settings (key, value, updated_at) VALUES (?, ?, 0) ON CONFLICT (key) DO UPDATE SET value=excluded.value`, workerFloorsKey, `{"floors":{"anthropic":40}}`); err != nil {
		t.Fatal(err)
	}

	quotas := &protocol.Quotas{
		GeneratedAt: c.now().Unix(),
		Providers: []protocol.QuotaProvider{
			{
				Provider: "claude",
				Windows:  []protocol.QuotaWindow{{ID: "5h", PercentUsed: 70}}, // 30% left < 40%
			},
		},
	}
	g.usage.set(node, c.now(), protocol.UsageReport{Quotas: quotas})

	// Agent calls explicit start with claude model
	_, err := g.workerStart(context.Background(), nc, protocol.WorkerStartFrame{
		Owner:              agent,
		Level:              "normal",
		WorkerStartRequest: protocol.WorkerStartRequest{Project: "alpha", Task: "test", Harness: "claude", Model: "claude-opus-5-5"},
	}, true)
	if err == nil {
		t.Fatal("expected refusal for explicit model under floor, got nil")
	}
	wantSentence := "anthropic has 30% left in its 5h window, under your 40% worker floor"
	if !strings.Contains(err.Error(), wantSentence) {
		t.Fatalf("err = %q, want containing %q", err.Error(), wantSentence)
	}

	// CTO starts follow the same rule (refused)
	_, err = g.workerStart(context.Background(), nc, protocol.WorkerStartFrame{
		Owner:              owner,
		FromCTO:            true,
		Level:              "normal",
		WorkerStartRequest: protocol.WorkerStartRequest{Project: "alpha", Task: "test", Harness: "claude", Model: "claude-opus-5-5"},
	}, true)
	if err == nil || !strings.Contains(err.Error(), wantSentence) {
		t.Fatalf("CTO explicit start should be refused: %v", err)
	}
}

func TestWorkerFloorOwnerOverride(t *testing.T) {
	g, node, owner, _, c := setupFloorGateway(t)
	nc := g.conns[node]

	// Anthropic floor 40%, 30% left
	if _, err := g.store.db.Exec(`INSERT INTO owner_settings (key, value, updated_at) VALUES (?, ?, 0) ON CONFLICT (key) DO UPDATE SET value=excluded.value`, workerFloorsKey, `{"floors":{"anthropic":40}}`); err != nil {
		t.Fatal(err)
	}

	quotas := &protocol.Quotas{
		GeneratedAt: c.now().Unix(),
		Providers: []protocol.QuotaProvider{
			{
				Provider: "claude",
				Windows:  []protocol.QuotaWindow{{ID: "5h", PercentUsed: 70}}, // 30% left < 40%
			},
		},
	}
	g.usage.set(node, c.now(), protocol.UsageReport{Quotas: quotas})

	// Owner session (FromCTO=false) starts worker explicitly on claude
	_, err := g.workerStart(context.Background(), nc, protocol.WorkerStartFrame{
		Owner:              owner,
		FromCTO:            false,
		Level:              "normal",
		WorkerStartRequest: protocol.WorkerStartRequest{Project: "alpha", Task: "test", Harness: "claude", Model: "claude-opus-5-5"},
	}, true)
	if err != nil {
		t.Fatalf("owner explicit start should be allowed (owner override): %v", err)
	}
}

// quotaAXIWindow builds a window as quota-axi reports it: a share remaining,
// not a share used. The opencode-go monthly window is id included_usage, label
// month, kind monthly on the live board (2026-11-11).
func quotaAXIWindow(id, label, kind string, remaining float64) protocol.QuotaWindow {
	return protocol.QuotaWindow{ID: id, Label: label, Kind: kind, PercentUsed: 100 - remaining}
}

// A worker must not start into a window that another machine can read for the
// same subscription. On 2026-11-11 the Mac read opencode-go's monthly window at
// 1% left while the machine that ran the workers could not read that login, so
// the picker saw no reading and started three workers into the limit.
func TestWorkerFloorPickerReadsMonthlyWindowFromAnotherMachine(t *testing.T) {
	g, node, owner, _, c := setupFloorGateway(t)
	nc := g.conns[node]

	if _, err := g.store.db.Exec(`INSERT INTO owner_settings (key, value, updated_at) VALUES (?, ?, 0) ON CONFLICT (key) DO UPDATE SET value=excluded.value`, workerFloorsKey, `{"floors":{"opencode-go":10,"anthropic":50,"openai":50,"deepseek":50}}`); err != nil {
		t.Fatal(err)
	}
	// The start machine reads every other subscription but not opencode-go.
	g.usage.set(node, c.now(), protocol.UsageReport{Quotas: &protocol.Quotas{
		GeneratedAt: c.now().Unix(),
		Providers: []protocol.QuotaProvider{
			{Provider: "claude", Windows: []protocol.QuotaWindow{quotaAXIWindow("seven_day", "week", "weekly", 30)}},
			{Provider: "codex", Windows: []protocol.QuotaWindow{quotaAXIWindow("five_hour", "session", "session", 0)}},
			{Provider: "deepseek", Windows: []protocol.QuotaWindow{quotaAXIWindow("5h", "5h", "session", 30)}},
		},
	}})
	// Another machine reads the same opencode-go subscription: rolling and
	// weekly are low, the monthly window has 1% left.
	mac, _, err := g.store.CreateNode("mac", "darwin")
	if err != nil {
		t.Fatal(err)
	}
	g.usage.set(mac, c.now(), protocol.UsageReport{Quotas: &protocol.Quotas{
		GeneratedAt: c.now().Unix(),
		Providers: []protocol.QuotaProvider{{Provider: "opencode-go", Windows: []protocol.QuotaWindow{
			quotaAXIWindow("rolling", "rolling", "rolling", 95),
			quotaAXIWindow("weekly", "weekly", "weekly", 90),
			quotaAXIWindow("included_usage", "month", "monthly", 1),
		}}},
	}})

	_, err = g.workerStart(context.Background(), nc, protocol.WorkerStartFrame{
		Owner:              owner,
		Level:              "hard",
		WorkerStartRequest: protocol.WorkerStartRequest{Project: "alpha", Task: "test", Harness: "opencode", Model: "auto"},
	}, true)
	wantNote := "skipped deepseek opencode: opencode-go has 1% left in its monthly window, under your 10% worker floor"
	if err == nil || !strings.Contains(err.Error(), wantNote) {
		t.Fatalf("workerStart = %v, want a refusal containing %q", err, wantNote)
	}
}

func TestWorkerFloorPickerMonthlyWindowUnderFloorExcluded(t *testing.T) {
	g, node, owner, _, c := setupFloorGateway(t)
	nc := g.conns[node]

	// opencode-go has floor 10%. Quota: rolling 5%, weekly 10%, monthly 99% used (1% left).
	// deepseek profile is excluded by excluding deepseek provider so only opencode-go candidate is considered.
	if _, err := g.store.db.Exec(`INSERT INTO owner_settings (key, value, updated_at) VALUES (?, ?, 0) ON CONFLICT (key) DO UPDATE SET value=excluded.value`, workerFloorsKey, `{"floors":{"opencode-go":10,"deepseek":50}}`); err != nil {
		t.Fatal(err)
	}

	quotas := &protocol.Quotas{
		GeneratedAt: c.now().Unix(),
		Providers: []protocol.QuotaProvider{
			{
				Provider: "opencode-go",
				Windows: []protocol.QuotaWindow{
					{ID: "rolling", Label: "rolling", Kind: "rolling", PercentUsed: 5},
					{ID: "weekly", Label: "weekly", Kind: "weekly", PercentUsed: 10},
					{ID: "included_usage", Label: "month", Kind: "monthly", PercentUsed: 99},
				},
			},
			{
				Provider: "deepseek",
				Windows:  []protocol.QuotaWindow{{ID: "5h", PercentUsed: 70}}, // excluded
			},
		},
	}
	g.usage.set(node, c.now(), protocol.UsageReport{Quotas: quotas})

	_, err := g.workerStart(context.Background(), nc, protocol.WorkerStartFrame{
		Owner:              owner,
		Level:              "hard",
		WorkerStartRequest: protocol.WorkerStartRequest{Project: "alpha", Task: "test", Harness: "opencode", Model: "auto"},
	}, true)
	if err == nil {
		t.Fatalf("expected error when opencode-go is under floor, got nil")
	}
	wantNote := "skipped deepseek opencode: opencode-go has 1% left in its monthly window, under your 10% worker floor"
	if !strings.Contains(err.Error(), wantNote) {
		t.Fatalf("error %q does not contain %q", err.Error(), wantNote)
	}
}

func TestWorkerFloorPickerMonthlyWindowAboveFloorKept(t *testing.T) {
	g, node, owner, _, c := setupFloorGateway(t)
	nc := g.conns[node]

	// opencode-go has floor 10%. All windows above floor.
	if _, err := g.store.db.Exec(`INSERT INTO owner_settings (key, value, updated_at) VALUES (?, ?, 0) ON CONFLICT (key) DO UPDATE SET value=excluded.value`, workerFloorsKey, `{"floors":{"opencode-go":10,"deepseek":50}}`); err != nil {
		t.Fatal(err)
	}

	quotas := &protocol.Quotas{
		GeneratedAt: c.now().Unix(),
		Providers: []protocol.QuotaProvider{
			{
				Provider: "opencode-go",
				Windows: []protocol.QuotaWindow{
					{ID: "rolling", Label: "rolling", Kind: "rolling", PercentUsed: 5},
					{ID: "weekly", Label: "weekly", Kind: "weekly", PercentUsed: 10},
					{ID: "included_usage", Label: "month", Kind: "monthly", PercentUsed: 40}, // 60% left >= 10%
				},
			},
			{
				Provider: "deepseek",
				Windows:  []protocol.QuotaWindow{{ID: "5h", PercentUsed: 70}}, // excluded under floor 50%
			},
		},
	}
	g.usage.set(node, c.now(), protocol.UsageReport{Quotas: quotas})

	_, err := g.workerStart(context.Background(), nc, protocol.WorkerStartFrame{
		Owner:              owner,
		Level:              "hard",
		WorkerStartRequest: protocol.WorkerStartRequest{Project: "alpha", Task: "test", Harness: "opencode", Model: "auto"},
	}, true)
	if err != nil {
		t.Fatalf("workerStart opencode auto: %v", err)
	}
}

// A spent window refuses a start even when the owner set no floor for that
// provider: exhaustion is one of the quota decisions of the same start, and it
// must read the same owner-wide quotas as the floor. On 2026-11-11 opencode-go's
// monthly window was at 100% used on the Mac, the machine that ran the workers
// could not read that login, and the picker started three workers into it.
func TestWorkerPickerMonthlyExhaustedOnAnotherMachineRefused(t *testing.T) {
	g, node, owner, _, c := setupFloorGateway(t)
	nc := g.conns[node]

	// No opencode-go floor. The allowlist leaves only the opencode-go profile,
	// so the picker has to decide on that provider.
	if _, err := g.store.db.Exec(`INSERT INTO owner_settings (key, value, updated_at) VALUES (?, ?, 0) ON CONFLICT (key) DO UPDATE SET value=excluded.value`, workerProvidersKey, `{"providers":["opencode-go"]}`); err != nil {
		t.Fatal(err)
	}
	// The start machine reports its other subscriptions but cannot read
	// opencode-go.
	g.usage.set(node, c.now(), protocol.UsageReport{Quotas: &protocol.Quotas{
		GeneratedAt: c.now().Unix(),
		Providers: []protocol.QuotaProvider{
			{Provider: "deepseek", Windows: []protocol.QuotaWindow{quotaAXIWindow("5h", "5h", "session", 30)}},
		},
	}})
	// The Mac reads the same opencode-go subscription, and its month is spent.
	mac, _, err := g.store.CreateNode("mac", "darwin")
	if err != nil {
		t.Fatal(err)
	}
	g.usage.set(mac, c.now(), protocol.UsageReport{Quotas: &protocol.Quotas{
		GeneratedAt: c.now().Unix(),
		Providers: []protocol.QuotaProvider{{Provider: "opencode-go", Windows: []protocol.QuotaWindow{
			quotaAXIWindow("rolling", "rolling", "rolling", 95),
			quotaAXIWindow("weekly", "weekly", "weekly", 90),
			quotaAXIWindow("included_usage", "month", "monthly", 0),
		}}},
	}})

	_, err = g.workerStart(context.Background(), nc, protocol.WorkerStartFrame{
		Owner:              owner,
		Level:              "hard",
		WorkerStartRequest: protocol.WorkerStartRequest{Project: "alpha", Task: "test", Harness: "opencode", Model: "auto"},
	}, true)
	if err == nil || !strings.Contains(err.Error(), "out of quota") {
		t.Fatalf("workerStart = %v, want a refusal because opencode-go is out of quota", err)
	}
}

// A machine that reports nothing at all is not a machine with quota to spare:
// the owner's subscriptions live wherever they are read, so the start falls back
// to the other machines' fresh reports before it applies the floor.
func TestWorkerPickerNoOwnReportUsesAnotherMachineFloor(t *testing.T) {
	g, node, owner, _, c := setupFloorGateway(t)
	nc := g.conns[node]

	// opencode-go floor 10%. The allowlist leaves only that profile.
	if _, err := g.store.db.Exec(`INSERT INTO owner_settings (key, value, updated_at) VALUES (?, ?, 0) ON CONFLICT (key) DO UPDATE SET value=excluded.value`, workerFloorsKey, `{"floors":{"opencode-go":10}}`); err != nil {
		t.Fatal(err)
	}
	if _, err := g.store.db.Exec(`INSERT INTO owner_settings (key, value, updated_at) VALUES (?, ?, 0) ON CONFLICT (key) DO UPDATE SET value=excluded.value`, workerProvidersKey, `{"providers":["opencode-go"]}`); err != nil {
		t.Fatal(err)
	}
	// The start machine sends no usage report here. The Mac reads the same
	// opencode-go subscription: the month has 1% left.
	mac, _, err := g.store.CreateNode("mac", "darwin")
	if err != nil {
		t.Fatal(err)
	}
	g.usage.set(mac, c.now(), protocol.UsageReport{Quotas: &protocol.Quotas{
		GeneratedAt: c.now().Unix(),
		Providers: []protocol.QuotaProvider{{Provider: "opencode-go", Windows: []protocol.QuotaWindow{
			quotaAXIWindow("rolling", "rolling", "rolling", 95),
			quotaAXIWindow("weekly", "weekly", "weekly", 90),
			quotaAXIWindow("included_usage", "month", "monthly", 1),
		}}},
	}})

	_, err = g.workerStart(context.Background(), nc, protocol.WorkerStartFrame{
		Owner:              owner,
		Level:              "hard",
		WorkerStartRequest: protocol.WorkerStartRequest{Project: "alpha", Task: "test", Harness: "opencode", Model: "auto"},
	}, true)
	wantNote := "skipped deepseek opencode: opencode-go has 1% left in its monthly window, under your 10% worker floor"
	if err == nil || !strings.Contains(err.Error(), wantNote) {
		t.Fatalf("workerStart = %v, want a refusal containing %q", err, wantNote)
	}
}

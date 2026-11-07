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
		{Name: "opencode", Models: []string{"deepseek/deepseek-v4.1-flash"}},
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
	}
	if err := st.saveWorkerProfiles(profiles); err != nil {
		t.Fatal(err)
	}
	scoresJSON := `[{"match":"claude-opus-5-5","score":55},{"match":"gpt-6.1-terra","score":52},{"match":"deepseek/deepseek-v4.1-flash","score":45}]`
	if _, err := st.db.Exec(`INSERT INTO owner_settings (key, value, updated_at) VALUES (?, ?, 0) ON CONFLICT (key) DO UPDATE SET value=excluded.value`, scoresKey, scoresJSON); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`INSERT INTO owner_settings (key, value, updated_at) VALUES (?, ?, 0) ON CONFLICT (key) DO UPDATE SET value=excluded.value`, workerProvidersKey, `{"providers":["anthropic","openai","deepseek"]}`); err != nil {
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

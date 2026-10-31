package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/integrations"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/quotaaxi"
)

const fakeQuotaJSON = `{
  "generatedAt": "2026-10-30T12:00:00.000Z",
  "schemaVersion": 5,
  "providers": [
    {
      "provider": "claude",
      "plan": "max",
      "windows": [
        {"id": "five_hour", "label": "session", "kind": "session", "percentRemaining": 80,
         "resetsAt": "2026-10-30T17:00:00.000Z", "pace": {"status": "behind"}},
        {"id": "seven_day", "label": "week", "kind": "weekly", "percentRemaining": 41,
         "resetsAt": "2026-11-02T09:00:00.000Z", "pace": {"status": "ahead"}}
      ],
      "state": {"status": "fresh", "stale": false},
      "quotaSemantics": {"effectiveAvailability": [
        {"scope": "all_models",
         "pace": {"aheadWindowIds": ["seven_day"]},
         "runway": {"status": "projected_exhaustion", "limitingWindowId": "seven_day"}}
      ]}
    },
    {
      "provider": "codex",
      "windows": [
        {"id": "weekly", "label": "week", "kind": "weekly", "percentRemaining": 10,
         "resetsAt": "2026-11-01T09:00:00.000Z", "pace": {"status": "on_pace"}}
      ],
      "state": {"status": "fresh", "stale": false},
      "quotaSemantics": {"effectiveAvailability": []}
    },
    {
      "provider": "cursor",
      "windows": [
        {"id": "included_usage", "label": "month", "kind": "monthly", "percentRemaining": 0,
         "resetsAt": "2026-11-15T00:00:00.000Z"}
      ],
      "state": {"status": "fresh", "stale": false},
      "quotaSemantics": {"effectiveAvailability": [
        {"scope": "all_models", "pace": {}, "runway": {"status": "exhausted_now", "limitingWindowId": "included_usage"}}
      ]}
    },
    {
      "provider": "openrouter",
      "windows": [],
      "state": {"status": "fresh", "stale": false}
    }
  ]
}`

// fakeQuotaAXI writes an executable quota-axi that records its arguments and
// prints body.
func fakeQuotaAXI(t *testing.T, dir, body string, argsFile string) string {
	t.Helper()
	path := filepath.Join(dir, "quota-axi")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + argsFile + "\ncat <<'QUOTA_EOF'\n" + body + "\nQUOTA_EOF\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func fakeQuotaAXIExit(t *testing.T, dir, version, body string, exitCode int, stderr string) string {
	t.Helper()
	path := filepath.Join(dir, "quota-axi")
	script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo 'quota-axi " + version + "'; exit 0; fi\ncat <<'QUOTA_EOF'\n" + body + "\nQUOTA_EOF\nprintf '%s\\n' '" + stderr + "' >&2\nexit " + strconv.Itoa(exitCode) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadQuotaAXIParsesWindowsAndPace(t *testing.T) {
	dir := t.TempDir()
	args := filepath.Join(dir, "args")
	path := fakeQuotaAXI(t, dir, fakeQuotaJSON, args)
	q, err := readQuotaAXI(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if q.GeneratedAt == 0 || len(q.Providers) != 4 {
		t.Fatalf("quota reading: %+v", q)
	}
	claude, ok := q.Provider("claude")
	if !ok || len(claude.Windows) != 2 {
		t.Fatalf("claude: %+v", claude)
	}
	if w := claude.Windows[1]; w.PercentUsed != 59 || w.Kind != "weekly" || w.Pace != protocol.QuotaPaceAhead || w.ResetsAt == 0 {
		t.Errorf("week window: %+v", w)
	}
	if _, ok := q.ExhaustedWindow("claude"); ok {
		t.Error("ahead claude was called exhausted")
	}
	if w, ok := q.ExhaustedWindow("cursor"); !ok || w.ID != "included_usage" {
		t.Errorf("cursor exhaustion: %+v %v", w, ok)
	}
	if got, _ := os.ReadFile(args); strings.TrimSpace(string(got)) != "--json --max-age 5m --no-credential-refresh" {
		t.Errorf("arguments: %q", got)
	}
	if strings.Contains(string(mustRead(t, args)), "--full") {
		t.Error("agent-net passed --full")
	}
}

func TestReadQuotaAXIUsesJSONWhenEveryProviderFailed(t *testing.T) {
	json := strings.Replace(fakeQuotaJSON,
		`"state": {"status": "fresh", "stale": false}`,
		`"state": {"status": "error", "stale": false, "error": "provider unavailable"}`, 1)
	path := fakeQuotaAXIExit(t, t.TempDir(), "0.1.69", json, 1, "provider failure")
	q, err := readQuotaAXI(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if q == nil || len(q.Providers) != 4 {
		t.Fatalf("report from exit 1: %+v", q)
	}
	claude, ok := q.Provider("claude")
	if !ok || claude.Error != "provider unavailable" {
		t.Fatalf("provider failure was lost: %+v", claude)
	}
}

func TestReadQuotaAXIFallsBackToPrivateCopyOnOptionRejection(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	user := fakeQuotaAXIExit(t, t.TempDir(), "0.1.69", fakeQuotaJSON, 2, "unknown option")
	private := quotaaxi.Binary()
	if err := os.MkdirAll(filepath.Dir(private), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(private, []byte("#!/bin/sh\ncat <<'QUOTA_EOF'\n"+fakeQuotaJSON+"\nQUOTA_EOF\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	oldEnsure := ensureQuotaAXIPrivate
	ensureQuotaAXIPrivate = func(context.Context, func(string, ...any)) error { return nil }
	t.Cleanup(func() { ensureQuotaAXIPrivate = oldEnsure })
	var logs []string
	q, err := readQuotaAXIWith(context.Background(), user, func(format string, args ...any) {
		logs = append(logs, fmt.Sprintf(format, args...))
	})
	if err != nil {
		t.Fatal(err)
	}
	if q == nil || len(q.Providers) != 4 {
		t.Fatalf("fallback report: %+v", q)
	}
	if len(logs) != 1 || logs[0] != "quota-axi rejected its options (version 0.1.69); using the pinned copy" {
		t.Fatalf("fallback log: %q", logs)
	}
}

func TestReadQuotaAXIIncludesOnlyRedactedFirstStderrLine(t *testing.T) {
	path := fakeQuotaAXIExit(t, t.TempDir(), "0.1.69", "not json", 3, "  provider failed: token=private-value\nsecond line")
	_, err := readQuotaAXI(context.Background(), path)
	if err == nil {
		t.Fatal("failed quota-axi returned no error")
	}
	if got := err.Error(); !strings.Contains(got, "provider failed: token=[REDACTED]") || strings.Contains(got, "private-value") || strings.Contains(got, "second line") {
		t.Fatalf("unexpected error text: %q", got)
	}
}

func testQuotaDaemon() *Daemon {
	return &Daemon{log: log.New(io.Discard, "", 0)}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRefreshQuotaKeepsTheLastGoodReading(t *testing.T) {
	dir := t.TempDir()
	args := filepath.Join(dir, "args")
	path := fakeQuotaAXI(t, dir, fakeQuotaJSON, args)
	d := testQuotaDaemon()
	if q := d.refreshQuota(context.Background(), path); q == nil {
		t.Fatal("first reading failed")
	}
	// The next run fails: the reading stays.
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if q := d.refreshQuota(context.Background(), path); q != nil {
		t.Fatalf("a failed run returned %+v", q)
	}
	if q := d.quota.snapshot(); q == nil || len(q.Providers) != 4 {
		t.Fatalf("last good reading lost: %+v", q)
	}
	if !strings.Contains(d.quota.errorText(), "failed") {
		t.Errorf("error text %q", d.quota.errorText())
	}
}

func TestReadQuotaAXIStopsAtTheTimeout(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "slow")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := readQuotaAXI(ctx, script); err == nil {
		t.Fatal("a slow quota-axi was not stopped")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("waited %s", d)
	}
}

func TestFindQuotaAXIUsesThePrivateCopy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("PATH", t.TempDir())
	path := quotaaxi.Binary()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n/bin/cat <<'EOF'\n" + fakeQuotaJSON + "\nEOF\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	// quota-axi runs only with a supported Node; this one sits next to it.
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "node"), []byte("#!/bin/sh\necho v22.19.0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, ok := FindQuotaAXI("")
	if !ok || got != path {
		t.Fatalf("private copy: %q %v", got, ok)
	}
	q, err := readQuotaAXI(context.Background(), got)
	if err != nil || len(q.Providers) != 4 {
		t.Fatalf("reading with the private copy: %+v %v", q, err)
	}
}

func TestFindQuotaAXIPrefersTheUserCopy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("PATH", t.TempDir())
	private := quotaaxi.Binary()
	if err := os.MkdirAll(filepath.Dir(private), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(private, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	user := filepath.Join(local, quotaCommand)
	if err := os.WriteFile(user, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, ok := FindQuotaAXI(""); !ok || got != user {
		t.Fatalf("user copy lost: %q %v", got, ok)
	}
}

func TestUsageReportCarriesQuotaNote(t *testing.T) {
	st, err := openState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	d := &Daemon{cfg: Config{}, st: st}
	d.quota.setNote(quotaaxi.NeedNodeMessage)
	rep, ok := d.usageReport()
	if !ok || rep.QuotaNote != quotaaxi.NeedNodeMessage {
		t.Fatalf("report: %+v ok=%v", rep, ok)
	}
}

func TestFindQuotaAXI(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "quota-axi")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, ok := FindQuotaAXI(path); !ok || got != path {
		t.Fatalf("configured path: %q %v", got, ok)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", dir)
	if got, ok := FindQuotaAXI(""); !ok || got != path {
		t.Fatalf("PATH: %q %v", got, ok)
	}
	t.Setenv("PATH", t.TempDir())
	local := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(local, "quota-axi"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, ok := FindQuotaAXI(""); !ok || got != filepath.Join(local, "quota-axi") {
		t.Fatalf("~/.local/bin: %q %v", got, ok)
	}
}

func TestQuotaIntegrationConsent(t *testing.T) {
	dir := t.TempDir()
	if !quotaAllowed(dir) {
		t.Error("no answer should default to on")
	}
	if err := integrations.SaveApplied(dir, protocol.Integrations{QuotaAXI: false}); err != nil {
		t.Fatal(err)
	}
	if quotaAllowed(dir) {
		t.Error("an off answer was ignored")
	}
	if err := integrations.SaveApplied(dir, protocol.Integrations{QuotaAXI: true}); err != nil {
		t.Fatal(err)
	}
	if !quotaAllowed(dir) {
		t.Error("an on answer was ignored")
	}
}

func TestUsageReportCarriesQuotas(t *testing.T) {
	st, err := openState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	d := &Daemon{cfg: Config{}, st: st}
	d.quota.set(&protocol.Quotas{Providers: []protocol.QuotaProvider{{
		Provider: "claude", Windows: []protocol.QuotaWindow{{ID: "seven_day", Kind: "weekly", PercentUsed: 59}},
	}}})
	rep, ok := d.usageReport()
	if !ok || rep.Quotas == nil || len(rep.Quotas.Providers) != 1 {
		t.Fatalf("usage report: %+v ok=%v", rep, ok)
	}
}

func TestFakeQuotaAXIDrivesModelChoiceAndAdmission(t *testing.T) {
	dir := t.TempDir()
	path := fakeQuotaAXI(t, dir, fakeQuotaJSON, filepath.Join(dir, "args"))
	q, err := readQuotaAXI(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	models := []string{"cursor/flash", "opencode-go/flash"}
	got, err := protocol.ResolveModelQuota("flash", models, nil, nil, nil, q)
	if err != nil || got.Model != "opencode-go/flash" {
		t.Fatalf("model choice with the fake reading: %+v %v", got, err)
	}
	reason := protocol.QuotaRefusal(q, "cursor", "cursor/flash")
	if !strings.Contains(reason, "cursor is exhausted") || !strings.Contains(reason, "2026-11-15") {
		t.Fatalf("admission with the fake reading: %q", reason)
	}
}

func TestQuotaLoopReadsAgain(t *testing.T) {
	dir := t.TempDir()
	count := filepath.Join(dir, "count")
	path := fakeQuotaAXI(t, dir, fakeQuotaJSON, count)
	d := testQuotaDaemon()
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); d.quotaLoop(ctx, path, 20*time.Millisecond) }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if b, err := os.ReadFile(count); err == nil && strings.Count(string(b), "--json") >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("quota-axi was not run twice")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	wg.Wait()
	if d.quota.snapshot() == nil {
		t.Fatal("no reading kept")
	}
}

func TestUsageReportCarriesQuotaError(t *testing.T) {
	st, err := openState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	d := &Daemon{cfg: Config{}, st: st}
	d.quota.fail(errors.New("it failed"))
	rep, ok := d.usageReport()
	if !ok || rep.QuotaError != "it failed" {
		t.Fatalf("report: %+v ok=%v", rep, ok)
	}
}

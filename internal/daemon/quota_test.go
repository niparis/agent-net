package daemon

import (
	"context"
	"errors"
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

func TestRunQuotaAXIReceivesOpenCodeAuthEnvironment(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"node":      "#!/bin/sh\necho v22.19.0\n",
		"quota-axi": "#!/bin/sh\nprintf 'HOME=%s\\nXDG_DATA_HOME=%s\\nXDG_CONFIG_HOME=%s\\nOPENCODE_DB=%s\\nPI_CODING_AGENT_DIR=%s\\nQUOTA_AXI_OPENCODE_GO_PI_AUTH=%s\\n' \"$HOME\" \"$XDG_DATA_HOME\" \"$XDG_CONFIG_HOME\" \"$OPENCODE_DB\" \"$PI_CODING_AGENT_DIR\" \"$QUOTA_AXI_OPENCODE_GO_PI_AUTH\"\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for key, value := range map[string]string{
		"HOME":                          filepath.Join(dir, "home"),
		"XDG_DATA_HOME":                 filepath.Join(dir, "custom-data"),
		"XDG_CONFIG_HOME":               filepath.Join(dir, "config"),
		"OPENCODE_DB":                   "opencode-test.db",
		"PI_CODING_AGENT_DIR":           filepath.Join(dir, "pi"),
		"QUOTA_AXI_OPENCODE_GO_PI_AUTH": "true",
	} {
		t.Setenv(key, value)
	}
	raw, _, err := runQuotaAXI(context.Background(), filepath.Join(dir, "quota-axi"))
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"HOME=" + filepath.Join(dir, "home"),
		"XDG_DATA_HOME=" + filepath.Join(dir, "custom-data"),
		"XDG_CONFIG_HOME=" + filepath.Join(dir, "config"),
		"OPENCODE_DB=opencode-test.db",
		"PI_CODING_AGENT_DIR=" + filepath.Join(dir, "pi"),
		"QUOTA_AXI_OPENCODE_GO_PI_AUTH=true",
		"",
	}, "\n")
	if string(raw) != want {
		t.Fatalf("quota-axi environment:\n%s\nwant:\n%s", raw, want)
	}
}

func TestQuotaAXIExplainsRejectedOpenCodeLogin(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "")
	raw := []byte(`{"providers":[{"provider":"opencode-go","state":{"error":"provider_auth_rejected"}}]}`)
	q, err := parseQuotaAXI(raw)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := q.Provider("opencode-go")
	if !ok || p.Error != "the OpenCode Go login the daemon found was rejected" || strings.Contains(p.Error, "provider_auth_rejected") {
		t.Fatalf("provider error was not explained: %+v", p)
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

func TestReadQuotaAXIDoesNotChangeCopyOnOptionRejection(t *testing.T) {
	user := fakeQuotaAXIExit(t, t.TempDir(), "0.1.69", fakeQuotaJSON, 2, "unknown option")
	q, err := readQuotaAXI(context.Background(), user)
	if err == nil || !strings.Contains(err.Error(), "rejected its options") || q != nil {
		t.Fatalf("got %+v, %v", q, err)
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

func writeQuotaCandidate(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "node"), []byte("#!/bin/sh\necho v22.19.0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "quota-axi")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho 0.1.66\n"), 0755); err != nil {
		t.Fatal(err)
	}
	return path
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
	script := "#!/bin/sh\nif [ \"$1\" = --version ]; then echo 0.1.69; exit; fi\n/bin/cat <<'EOF'\n" + fakeQuotaJSON + "\nEOF\n"
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
	writeQuotaCandidate(t, filepath.Dir(quotaaxi.Binary()))
	user := writeQuotaCandidate(t, filepath.Join(home, ".local", "bin"))
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
	path := writeQuotaCandidate(t, dir)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("PATH", dir)
	if got, ok := FindQuotaAXI(path); !ok || got != path {
		t.Fatalf("configured: %q %v", got, ok)
	}
	if got, ok := FindQuotaAXI(""); !ok || got != path {
		t.Fatalf("PATH: %q %v", got, ok)
	}
	t.Setenv("PATH", t.TempDir())
	local := writeQuotaCandidate(t, filepath.Join(home, ".local", "bin"))
	if got, ok := FindQuotaAXI(""); !ok || got != local {
		t.Fatalf("local: %q %v", got, ok)
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

func TestQuotaUsesRecordedChoiceAtStart(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	old := discoverQuotaChoice
	t.Cleanup(func() { discoverQuotaChoice = old })
	got := make(chan string, 1)
	dir := t.TempDir()
	choice := writeQuotaCandidate(t, dir)
	marker := filepath.Join(dir, "ran")
	if err := os.WriteFile(choice, []byte("#!/bin/sh\nprintf yes > '"+marker+"'\necho '{}'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	discoverQuotaChoice = func(dir, configured string) (quotaaxi.Choice, error) {
		got <- configured
		return quotaaxi.Choice{Path: choice, Version: "0.1.66"}, nil
	}
	d := &Daemon{cfg: Config{QuotaAXIPath: "configured"}, log: log.New(io.Discard, "", 0)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.startQuota(ctx)
	if configured := <-got; configured != "configured" {
		t.Fatalf("configured %q", configured)
	}
	eventually(t, "chosen quota executable runs", func() bool { _, err := os.Stat(marker); return err == nil })
	cancel()
	d.wg.Wait()
}

// This report shape is taken from quota-axi 0.1.66's response redaction test
// (source commit a568c710d5321dacc81f799d4e04545906e31dc0).
func TestQuotaParsesMinSupportedReport(t *testing.T) {
	const report = `{"generatedAt":"2026-07-06T18:10:00Z","schemaVersion":5,"providers":[{"provider":"claude","label":"Claude","source":"oauth","windows":[],"state":{"status":"fresh","stale":false,"sourcesTried":["oauth"]}}]}`
	if quotaaxi.MinSupported != "0.1.66" {
		t.Fatal("update the minimum-version fixture")
	}
	q, err := parseQuotaAXI([]byte(report))
	if err != nil || q.GeneratedAt == 0 || len(q.Providers) != 1 || q.Providers[0].Provider != "claude" || q.Providers[0].Stale {
		t.Fatalf("report: %+v %v", q, err)
	}
}

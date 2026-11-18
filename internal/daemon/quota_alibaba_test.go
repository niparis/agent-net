package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

const (
	alibabaQuotaAXIFailed = `{"providers":[{"provider":"alibaba","windows":[],"state":{"status":"failed","error":"bl_usage_malformed_json"}}]}`
	alibabaQuotaAXIRead   = `{"providers":[{"provider":"alibaba","plan":"Coding","windows":[{"id":"weekly","label":"week","kind":"weekly","percentUsed":10}],"state":{"status":"fresh"}}]}`
	alibabaMonthly        = `{"per1MonthPercentage": 0.7621849300106, "per1MonthResetTime": 1797004800000, "hasQuotaData": true}`
	alibabaWeekly         = `{"planName":"Pro","per1WeekPercentage": 0.25, "per1WeekResetTime": 1797004800000, "hasQuotaData": true}`
)

// alibabaFixture writes a fake node, quota-axi and bl into one folder, which
// is first in the quota reader's PATH. It returns the quota-axi path and a
// file that gets one line for each bl run.
func alibabaFixture(t *testing.T, quotaJSON, blStdout, blStderr string, blExit int) (string, string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	calls := filepath.Join(dir, "bl-calls")
	files := map[string]string{
		"node":      "#!/bin/sh\necho v22.19.0\n",
		"quota-axi": "#!/bin/sh\nif [ \"$1\" = --version ]; then echo 0.1.69; exit 0; fi\ncat <<'EOF'\n" + quotaJSON + "\nEOF\n",
		"bl": "#!/bin/sh\necho \"$*\" >> " + calls + "\ncat <<'EOF'\n" + blStdout + "\nEOF\n" +
			"cat >&2 <<'EOF'\n" + blStderr + "\nEOF\nexit " + string(rune('0'+blExit)) + "\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "quota-axi"), calls
}

func alibabaRow(t *testing.T, q *protocol.Quotas) protocol.QuotaProvider {
	t.Helper()
	if q == nil {
		t.Fatal("no reading")
	}
	p, ok := q.Provider("alibaba")
	if !ok {
		t.Fatalf("no alibaba row: %+v", q)
	}
	return p
}

func TestAlibabaMonthlyPlanGetsAMonthWindow(t *testing.T) {
	path, _ := alibabaFixture(t, alibabaQuotaAXIFailed, alibabaMonthly, "", 0)
	p := alibabaRow(t, testQuotaDaemon().refreshQuota(context.Background(), path))
	if p.Error != "" || len(p.Windows) != 1 {
		t.Fatalf("row %+v", p)
	}
	w := p.Windows[0]
	if w.ID != "monthly" || w.Kind != "monthly" || w.Label != "month" || w.ResetsAt != 1797004800 || w.PercentUsed < 76.2 || w.PercentUsed > 76.3 || w.Exhausted {
		t.Errorf("window %+v", w)
	}
}

func TestAlibabaWeeklyPlanMatchesQuotaAXI(t *testing.T) {
	path, _ := alibabaFixture(t, alibabaQuotaAXIFailed, alibabaWeekly, "", 0)
	p := alibabaRow(t, testQuotaDaemon().refreshQuota(context.Background(), path))
	want := protocol.QuotaWindow{ID: "weekly", Label: "week", Kind: "weekly", PercentUsed: 25, ResetsAt: 1797004800}
	if p.Error != "" || p.Plan != "Pro" || len(p.Windows) != 1 || p.Windows[0] != want {
		t.Fatalf("row %+v, want window %+v", p, want)
	}
}

func TestAlibabaKeepsTheSignInErrorText(t *testing.T) {
	const doc = `{"error":{"code":3,"message":"No console access token found."}}`
	for name, stream := range map[string][2]string{"stdout": {doc, ""}, "stderr": {"", doc}} {
		t.Run(name, func(t *testing.T) {
			path, _ := alibabaFixture(t, alibabaQuotaAXIFailed, stream[0], stream[1], 3)
			q := &protocol.Quotas{Providers: []protocol.QuotaProvider{{Provider: "alibaba", Error: "bl_usage_failed", Windows: []protocol.QuotaWindow{}}}}
			testQuotaDaemon().fillAlibabaFromBL(context.Background(), path, q)
			p := alibabaRow(t, q)
			if len(p.Windows) != 0 || !protocol.SignInNeeded("alibaba", p.Error) {
				t.Fatalf("row %+v", p)
			}
		})
	}
}

func TestAlibabaIgnoresATrailingDocument(t *testing.T) {
	path, _ := alibabaFixture(t, alibabaQuotaAXIFailed, alibabaMonthly+"\n"+`{"warning":"update available"}`, "", 0)
	p := alibabaRow(t, testQuotaDaemon().refreshQuota(context.Background(), path))
	if p.Error != "" || len(p.Windows) != 1 || p.Windows[0].ID != "monthly" {
		t.Fatalf("row %+v", p)
	}
}

func TestAlibabaBLDoesNotRunWhenQuotaAXIReadIt(t *testing.T) {
	path, calls := alibabaFixture(t, alibabaQuotaAXIRead, alibabaMonthly, "", 0)
	p := alibabaRow(t, testQuotaDaemon().refreshQuota(context.Background(), path))
	if len(p.Windows) != 1 || p.Windows[0].ID != "weekly" || p.Windows[0].PercentUsed != 10 {
		t.Fatalf("row %+v", p)
	}
	if _, err := os.Stat(calls); err == nil {
		t.Errorf("bl ran: %s", mustRead(t, calls))
	}
}

func TestAlibabaBLStopsAtItsTimeout(t *testing.T) {
	old := alibabaBLTimeout
	alibabaBLTimeout = 200 * time.Millisecond
	t.Cleanup(func() { alibabaBLTimeout = old })
	path, _ := alibabaFixture(t, alibabaQuotaAXIFailed, alibabaMonthly, "", 0)
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "bl"), []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := alibabaRow(t, testQuotaDaemon().refreshQuota(context.Background(), path))
	if len(p.Windows) != 0 || !strings.Contains(p.Error, "no answer within") {
		t.Fatalf("row %+v", p)
	}
}

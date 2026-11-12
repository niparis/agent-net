package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

func TestSettingsIdleCompactionCTOAppliesImmediately(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	raw := json.RawMessage(`{"area":"values_write","changes":[{"key":"sessions.compaction_threshold_percent","level":"network","scope":"network","value":65},{"key":"sessions.cache_window_seconds","level":"network","scope":"network","value":{"default":600}},{"key":"sessions.idle_compaction","level":"network","scope":"network","value":false},{"key":"sessions.idle_compaction_min_tokens","level":"network","scope":"network","value":100000}],"reason":"compaction policy"}`)
	if _, err := n.d.runCTOToolRaw(context.Background(), "settings_change", raw); err != nil {
		t.Fatal(err)
	}
	proposals, err := n.d.proposalRunner().list()
	if err != nil || len(proposals) != 0 {
		t.Fatalf("unexpected approvals: %v %v", proposals, err)
	}
	s, err := g.gw.Store().SettingsSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if s.Int("sessions.compaction_threshold_percent", settings.Where{}) != 65 || s.Map("sessions.cache_window_seconds", settings.Where{})["default"] != 600 || s.Bool("sessions.idle_compaction", settings.Where{}) || s.Int("sessions.idle_compaction_min_tokens", settings.Where{}) != 100000 {
		t.Fatalf("CTO settings not applied: %+v", s)
	}
	rows, err := n.d.readSettings(context.Background(), protocol.SettingsReadRequest{}, true)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, row := range rows {
		if row.Key == "sessions.compaction_threshold_percent" || row.Key == "sessions.cache_window_seconds" || row.Key == "sessions.idle_compaction" || row.Key == "sessions.idle_compaction_min_tokens" {
			found++
		}
	}
	if found != 4 {
		t.Fatalf("Settings tab read exposes %d controls", found)
	}
}

// An attach client blocks idle compaction only when the owner can see it:
// outside herdr, or in a herdr pane that herdr reports as focused. One scan
// asks herdr once for the pane list and once per pane that holds a client.
func TestIdleCompactAttachClientsViewing(t *testing.T) {
	f := newFakeHerdr(t)
	f.panes(t, `{"pane_id":"w1:pA","tab_id":"w1:tA","terminal_id":"term_a","agent":"claude","focused":false},`+
		`{"pane_id":"w1:pB","tab_id":"w1:tB","terminal_id":"term_b","agent":"claude","focused":true},`+
		`{"pane_id":"w1:pC","tab_id":"w1:tC","terminal_id":"term_c","agent":"claude","focused":false}`)
	unfocused, focused, second := f.session(t, "w1:pA"), f.session(t, "w1:pB"), f.session(t, "w1:pA")
	f.holds(t, "w1:pA", unfocused)
	f.holds(t, "w1:pB", focused)
	d := &Daemon{}
	got := d.AttachClientsViewing([]int{unfocused, focused, os.Getpid()})
	if got[unfocused] {
		t.Error("a client in an unfocused herdr pane counts as viewing")
	}
	if !got[focused] {
		t.Error("a client in a focused herdr pane does not count as viewing")
	}
	if !got[os.Getpid()] {
		t.Error("a client outside herdr does not count as viewing")
	}
	calls := f.calls(t)
	if n := strings.Count(calls, "pane list"); n != 1 {
		t.Errorf("%d pane lists for one scan, want 1:\n%s", n, calls)
	}
	if n := strings.Count(calls, "pane process-info"); n != 2 || strings.Contains(calls, "--pane w1:pC") {
		t.Errorf("%d process-info calls, want one for each of the two panes that hold a client:\n%s", n, calls)
	}
	// Two clients of one pane still cost one process-info for it.
	_ = os.Remove(f.log)
	both := fmt.Sprintf(`{"result":{"process_info":{"shell_pid":1,"foreground_processes":[{"pid":%d},{"pid":%d}]}}}`, unfocused, second)
	if err := os.WriteFile(filepath.Join(f.dir, "info-w1:pA"), []byte(both), 0o644); err != nil {
		t.Fatal(err)
	}
	d.AttachClientsViewing([]int{unfocused, second})
	if n := strings.Count(f.calls(t), "pane process-info"); n != 1 {
		t.Errorf("%d process-info calls for two clients of one pane, want 1", n)
	}
	// When herdr cannot say, the client is treated as viewed.
	if err := os.WriteFile(filepath.Join(f.dir, "fail"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if !d.AttachClientsViewing([]int{unfocused})[unfocused] {
		t.Error("a client counts as not viewing when herdr does not answer")
	}
}

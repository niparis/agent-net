package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

func TestSettingsIdleCompactionCTOAppliesImmediately(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	raw := json.RawMessage(`{"area":"values_write","changes":[{"key":"sessions.idle_compaction","level":"network","scope":"network","value":false},{"key":"sessions.idle_compaction_after","level":"network","scope":"network","value":"45m"},{"key":"sessions.idle_compaction_min_tokens","level":"network","scope":"network","value":100000}],"reason":"idle cache policy"}`)
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
	if s.Bool("sessions.idle_compaction", settings.Where{}) || s.Duration("sessions.idle_compaction_after", settings.Where{}) != 45*time.Minute || s.Int("sessions.idle_compaction_min_tokens", settings.Where{}) != 100000 {
		t.Fatalf("CTO settings not applied: %+v", s)
	}
	rows, err := n.d.readSettings(context.Background(), protocol.SettingsReadRequest{}, true)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, row := range rows {
		if row.Key == "sessions.idle_compaction" || row.Key == "sessions.idle_compaction_after" || row.Key == "sessions.idle_compaction_min_tokens" {
			found++
		}
	}
	if found != 3 {
		t.Fatalf("Settings tab read exposes %d controls", found)
	}
}

// An attach client blocks idle compaction only when the owner can see it:
// outside herdr, or in a herdr pane that herdr reports as focused.
func TestIdleCompactAttachClientViewing(t *testing.T) {
	f := newFakeHerdr(t)
	f.panes(t, `{"pane_id":"w1:pA","tab_id":"w1:tA","terminal_id":"term_a","agent":"claude","focused":false},`+
		`{"pane_id":"w1:pB","tab_id":"w1:tB","terminal_id":"term_b","agent":"claude","focused":true}`)
	unfocused, focused := f.session(t, "w1:pA"), f.session(t, "w1:pB")
	f.holds(t, "w1:pA", unfocused)
	f.holds(t, "w1:pB", focused)
	d := &Daemon{}
	if d.AttachClientViewing(unfocused) {
		t.Error("a client in an unfocused herdr pane counts as viewing")
	}
	if !d.AttachClientViewing(focused) {
		t.Error("a client in a focused herdr pane does not count as viewing")
	}
	// A process outside herdr (this test process) is viewed.
	if !d.AttachClientViewing(os.Getpid()) {
		t.Error("a client outside herdr does not count as viewing")
	}
	// When herdr cannot say, the client is treated as viewed.
	if err := os.WriteFile(filepath.Join(f.dir, "fail"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if !d.AttachClientViewing(unfocused) {
		t.Error("a client counts as not viewing when herdr does not answer")
	}
}

package daemon

import (
	"context"
	"encoding/json"
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

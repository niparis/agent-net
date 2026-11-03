package settingsview

import (
	"encoding/json"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
	"testing"
)

func TestViewsEffectiveValueAndHistory(t *testing.T) {
	snap := settings.Snapshot{Revision: 7, Values: []settings.Value{{Key: "delivery.merge_authority", Level: "repo", Scope: "repo", Value: json.RawMessage(`"ask"`)}}}
	history := []protocol.SettingsHistory{{Key: "delivery.merge_authority", Level: "repo", Scope: "repo", Actor: "owner"}}
	rows, err := Views(snap, history, protocol.SettingsReadRequest{Key: "delivery.merge_authority", Repo: "repo"}, true)
	if err != nil || len(rows) != 1 || string(rows[0].Value) != `"ask"` || rows[0].Actor != "owner" || len(rows[0].History) != 1 {
		t.Fatalf("views: %+v %v", rows, err)
	}
	if _, err = Views(snap, nil, protocol.SettingsReadRequest{Key: "unknown"}, true); err == nil {
		t.Fatal("unknown setting accepted")
	}
}

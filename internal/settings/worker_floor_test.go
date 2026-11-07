package settings_test

import (
	"slices"
	"testing"

	"github.com/niparis/agent-net/internal/settings"
)

func TestWorkerFloorSettingInLedger(t *testing.T) {
	recipe := settings.Standard()
	found := false
	for _, entry := range recipe.Ledger {
		if entry.Row == 94 {
			if slices.Contains(entry.Keys, "providers.worker_floor") {
				found = true
				break
			}
		}
	}
	if !found {
		t.Fatalf("row 94 ledger entry does not contain providers.worker_floor: %+v", recipe.Ledger)
	}
}

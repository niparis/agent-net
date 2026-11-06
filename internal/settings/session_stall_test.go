package settings_test

import (
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/settings"
)

func TestSessionStallDefaultTimeout(t *testing.T) {
	if got := (settings.Snapshot{}).Duration("sessions.stall_timeout", settings.Where{}); got != 20*time.Minute {
		t.Fatalf("stall timeout = %v, want 20m", got)
	}
}

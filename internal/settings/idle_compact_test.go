package settings

import (
	"testing"
	"time"
)

func TestIdleCompactionSettings(t *testing.T) {
	s := Snapshot{}
	if !s.Bool("sessions.idle_compaction", Where{}) || s.Duration("sessions.idle_compaction_after", Where{}) != 50*time.Minute || s.Int("sessions.idle_compaction_min_tokens", Where{}) != 200000 {
		t.Fatal("idle compaction defaults missing")
	}
	for _, key := range []string{"sessions.idle_compaction", "sessions.idle_compaction_after", "sessions.idle_compaction_min_tokens"} {
		d, ok := DefinitionFor(key)
		if !ok || d.Kind != "advice" || !d.Live {
			t.Fatalf("%s must apply immediately for CTO", key)
		}
	}
}

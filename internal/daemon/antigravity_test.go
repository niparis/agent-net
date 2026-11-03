package daemon

import (
	"encoding/json"
	"testing"
)

func TestAntigravityEvent(t *testing.T) {
	d := &Daemon{}
	if err := d.recordAntigravity(json.RawMessage(`{"conversationId":"one","workspacePaths":["/tmp"],"event":"Stop","fullyIdle":true}`)); err != nil {
		t.Fatal(err)
	}
	e := d.agyEvents["one"]
	if e.Event != "Stop" || !e.FullyIdle || e.Time.IsZero() || len(e.WorkspacePaths) != 1 {
		t.Fatalf("event: %+v", e)
	}
}

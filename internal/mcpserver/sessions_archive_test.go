package mcpserver

import (
	"encoding/json"
	"testing"
)

func TestSessionsArchiveSchema(t *testing.T) {
	for _, raw := range []string{`{"name":"judge-*","reason":"junk"}`, `{"project":"","ended_workers":true}`, `{"ids":["s_test"],"created_before":"2026-01-01","harness":"pi","folder":"/tmp/*"}`} {
		if err := CTOArguments("sessions_archive", json.RawMessage(raw)); err != nil {
			t.Fatal(err)
		}
	}
	if err := CTOArguments("sessions_archive", json.RawMessage(`{"restore":true}`)); err == nil {
		t.Fatal("CTO restore should be owner-only")
	}
}

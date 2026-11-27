package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// ctoRunNow runs one CTO change as the CTO's tool call does, through the same
// concrete handler, as actor "cto". It leaves out the Inbox line, so a test
// needs no registered CTO session.
func ctoRunNow(ctx context.Context, d *Daemon, name string, raw json.RawMessage) (any, error) {
	concrete, args, _, err := ctoConcreteCall(name, raw)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(args, &fields); err != nil {
		return nil, err
	}
	if concrete != "protected_providers_set" && concrete != "settings_values_write" {
		delete(fields, "reason")
	}
	args, _ = json.Marshal(fields)
	return d.executeCTOAction(context.WithValue(ctx, ctoActorKey{}, "cto"), concrete, args)
}

// ctoStartNow runs a CTO session start or project create and returns its
// start record.
func ctoStartNow(t *testing.T, d *Daemon, name string, raw json.RawMessage) protocol.StartedSession {
	t.Helper()
	value, err := ctoRunNow(context.Background(), d, name, raw)
	if err != nil {
		t.Fatal(err)
	}
	var r protocol.StartedSession
	b, _ := json.Marshal(value)
	if err := json.Unmarshal(b, &r); err != nil || r.ID == "" {
		t.Fatalf("start %s: %v", b, err)
	}
	return r
}

func assertCTOImmediate(t *testing.T, v any) {
	t.Helper()
	if s, ok := v.(string); ok && strings.HasPrefix(s, "proposal ") {
		t.Fatalf("unexpected proposal: %s", s)
	}
}

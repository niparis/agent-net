package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// legacyProposalFixture models a call stored before the at-once policy. Those
// open proposals must remain executable after the upgrade.
func legacyProposalFixture(ctx context.Context, d *Daemon, name string, raw json.RawMessage) (any, error) {
	concrete, _, action, err := ctoConcreteCall(name, raw)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	var reason string
	_ = json.Unmarshal(fields["reason"], &reason)
	if reason == "" {
		reason = "legacy proposal fixture"
	}
	if concrete != "protected_providers_set" && concrete != "settings_values_write" {
		delete(fields, "reason")
	}
	raw, _ = json.Marshal(fields)
	id, err := d.Propose(ctx, name, action, raw, reason)
	if err != nil {
		return nil, err
	}
	return ProposalToolResult(id), nil
}

func assertCTOImmediate(t *testing.T, v any) {
	t.Helper()
	if s, ok := v.(string); ok && strings.HasPrefix(s, "proposal ") {
		t.Fatalf("unexpected proposal: %s", s)
	}
}

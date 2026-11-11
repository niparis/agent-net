package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestOperatorDecodeRefusesUnknownArgument is the regression test for the CTO
// call {"project":"agent-net","step":"accept","dry_run":true}. The method has
// no dry_run, so the field was dropped without a word and only the expect
// check stopped a real write. A caller must never believe a preview exists.
func TestOperatorDecodeRefusesUnknownArgument(t *testing.T) {
	r := newDeliveryRig(t)
	_, err := r.g.handle(context.Background(), &nodeConn{id: r.a}, "delivery.owner.set",
		[]byte(`{"type":"delivery.owner.set","id":"1","project":"demo","step":"accept","dry_run":true}`))
	if err == nil {
		t.Fatal("unknown argument dry_run was accepted")
	}
	for _, want := range []string{`unknown argument "dry_run"`, "delivery_owner_set", "known:", "project", "step", "expect"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not name %q", err, want)
		}
	}
}

// TestOperatorDecodeAcceptsKnownArguments keeps a correct call working, and
// accepts the frame fields and daemon attribution that sit next to the method
// arguments.
func TestOperatorDecodeAcceptsKnownArguments(t *testing.T) {
	r := newDeliveryRig(t)
	data, err := r.g.handle(context.Background(), &nodeConn{id: r.a}, "delivery.owner.set",
		[]byte(`{"type":"delivery.owner.set","id":"2","via":"cto","by":"cto","project":"demo","step":"accept","node":"","expect":""}`))
	if err != nil {
		t.Fatalf("known arguments refused: %v", err)
	}
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"ok":true}` {
		t.Fatalf("result = %s", raw)
	}
}

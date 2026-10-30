package main

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/niparis/agent-net/internal/protocol"
	"strings"
	"testing"
)

type ctoFake struct{ req protocol.CTOSettingsRequest }

func (f *ctoFake) Call(_ context.Context, method string, p any) (json.RawMessage, error) {
	f.req = p.(protocol.CTOSettingsRequest)
	return json.RawMessage(`{"harness":"claude","model":"opus","compaction":{"threshold_percent":70}}`), nil
}
func TestCTOCommand(t *testing.T) {
	f := &ctoFake{}
	var out bytes.Buffer
	if err := runCTOCommand(context.Background(), f, &out, nil); err != nil || !strings.Contains(out.String(), "claude") || !strings.Contains(out.String(), "opus") || !strings.Contains(out.String(), "70") {
		t.Fatalf("output %q error %v", out.String(), err)
	}
	for _, args := range [][]string{{"harness", "claude"}, {"model", ""}} {
		if err := runCTOCommand(context.Background(), f, &out, args); err != nil {
			t.Fatal(err)
		}
		if args[0] == "harness" && (f.req.Harness == nil || *f.req.Harness != args[1]) {
			t.Fatal(f.req)
		}
		if args[0] == "model" && (f.req.Model == nil || *f.req.Model != args[1]) {
			t.Fatal(f.req)
		}
	}
	if runCTOCommand(context.Background(), f, &out, []string{"harness", "bad"}) == nil {
		t.Fatal("accepted bad harness")
	}
}

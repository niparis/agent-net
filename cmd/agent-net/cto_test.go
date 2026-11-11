package main

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/niparis/agent-net/internal/protocol"
	"testing"
)

type ctoFake struct{ req protocol.CTOSettingsRequest }

func (f *ctoFake) Call(_ context.Context, method string, p any) (json.RawMessage, error) {
	f.req = p.(protocol.CTOSettingsRequest)
	return json.RawMessage(`{"harness":"claude","model":"opus"}`), nil
}
func TestCTOCommand(t *testing.T) {
	f := &ctoFake{}
	var out bytes.Buffer
	if err := runCTOCommand(context.Background(), f, &out, nil); err != nil || out.String() != "harness: claude\nmodel: opus\n" {
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

package toolcatalog

import (
	"encoding/json"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/niparis/agent-net/internal/protocol"
)

func TestAskSortSchemaMatchesProtocolResults(t *testing.T) {
	tool, ok := toolNamed("agent_ask_sort")
	if !ok {
		t.Fatal("missing agent_ask_sort")
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range []string{protocol.AskSortClose, protocol.AskSortMerge, protocol.AskSortSend, protocol.AskSortOwner, protocol.AskSortList, "hide", protocol.AskSortUndo} {
		t.Run(result, func(t *testing.T) {
			args := protocol.AskSortArgs{Ask: "ask_test", Result: result}
			if result != protocol.AskSortUndo && result != protocol.AskSortList && result != "hide" {
				args.Reason = "test"
				args.Into = "ask_other"
				args.To = "session_test"
				args.Priority = 1
				args.WhyYou = "owner decision"
			}
			if result == "hide" {
				args.Mode = "preview"
			}
			if err := args.Validate(); err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(args)
			if err != nil {
				t.Fatal(err)
			}
			var value any
			if err := json.Unmarshal(data, &value); err != nil {
				t.Fatal(err)
			}
			if err := resolved.Validate(value); err != nil {
				t.Fatal(err)
			}
		})
	}
}

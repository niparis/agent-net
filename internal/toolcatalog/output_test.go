package toolcatalog

import (
	"encoding/json"
	"testing"
)

func TestPruneOutputPreservesOpenValues(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"open":{},"blocked":false,"map":{"type":"object","additionalProperties":{"$ref":"#/$defs/Row"}}},"additionalProperties":false,"$defs":{"Row":{"type":"object","properties":{"n":{"type":"integer"}},"additionalProperties":false}}}`)
	raw := json.RawMessage(`{"open":{"arbitrary":[9007199254740993,null]},"blocked":1,"map":{"row":{"n":9007199254740993,"future":true}},"future":1}`)
	got, err := PruneOutput(schema, raw)
	if err != nil {
		t.Fatal(err)
	}
	// A declared false schema must survive pruning so validation can reject it.
	want := `{"blocked":1,"map":{"row":{"n":9007199254740993}},"open":{"arbitrary":[9007199254740993,null]}}`
	if string(got) != want {
		t.Fatalf("got %s; want %s", got, want)
	}
}

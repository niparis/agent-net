package protocol

import (
	"encoding/json"
	"testing"
)

// A parameter named like a frame field would be silently replaced by it.
func TestEncodeRequestRefusesFrameFieldParams(t *testing.T) {
	for _, k := range []string{"id", "type"} {
		if _, err := EncodeRequest("inbox.answer", "7", map[string]string{k: "x"}); err == nil {
			t.Fatalf("accepted a %q parameter", k)
		}
	}
	if _, err := EncodeRequest("inbox.answer", "7", map[string]string{"item_id": "i_1"}); err != nil {
		t.Fatal(err)
	}
}

func TestSettingsFrameKeepsLargeInteger(t *testing.T) {
	q := SettingsWrite{Changes: []SettingsChange{{Key: "workers.auto_continue_max", Level: "network", Scope: "network", Value: json.RawMessage(`9007199254740993`), Expect: json.RawMessage(`9007199254740995`)}}}
	raw, err := EncodeRequest("settings.write", "one", q)
	if err != nil {
		t.Fatal(err)
	}
	var got SettingsWrite
	if err = json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if string(got.Changes[0].Value) != "9007199254740993" || string(got.Changes[0].Expect) != "9007199254740995" {
		t.Fatalf("precision lost: %s", raw)
	}
}

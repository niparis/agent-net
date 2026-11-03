package protocol

import "testing"

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

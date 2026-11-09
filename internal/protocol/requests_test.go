package protocol

import (
	"encoding/json"
	"testing"
)

func TestEncodeRequestViaAddsTheOriginOnly(t *testing.T) {
	raw, err := EncodeRequestVia("query.agents", "7", ViaTUI, map[string]any{"limit": 5})
	if err != nil {
		t.Fatal(err)
	}
	var h Header
	if err := json.Unmarshal(raw, &h); err != nil || h.Via != "tui" || h.Type != "query.agents" || h.ID != "7" {
		t.Fatalf("header = %+v, err %v", h, err)
	}
	plain, _ := EncodeRequest("query.agents", "7", map[string]any{"limit": 5})
	var m map[string]any
	json.Unmarshal(plain, &m)
	if _, ok := m["via"]; ok {
		t.Fatalf("EncodeRequest must not add an origin: %s", plain)
	}
	if _, err := EncodeRequestVia("x", "1", ViaTUI, map[string]any{"via": "mine"}); err == nil {
		t.Fatal("a parameter named via must clash with the frame field")
	}
}

func TestCleanViaBoundsTheLabels(t *testing.T) {
	for in, want := range map[string]string{"tui": "tui", "worker": "worker", "": "unknown", "evil-" + "label": "unknown"} {
		if got := CleanVia(in); got != want {
			t.Errorf("CleanVia(%q) = %q, want %q", in, got, want)
		}
	}
}

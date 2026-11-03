package protocol

import "testing"

func TestParseModelSelection(t *testing.T) {
	for _, h := range []string{"codex", "claude", "pi", "opencode", "agy"} {
		t.Run(h, func(t *testing.T) {
			old := "vendor/model:high"
			if h == "agy" {
				old = "vendor/model-high"
			}
			got := ParseModelSelection(h, "", old, "")
			want := ParseModelSelection(h, "vendor", "model", "high")
			if got != want {
				t.Fatalf("legacy %#v != separate %#v", got, want)
			}
		})
	}
}

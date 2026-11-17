package protocol

import (
	"strings"
	"testing"
)

func TestCheckCTOHarness(t *testing.T) {
	for _, h := range []string{"claude", "opencode", "codex"} {
		if err := CheckCTOHarness(h); err != nil {
			t.Errorf("%s: %v", h, err)
		}
	}
	if err := CheckCTOHarness("pi"); err == nil || err.Error() != "Pi cannot be woken by a message yet" {
		t.Errorf("pi: %v", err)
	}
	if err := CheckCTOHarness("other"); err == nil || !strings.Contains(err.Error(), `bad CTO harness "other"`) {
		t.Errorf("other: %v", err)
	}
}

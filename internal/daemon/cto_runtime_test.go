package daemon

import "testing"

func TestOpenCodeImplementsCTORuntime(t *testing.T) {
	var runtime ctoHarness = openCodeCTOHarness{}
	if runtime.name() != "opencode" {
		t.Fatalf("harness name = %q", runtime.name())
	}
}

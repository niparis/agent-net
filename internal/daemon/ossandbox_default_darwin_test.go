//go:build darwin

package daemon

import "testing"

// A Mac wraps Pi and OpenCode workers by default too, like Linux: the profile
// passed its real check on a Mac on 2026-11-17 (decision 0171, phase 4b).
// Change this test together with that decision.
func TestOSSandboxIsDefaultOnMacOS(t *testing.T) {
	if !OSSandboxDefaultOn {
		t.Fatal("the OS sandbox is not on by default on macOS")
	}
	ready := func() error { return nil }
	for _, harness := range []string{"pi", "opencode"} {
		if got, err := osSandboxDecide(osSandboxDefault, harness, "", ready); err != nil || got != osSandboxName {
			t.Errorf("default %s on macOS: %q, %v; want %q", harness, got, err, osSandboxName)
		}
	}
	// Claude keeps Claude Code's own sandbox settings, as on Linux.
	if got, err := osSandboxDecide(osSandboxDefault, "claude", "", ready); err != nil || got != "" {
		t.Errorf("default claude on macOS: %q, %v; want no wrapper", got, err)
	}
}

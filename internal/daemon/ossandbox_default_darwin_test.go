//go:build darwin

package daemon

import "testing"

// A Mac does not wrap by default until the profile has been run on one
// (decision 0171, phase 4b). Change this test together with that decision.
func TestOSSandboxIsOptInOnMacOS(t *testing.T) {
	if OSSandboxDefaultOn {
		t.Fatal("the OS sandbox is on by default on macOS")
	}
	if got, err := osSandboxDecide(osSandboxDefault, "pi", "", func() error { return nil }); err != nil || got == osSandboxName {
		t.Fatalf("default pi on macOS: %q, %v", got, err)
	}
}

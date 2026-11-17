//go:build linux

package daemon

import "testing"

func TestOSSandboxIsDefaultOnLinux(t *testing.T) {
	if !OSSandboxDefaultOn {
		t.Fatal("the OS sandbox is not on by default on Linux")
	}
}

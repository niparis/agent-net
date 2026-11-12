//go:build darwin

package daemon

import (
	"os"
	"testing"
)

// The view of a process and procToken must give the same token for the same
// run. This reads the test's own process only; nothing is signalled.
func TestProcViewTokenMatchesProcToken(t *testing.T) {
	pid := os.Getpid()
	v, err := readProcView(pid)
	if err != nil {
		t.Fatal(err)
	}
	tok, alive := procToken(pid)
	if !alive {
		t.Fatal("procToken says the test process is not alive")
	}
	if v.Token != tok {
		t.Fatalf("view token %q != procToken %q", v.Token, tok)
	}
}

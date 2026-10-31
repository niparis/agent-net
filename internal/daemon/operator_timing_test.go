package daemon

import (
	"bytes"
	"log"
	"strings"
	"testing"
	"time"
)

func TestOperatorSlowCallLogged(t *testing.T) {
	var out bytes.Buffer
	d := &Daemon{log: log.New(&out, "", 0)}
	d.logSlowOperatorCall("nodes", time.Now().Add(-1100*time.Millisecond))
	if got := out.String(); !strings.Contains(got, "nodes") || !strings.Contains(got, "1.1") {
		t.Fatalf("slow call log: %q", got)
	}
	out.Reset()
	d.logSlowOperatorCall("info", time.Now())
	if out.Len() != 0 {
		t.Fatalf("fast call logged: %q", out.String())
	}
}

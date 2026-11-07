package daemon

import (
	"bytes"
	"log"
	"strings"
	"testing"
	"time"
)

func TestSlowOperatorLog(t *testing.T) {
	var logs bytes.Buffer
	d := &Daemon{log: log.New(&logs, "", 0)}
	for _, method := range []string{"list", "session_enter"} {
		logs.Reset()
		d.slowCall(method, time.Now().Add(-250*time.Millisecond))
		if strings.Count(logs.String(), "event=slow") != 1 || !strings.Contains(logs.String(), "method="+method+" ms=") {
			t.Fatal(logs.String())
		}
	}
	logs.Reset()
	d.slowCall("list", time.Now())
	if logs.Len() != 0 {
		t.Fatal(logs.String())
	}
}

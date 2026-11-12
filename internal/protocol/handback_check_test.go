package protocol

import (
	"strings"
	"testing"
)

func TestHandbackCheckSignalAndNotice(t *testing.T) {
	failed := HandbackCheck{Status: HandbackCheckFailed, ExitCode: 3, DurationMS: 1250,
		OutputTail: "line one\n\x00the check found a problem\n"}
	signal, ok := failed.Signal()
	if !ok || signal.Code != SignalHandbackCheckFailed || signal.Strength != SignalStrong {
		t.Fatalf("failed signal: %+v %v", signal, ok)
	}
	for _, want := range []string{"exit 3", "1.2s", "the check found a problem"} {
		if !strings.Contains(signal.Evidence, want) {
			t.Errorf("evidence lacks %q: %q", want, signal.Evidence)
		}
	}
	if strings.ContainsRune(signal.Evidence, 0) {
		t.Errorf("evidence keeps a control character: %q", signal.Evidence)
	}
	if line := failed.NoticeLine(); !strings.HasPrefix(line, "hand-back check: failed (exit 3) in ") {
		t.Errorf("failed notice line: %q", line)
	}

	timedOut := HandbackCheck{Status: HandbackCheckTimedOut, DurationMS: 30000}
	signal, ok = timedOut.Signal()
	if !ok || signal.Code != SignalHandbackCheckTimedOut {
		t.Fatalf("timeout signal: %+v %v", signal, ok)
	}
	if line := timedOut.NoticeLine(); line != "hand-back check: timed out after 30.0s" {
		t.Errorf("timeout notice line: %q", line)
	}

	for _, check := range []HandbackCheck{
		{Status: HandbackCheckPassed, DurationMS: 40},
		{Status: HandbackCheckNotConfigured},
	} {
		if _, ok := check.Signal(); ok {
			t.Errorf("%s raised a signal", check.Status)
		}
	}
	if line := (HandbackCheck{Status: HandbackCheckNotConfigured}).NoticeLine(); line != "hand-back check: not configured" {
		t.Errorf("not configured notice line: %q", line)
	}
}

func TestHandbackCheckValidate(t *testing.T) {
	for _, bad := range []HandbackCheck{
		{Status: "ok"},
		{Status: HandbackCheckFailed, ExitCode: 300},
		{Status: HandbackCheckPassed, DurationMS: -1},
		{Status: HandbackCheckPassed, OutputTail: strings.Repeat("x", MaxHandbackCheckOutputBytes+1)},
		{Status: HandbackCheckPassed, OutputTail: "bad\xff"},
	} {
		if err := bad.Validate(); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	good := HandbackCheck{Status: HandbackCheckFailed, ExitCode: 2, DurationMS: 1, OutputTail: "x"}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
}

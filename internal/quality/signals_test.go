package quality

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// addedDiff builds one `git diff -U0` shape with the given files' added lines.
func addedDiff(files map[string][]string) string {
	paths := make([]string, 0, len(files))
	for name := range files {
		paths = append(paths, name)
	}
	// Deterministic output is easier to read when a test fails.
	for i := 0; i < len(paths); i++ {
		for j := i + 1; j < len(paths); j++ {
			if paths[j] < paths[i] {
				paths[i], paths[j] = paths[j], paths[i]
			}
		}
	}
	var b strings.Builder
	for _, name := range paths {
		lines := files[name]
		b.WriteString("diff --git a/" + name + " b/" + name + "\n")
		b.WriteString("--- a/" + name + "\n+++ b/" + name + "\n")
		b.WriteString("@@ -0,0 +1 @@\n")
		for _, line := range lines {
			b.WriteString("+" + line + "\n")
		}
	}
	return b.String()
}

func codes(signals []protocol.QualitySignal) []string {
	out := make([]string, len(signals))
	for i, s := range signals {
		out[i] = s.Code
	}
	return out
}

func TestRepeatedLineSignal(t *testing.T) {
	line := "\tif p.Ack || p.Type == protocol.TypeAck {"
	diff := addedDiff(map[string][]string{
		"internal/gateway/server.go":  {line, line},
		"internal/gateway/workers.go": {line},
	})
	got := Signals(diff, "normal")
	if len(got) != 1 || got[0].Code != protocol.SignalRepeatedLine || got[0].Strength != protocol.SignalStrong {
		t.Fatalf("signals = %+v", got)
	}
	want := "repeated line ×3: " + strings.TrimSpace(line) + " in server.go, workers.go"
	if got[0].Evidence != want {
		t.Fatalf("evidence %q, want %q", got[0].Evidence, want)
	}
}

func TestRepeatedLineIgnoresTestsAndShortBoilerplate(t *testing.T) {
	line := "\tnode, id := nudgeChiefPause(t, g, chief)"
	diff := addedDiff(map[string][]string{
		"internal/daemon/nudge_test.go": {line, line, line, line},
		"internal/gateway/server.go":    {"if err != nil {", "if err != nil {", "if err != nil {"},
	})
	if got := Signals(diff, "normal"); len(got) != 0 {
		t.Fatalf("signals = %+v", got)
	}
}

func TestTestTimingSignal(t *testing.T) {
	diff := addedDiff(map[string][]string{
		"internal/gateway/ack_test.go": {
			"\tcase <-time.After(50 * time.Millisecond):",
			"\ttime.Sleep(10 * time.Millisecond)",
		},
	})
	got := Signals(diff, "normal")
	if len(got) != 1 || got[0].Code != protocol.SignalTestTiming || got[0].Strength != protocol.SignalStrong {
		t.Fatalf("signals = %+v", got)
	}
	if !strings.Contains(got[0].Evidence, "test timing ×2") || !strings.Contains(got[0].Evidence, "ack_test.go") {
		t.Fatalf("evidence %q", got[0].Evidence)
	}
}

// A select timeout that waits seconds for something that should happen is
// ordinary test structure: it proves a frame arrived, not that nothing did.
func TestTestTimingSignalIgnoresLongSelectTimeout(t *testing.T) {
	diff := addedDiff(map[string][]string{
		"internal/gateway/ack_test.go": {
			"\tselect {",
			"\tcase <-time.After(5 * time.Second):",
			"\t\tt.Fatal(\"no ack\")",
			"\t}",
			"\tcase <-time.After(waitFor):",
		},
	})
	if got := Signals(diff, "normal"); len(got) != 0 {
		t.Fatalf("signals = %+v", got)
	}
}

// A literal timeout under a second usually proves nothing happened.
func TestTestTimingSignalFiresOnShortTimer(t *testing.T) {
	diff := addedDiff(map[string][]string{
		"internal/gateway/ack_test.go": {
			"\tcase <-time.After(50 * time.Millisecond):",
		},
	})
	got := Signals(diff, "normal")
	if len(got) != 1 || got[0].Code != protocol.SignalTestTiming || got[0].Strength != protocol.SignalStrong {
		t.Fatalf("signals = %+v", got)
	}
	if !strings.Contains(got[0].Evidence, "test timing ×1") || !strings.Contains(got[0].Evidence, "ack_test.go") {
		t.Fatalf("evidence %q", got[0].Evidence)
	}
}

// Every new sleep counts, whatever its duration: it slows the suite.
func TestTestTimingSignalFiresOnSleep(t *testing.T) {
	diff := addedDiff(map[string][]string{
		"internal/gateway/ack_test.go": {
			"\ttime.Sleep(2 * time.Second)",
			"\ttime.Sleep(waitFor)",
		},
	})
	got := Signals(diff, "normal")
	if len(got) != 1 || got[0].Code != protocol.SignalTestTiming {
		t.Fatalf("signals = %+v", got)
	}
	if !strings.Contains(got[0].Evidence, "test timing ×2") {
		t.Fatalf("evidence %q", got[0].Evidence)
	}
}

func TestOutOfProportionSignalFollowsDifficulty(t *testing.T) {
	var lines []string
	for i := 0; i < linesTrivial+1; i++ {
		lines = append(lines, fmt.Sprintf("\tx%d := computeSomethingMeaningful()", i))
	}
	diff := addedDiff(map[string][]string{"internal/gateway/server.go": lines})
	if got := Signals(diff, "trivial"); len(got) != 1 || got[0].Code != protocol.SignalOutOfProportion || got[0].Strength != protocol.SignalWeak {
		t.Fatalf("trivial signals = %+v", got)
	}
	if got := Signals(diff, "simple"); len(got) != 0 {
		t.Fatalf("simple signals = %+v", got)
	}
	if got := Signals(diff, "very_complex"); len(got) != 0 {
		t.Fatalf("very_complex signals = %+v", got)
	}
}

func TestHeavyTestsSignal(t *testing.T) {
	var testLines, codeLines []string
	for i := 0; i < heavyTestLines+1; i++ {
		testLines = append(testLines, "\tif got != want {")
	}
	for i := 0; i < 10; i++ {
		codeLines = append(codeLines, "\tdoSomething()")
	}
	diff := addedDiff(map[string][]string{
		"internal/gateway/a_test.go": testLines,
		"internal/gateway/a.go":      codeLines,
	})
	got := Signals(diff, "normal")
	if len(got) != 1 || got[0].Code != protocol.SignalHeavyTests || got[0].Strength != protocol.SignalWeak {
		t.Fatalf("signals = %+v", got)
	}
	if !strings.Contains(got[0].Evidence, "151 test lines added for 10 code lines") {
		t.Fatalf("evidence %q", got[0].Evidence)
	}
}

func TestNewTestHelperSignal(t *testing.T) {
	diff := addedDiff(map[string][]string{
		"internal/daemon/nudge_test.go": {
			"func nudgeChiefRig(t *testing.T) (*node, *testGateway, *localClient, string) {",
			"func TestNudgeResume(t *testing.T) {",
		},
	})
	got := Signals(diff, "normal")
	if len(got) != 1 || got[0].Code != protocol.SignalTestHelpers || got[0].Strength != protocol.SignalWeak {
		t.Fatalf("signals = %+v", got)
	}
	if !strings.Contains(got[0].Evidence, "nudgeChiefRig") || strings.Contains(got[0].Evidence, "TestNudgeResume") {
		t.Fatalf("evidence %q", got[0].Evidence)
	}
}

// The two changes the owner named. ch_8334 copied a condition and leaned on
// timers and test helpers. ch_1cc0 only leaned on test helpers and volume.
func TestRealChangeFixtures(t *testing.T) {
	for _, tc := range []struct {
		file  string
		codes []string
	}{
		{"testdata/ch_8334.diff", []string{protocol.SignalRepeatedLine, protocol.SignalTestTiming, protocol.SignalHeavyTests, protocol.SignalTestHelpers}},
		{"testdata/ch_1cc0.diff", []string{protocol.SignalHeavyTests, protocol.SignalTestHelpers}},
	} {
		diff, err := os.ReadFile(tc.file)
		if err != nil {
			t.Fatal(err)
		}
		got := Signals(string(diff), "")
		if len(got) != len(tc.codes) {
			t.Fatalf("%s signals = %+v, want %v", tc.file, got, tc.codes)
		}
		for i, want := range tc.codes {
			if got[i].Code != want {
				t.Fatalf("%s signals = %v, want %v", tc.file, codes(got), tc.codes)
			}
		}
		for _, s := range got {
			t.Logf("%s: %s", tc.file, s.Line())
		}
	}
}

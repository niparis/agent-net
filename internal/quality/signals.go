// Package quality computes free quality signals from a worker change's diff.
// It uses no model and no service: the daemon runs it once at hand-back, and
// the gateway stores what it returns. The signals inform the owner and a later
// grader; nothing in this package blocks a change.
package quality

import (
	"fmt"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/niparis/agent-net/internal/protocol"
)

// Thresholds. Repeat a line only when it is long enough to carry meaning and
// short of copies that are ordinary test structure. Code lines are all added
// lines outside test files, so docs count against the difficulty budget too.
const (
	repeatedLineMinLen   = 24
	repeatedLineMinCount = 3

	heavyTestLines = 150 // more added test lines than this is heavy
	heavyTestRatio = 3   // and several times the added code lines

	// Added lines a difficulty may hold before the change is out of
	// proportion. An empty difficulty counts as normal, like the pipeline.
	linesTrivial        = 150
	linesSimple         = 300
	linesNormal         = 600
	linesHard           = 1000
	linesVeryComplex    = 1600
	maxSignalDiffBytes  = 4 << 20
	maxSignalFileLength = 128
)

var (
	testTimingPattern = regexp.MustCompile(`\btime\.(?:Sleep|After|NewTimer)\(`)
	testHelperPattern = regexp.MustCompile(`^\+func\s+(?:\([^)]*\)\s*)?([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
)

// Signals reads one `git diff -U0 base head` and returns the signals that
// fired, strong first. Difficulty is the judge's label; unknown labels are
// treated as normal. A diff too large to read cheaply gives no signals.
func Signals(diff, difficulty string) []protocol.QualitySignal {
	if diff == "" || len(diff) > maxSignalDiffBytes {
		return nil
	}
	var (
		total, testLines, codeLines int
		file                        string
		repeated                    = map[string]*repeat{}
		repeatOrder                 []string
		timingLines                 []string
		timingFiles                 []string
		helperNames                 []string
		helperFiles                 []string
	)
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "+++ "):
			file = diffPath(strings.TrimPrefix(line, "+++ "))
			continue
		case strings.HasPrefix(line, "--- "), strings.HasPrefix(line, "diff --git "), strings.HasPrefix(line, "@@"):
			continue
		case !strings.HasPrefix(line, "+"):
			continue
		}
		total++
		if strings.HasSuffix(file, "_test.go") {
			testLines++
			body := strings.TrimSpace(strings.TrimPrefix(line, "+"))
			if testTimingPattern.MatchString(body) {
				timingLines = append(timingLines, body)
				timingFiles = append(timingFiles, path.Base(file))
			}
			if m := testHelperPattern.FindStringSubmatch(line); m != nil && !testEntryPoint(m[1]) {
				helperNames = append(helperNames, m[1])
				helperFiles = append(helperFiles, path.Base(file))
			}
			continue
		}
		codeLines++
		body := strings.TrimSpace(strings.TrimPrefix(line, "+"))
		if !nonTrivialLine(body) {
			continue
		}
		r := repeated[body]
		if r == nil {
			r = &repeat{files: map[string]bool{}}
			repeated[body] = r
			repeatOrder = append(repeatOrder, body)
		}
		r.count++
		base := path.Base(file)
		if !r.files[base] {
			r.files[base] = true
			r.fileOrder = append(r.fileOrder, base)
		}
	}

	var out []protocol.QualitySignal
	if s := repeatedSignal(repeated, repeatOrder); s != nil {
		out = append(out, *s)
	}
	if s := timingSignal(timingLines, timingFiles); s != nil {
		out = append(out, *s)
	}
	if s := proportionSignal(total, difficulty); s != nil {
		out = append(out, *s)
	}
	if s := heavyTestSignal(testLines, codeLines); s != nil {
		out = append(out, *s)
	}
	if s := testHelperSignal(helperNames, helperFiles); s != nil {
		out = append(out, *s)
	}
	return out
}

type repeat struct {
	count     int
	files     map[string]bool
	fileOrder []string
}

// repeatedSignal picks the line with the most copies, then the one seen first.
func repeatedSignal(repeated map[string]*repeat, order []string) *protocol.QualitySignal {
	best := ""
	for _, line := range order {
		r := repeated[line]
		if r.count < repeatedLineMinCount {
			continue
		}
		if best == "" || r.count > repeated[best].count {
			best = line
		}
	}
	if best == "" {
		return nil
	}
	r := repeated[best]
	return &protocol.QualitySignal{Code: protocol.SignalRepeatedLine, Strength: protocol.SignalStrong,
		Evidence: clipEvidence(fmt.Sprintf("repeated line ×%d: %s in %s", r.count, best, strings.Join(r.fileOrder, ", ")))}
}

func timingSignal(lines, files []string) *protocol.QualitySignal {
	if len(lines) == 0 {
		return nil
	}
	return &protocol.QualitySignal{Code: protocol.SignalTestTiming, Strength: protocol.SignalStrong,
		Evidence: clipEvidence(fmt.Sprintf("test timing ×%d: %s in %s", len(lines), strings.Join(lines, ", "), strings.Join(unique(files), ", ")))}
}

func proportionSignal(total int, difficulty string) *protocol.QualitySignal {
	limit, level := proportionLimit(difficulty)
	if total <= limit {
		return nil
	}
	return &protocol.QualitySignal{Code: protocol.SignalOutOfProportion, Strength: protocol.SignalWeak,
		Evidence: fmt.Sprintf("lines out of proportion: %d added, above the %s limit of %d", total, level, limit)}
}

func heavyTestSignal(testLines, codeLines int) *protocol.QualitySignal {
	if testLines <= heavyTestLines {
		return nil
	}
	if codeLines > 0 && testLines < heavyTestRatio*codeLines {
		return nil
	}
	return &protocol.QualitySignal{Code: protocol.SignalHeavyTests, Strength: protocol.SignalWeak,
		Evidence: fmt.Sprintf("heavy tests: %d test lines added for %d code lines", testLines, codeLines)}
}

func testHelperSignal(names, files []string) *protocol.QualitySignal {
	if len(names) == 0 {
		return nil
	}
	return &protocol.QualitySignal{Code: protocol.SignalTestHelpers, Strength: protocol.SignalWeak,
		Evidence: clipEvidence(fmt.Sprintf("new test helper ×%d: %s in %s", len(names), strings.Join(names, ", "), strings.Join(unique(files), ", ")))}
}

func proportionLimit(difficulty string) (int, string) {
	switch difficulty {
	case "trivial":
		return linesTrivial, "trivial"
	case "simple":
		return linesSimple, "simple"
	case "hard":
		return linesHard, "hard"
	case "very_complex":
		return linesVeryComplex, "very_complex"
	default:
		return linesNormal, "normal"
	}
}

// nonTrivialLine drops braces, blanks, comments and boilerplate short enough
// that the same line says nothing about copy-paste.
func nonTrivialLine(line string) bool {
	if len(line) < repeatedLineMinLen || strings.HasPrefix(line, "//") {
		return false
	}
	letters := false
	for _, r := range line {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			letters = true
			break
		}
	}
	return letters
}

func diffPath(raw string) string {
	raw = strings.TrimSpace(raw)
	if i := strings.IndexByte(raw, '\t'); i >= 0 {
		raw = raw[:i]
	}
	raw = strings.TrimPrefix(raw, "b/")
	if raw == "/dev/null" {
		return ""
	}
	if len(raw) > maxSignalFileLength {
		raw = raw[:maxSignalFileLength]
	}
	return raw
}

func testEntryPoint(name string) bool {
	for _, prefix := range []string{"Test", "Benchmark", "Fuzz", "Example"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func unique(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func clipEvidence(s string) string {
	if len(s) <= protocol.MaxSignalEvidenceBytes {
		return s
	}
	const tail = "..."
	end := protocol.MaxSignalEvidenceBytes - len(tail)
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end] + tail
}

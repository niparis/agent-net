package gateway

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// A wake goroutine can still read the test clock while a test moves it. The
// clock takes its lock in set and advance, so a test that assigns the field
// directly brings the data race back.
func TestTestsMoveTheClockOnlyThroughSetAndAdvance(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	direct := regexp.MustCompile(`\b\w+\.t\s*(=|\+=|-=)[^=]`)
	for _, e := range entries {
		name := e.Name()
		// store_test.go defines the clock, so its own methods write the field.
		if e.IsDir() || !strings.HasSuffix(name, "_test.go") || name == "store_test.go" {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			if direct.MatchString(line) {
				t.Errorf("%s:%d writes the test clock directly, use c.set or c.advance: %s", name, i+1, strings.TrimSpace(line))
			}
		}
	}
}

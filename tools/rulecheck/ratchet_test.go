package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const siteKey = "github.com/x/internal/daemon/a.go F d.call while d.mu"

func site(key string) finding {
	return finding{file: "a.go", line: 3, col: 2, rule: "lockcall", key: key}
}

func TestRatchetLocks(t *testing.T) {
	analysed := map[string]bool{"github.com/x/internal/daemon": true}
	for _, tc := range []struct {
		name     string
		sites    []finding
		baseline map[string]int
		want     int
		msg      string
	}{
		{"listed site passes", []finding{site(siteKey)}, map[string]int{siteKey: 1}, 0, ""},
		{"new site fails", []finding{site(siteKey)}, map[string]int{}, 1, "new call under a lock"},
		{"extra site over the count fails", []finding{site(siteKey), site(siteKey)}, map[string]int{siteKey: 1}, 2, "new call under a lock"},
		{"fixed site leaves a stale count", nil, map[string]int{siteKey: 1}, 1, "lower its count"},
		{"package not analysed is not judged", nil, map[string]int{"github.com/y/internal/daemon/b.go G x while y": 1}, 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ratchetLocks(tc.sites, tc.baseline, analysed, "lockcall.baseline")
			if len(got) != tc.want {
				t.Fatalf("want %d findings, got %v", tc.want, got)
			}
			if tc.msg != "" && (len(got) == 0 || !strings.Contains(got[0].msg, tc.msg)) {
				t.Fatalf("want a message with %q, got %v", tc.msg, got)
			}
		})
	}
}

func TestLoadBaseline(t *testing.T) {
	file := filepath.Join(t.TempDir(), "lockcall.baseline")
	write(t, file, "# header\n\n2\t"+siteKey+"\n")
	got, err := loadBaseline(file)
	if err != nil {
		t.Fatal(err)
	}
	if got[siteKey] != 2 || len(got) != 1 {
		t.Fatalf("baseline %v", got)
	}
	for name, text := range map[string]string{
		"no tab":    "2 " + siteKey + "\n",
		"not count": "many\t" + siteKey + "\n",
		"duplicate": "1\t" + siteKey + "\n1\t" + siteKey + "\n",
	} {
		write(t, file, text)
		if _, err := loadBaseline(file); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if _, err := loadBaseline(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("a missing baseline must be an error, not an empty list")
	}
}

func TestPrintLockBaselineIsSortedAndCounted(t *testing.T) {
	var out bytes.Buffer
	printLockBaseline(&out, []finding{site("b key"), site("a key"), site("a key")})
	if got, want := out.String(), "2\ta key\n1\tb key\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestPkgOfKey(t *testing.T) {
	if got := pkgOfKey(siteKey); got != "github.com/x/internal/daemon" {
		t.Fatalf("pkgOfKey = %q", got)
	}
}

// TestRunDocsOnly runs the command on docs alone: no packages are loaded, so the
// exit code shows the result of the em dash check.
func TestRunDocsOnly(t *testing.T) {
	bad := t.TempDir()
	write(t, filepath.Join(bad, "x.md"), "a \u2014 b\n")
	var out, errOut bytes.Buffer
	if code := run([]string{"-docs", bad}, &out, &errOut); code != 1 {
		t.Fatalf("exit %d, want 1: %s%s", code, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "[emdash]") {
		t.Fatalf("output %q does not name the rule", out.String())
	}

	clean := t.TempDir()
	if err := os.WriteFile(filepath.Join(clean, "x.md"), []byte("a - b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errOut.Reset()
	if code := run([]string{"-docs", clean}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d, want 0: %s%s", code, out.String(), errOut.String())
	}
}

func TestRunNeedsWork(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run(nil, &out, &errOut); code != 2 {
		t.Fatalf("exit %d, want 2 for no docs and no packages", code)
	}
}

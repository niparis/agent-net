package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
)

// The lock check is a ratchet. The sites that existed when the check was added
// are listed in lockcall.baseline with their count. A site that is not listed,
// or a listed site that appears more often than its count, fails the check. A
// listed site that has gone also fails it, so the list shrinks as sites are
// fixed and never grows. The list is debt to pay down, not an exemption.

// loadBaseline reads lines of the form "<count><TAB><key>". A line that starts
// with # is a comment, and a blank line is skipped.
func loadBaseline(file string) (map[string]int, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("cannot read the lock baseline: %w", err)
	}
	out := map[string]int{}
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	for line := 1; sc.Scan(); line++ {
		text := sc.Text()
		if strings.TrimSpace(text) == "" || strings.HasPrefix(text, "#") {
			continue
		}
		count, key, ok := strings.Cut(text, "\t")
		n, err := strconv.Atoi(count)
		if !ok || err != nil || n < 1 || key == "" {
			return nil, fmt.Errorf("%s:%d: want a count, a tab and a key", file, line)
		}
		if _, dup := out[key]; dup {
			return nil, fmt.Errorf("%s:%d: %q is listed twice", file, line, key)
		}
		out[key] = n
	}
	return out, sc.Err()
}

// ratchetLocks compares the lock-across-call sites with the baseline. Only the
// baseline entries of the analysed packages are judged, so a run over changed
// packages does not call the rest of the list stale.
func ratchetLocks(sites []finding, baseline map[string]int, analysed map[string]bool, baselineFile string) []finding {
	byKey := map[string][]finding{}
	for _, s := range sites {
		byKey[s.key] = append(byKey[s.key], s)
	}
	var out []finding
	for key, found := range byKey {
		if allowed := baseline[key]; len(found) > allowed {
			for _, f := range found {
				f.msg = fmt.Sprintf("new call under a lock (B12): %d site(s) here, the baseline allows %d; unlock first", len(found), allowed)
				out = append(out, f)
			}
		}
	}
	for key, allowed := range baseline {
		if !analysed[pkgOfKey(key)] {
			continue
		}
		if remain := len(byKey[key]); remain < allowed {
			out = append(out, finding{
				file: baselineFile,
				line: 1,
				col:  1,
				rule: LockAnalyzer.Name,
				msg:  fmt.Sprintf("lockcall.baseline lists %d site(s) for %q, but %d remain: lower its count", allowed, key, remain),
			})
		}
	}
	return out
}

// printLockBaseline writes the sites in baseline format, sorted by key, so a
// reviewer can compare the list with the file.
func printLockBaseline(w io.Writer, sites []finding) {
	counts := map[string]int{}
	for _, s := range sites {
		counts[s.key]++
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(w, "%d\t%s\n", counts[k], k)
	}
}

// pkgOfKey returns the import path of the package a baseline key names. A key
// starts with the package path, a slash, the file name and a space.
func pkgOfKey(key string) string {
	first, _, _ := strings.Cut(key, " ")
	return path.Dir(first)
}

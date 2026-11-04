package tsnode

import (
	"fmt"
	"os"
	"sort"
	"testing"
)

// TestMain fails the run when a test leaves a new file in the package
// folder. Tests must write under t.TempDir(); a node state file in the
// source tree would end up in a commit.
func TestMain(m *testing.M) {
	before, err := folderNames(".")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	after, err := folderNames(".")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var added []string
	for name := range after {
		if !before[name] {
			added = append(added, name)
		}
	}
	if len(added) > 0 {
		sort.Strings(added)
		fmt.Fprintf(os.Stderr, "tests left new files in the package folder, write them under t.TempDir(): %v\n", added)
		code = 1
	}
	os.Exit(code)
}

func folderNames(dir string) (map[string]bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := make(map[string]bool, len(entries))
	for _, e := range entries {
		names[e.Name()] = true
	}
	return names, nil
}

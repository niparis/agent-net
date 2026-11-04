package delivery

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
)

// Status is how a file changed between two commits.
type Status string

const (
	StatusAdded    Status = "added"
	StatusModified Status = "modified"
	StatusDeleted  Status = "deleted"
	StatusRenamed  Status = "renamed"
)

// FileChange is one file in a change. For a rename, Path is the new path and
// OldPath is the old one. AddedLines holds the lines the change added; it is
// filled only for Rust files, by LoadRustAddedLines.
type FileChange struct {
	Path       string
	OldPath    string
	Status     Status
	AddedLines []string
}

// Result is the answer of the tests_added evidence check.
type Result struct {
	Added  bool
	Files  []string // the test files that were added or changed, sorted
	Reason string
}

func runGit(ctx context.Context, repoDir string, args ...string) ([]byte, error) {
	return gitIn{dir: repoDir}.bytes(ctx, nil, args...)
}

func checkRef(name, ref string) error {
	return checkCommitID(name, ref)
}

// ChangedFiles lists the files that head changed since the merge base of base
// and head (git diff --name-status -M base...head).
func ChangedFiles(ctx context.Context, repoDir, base, head string) ([]FileChange, error) {
	if err := checkRef("base", base); err != nil {
		return nil, err
	}
	if err := checkRef("head", head); err != nil {
		return nil, err
	}
	out, err := runGit(ctx, repoDir, "diff", "--name-status", "-M", "-z", "--no-color", "--no-ext-diff", base+"..."+head)
	if err != nil {
		return nil, err
	}
	return parseNameStatus(string(out))
}

// parseNameStatus reads the NUL separated output of git diff --name-status -z.
func parseNameStatus(out string) ([]FileChange, error) {
	var files []FileChange
	fields := strings.Split(out, "\x00")
	// The output ends with a NUL, so the last field is empty.
	if n := len(fields); n > 0 && fields[n-1] == "" {
		fields = fields[:n-1]
	}
	for i := 0; i < len(fields); {
		code := fields[i]
		i++
		if code == "" {
			return nil, errors.New("git diff: empty status")
		}
		switch code[0] {
		case 'A', 'M', 'D', 'T':
			if i >= len(fields) {
				return nil, fmt.Errorf("git diff: status %q has no path", code)
			}
			st := StatusModified
			switch code[0] {
			case 'A':
				st = StatusAdded
			case 'D':
				st = StatusDeleted
			}
			files = append(files, FileChange{Path: fields[i], Status: st})
			i++
		case 'R', 'C':
			if i+1 >= len(fields) {
				return nil, fmt.Errorf("git diff: status %q needs two paths", code)
			}
			if code[0] == 'R' {
				files = append(files, FileChange{Path: fields[i+1], OldPath: fields[i], Status: StatusRenamed})
			} else {
				// A copy is a new file as far as tests are concerned.
				files = append(files, FileChange{Path: fields[i+1], Status: StatusAdded})
			}
			i += 2
		default:
			return nil, fmt.Errorf("git diff: unknown status %q", code)
		}
	}
	return files, nil
}

// LoadRustAddedLines returns a copy of files in which every Rust file that is
// not already a test by its path, and was not deleted, carries the lines the
// change added. TestsAdded needs these to see inline "#[test]" and
// "#[cfg(test)]" blocks.
func LoadRustAddedLines(ctx context.Context, repoDir, base, head string, files []FileChange) ([]FileChange, error) {
	if err := checkRef("base", base); err != nil {
		return nil, err
	}
	if err := checkRef("head", head); err != nil {
		return nil, err
	}
	res := make([]FileChange, len(files))
	copy(res, files)
	for i, f := range res {
		if f.Status == StatusDeleted || !strings.HasSuffix(f.Path, ".rs") || isTestPath(f.Path) {
			continue
		}
		args := []string{"diff", "-U0", "-M", "--no-color", "--no-ext-diff", "--no-textconv", base + "..." + head, "--"}
		if f.OldPath != "" {
			args = append(args, f.OldPath)
		}
		args = append(args, f.Path)
		out, err := runGit(ctx, repoDir, args...)
		if err != nil {
			return nil, err
		}
		res[i].AddedLines = addedLines(string(out))
	}
	return res, nil
}

// addedLines returns the lines a unified diff adds, without the leading "+".
func addedLines(diff string) []string {
	var lines []string
	inHunk := false
	for _, l := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(l, "diff --git "):
			inHunk = false
		case strings.HasPrefix(l, "@@"):
			inHunk = true
		case inHunk && strings.HasPrefix(l, "+"):
			lines = append(lines, l[1:])
		}
	}
	return lines
}

// DetectTestsAdded runs the whole check on a repo: it lists the changed files,
// reads the added lines of Rust files, and applies the rules.
func DetectTestsAdded(ctx context.Context, repoDir, base, head string) (Result, error) {
	files, err := ChangedFiles(ctx, repoDir, base, head)
	if err != nil {
		return Result{}, err
	}
	files, err = LoadRustAddedLines(ctx, repoDir, base, head, files)
	if err != nil {
		return Result{}, err
	}
	return TestsAdded(files), nil
}

// TestsAdded reports whether the change adds or changes a test file. A deleted
// test file does not count. Test data (testdata/, fixtures) does not count
// either. A Rust file outside tests/ counts only when AddedLines holds a
// "#[test]" or "#[cfg(test)]" line.
func TestsAdded(files []FileChange) Result {
	seen := map[string]bool{}
	var tests []string
	var deletedTests, data bool
	for _, f := range files {
		switch {
		case f.Status == StatusDeleted:
			if isTestPath(f.Path) {
				deletedTests = true
			}
		case isTestPath(f.Path) || hasRustTestMarker(f):
			if !seen[f.Path] {
				seen[f.Path] = true
				tests = append(tests, f.Path)
			}
		case isTestDataPath(f.Path):
			data = true
		}
	}
	sort.Strings(tests)
	switch {
	case len(tests) > 0:
		return Result{Added: true, Files: tests, Reason: "test files added or changed: " + strings.Join(tests, ", ")}
	case data:
		return Result{Reason: "only test data changed (testdata or fixtures), no test files"}
	case deletedTests:
		return Result{Reason: "only test files deleted, none added or changed"}
	default:
		return Result{Reason: "no test files added or changed"}
	}
}

func hasRustTestMarker(f FileChange) bool {
	if !strings.HasSuffix(f.Path, ".rs") {
		return false
	}
	for _, l := range f.AddedLines {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "#[test]") || strings.HasPrefix(l, "#[cfg(test)]") {
			return true
		}
	}
	return false
}

var jsExts = map[string]bool{".js": true, ".jsx": true, ".ts": true, ".tsx": true, ".mjs": true, ".cjs": true}

func dirSegments(p string) []string {
	dir := path.Dir(p)
	if dir == "." {
		return nil
	}
	return strings.Split(dir, "/")
}

func hasSegment(segs []string, names ...string) bool {
	for _, s := range segs {
		for _, n := range names {
			if s == n {
				return true
			}
		}
	}
	return false
}

// isTestDataPath reports whether the path is under a test data directory.
func isTestDataPath(p string) bool {
	return hasSegment(dirSegments(p), "testdata", "fixtures", "__fixtures__")
}

// isTestPath reports whether the path is a test file in one of the supported
// languages. Files under test data directories never are.
func isTestPath(p string) bool {
	segs := dirSegments(p)
	if hasSegment(segs, "testdata", "fixtures", "__fixtures__") {
		return false
	}
	base := path.Base(p)
	ext := path.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	switch {
	case ext == ".go":
		return strings.HasSuffix(stem, "_test")
	case ext == ".py":
		return strings.HasPrefix(stem, "test_") || strings.HasSuffix(stem, "_test") || hasSegment(segs, "tests", "test")
	case jsExts[ext]:
		return strings.HasSuffix(stem, ".test") || strings.HasSuffix(stem, ".spec") || hasSegment(segs, "__tests__")
	case ext == ".rs":
		return hasSegment(segs, "tests")
	case ext == ".bats":
		return true
	}
	return false
}

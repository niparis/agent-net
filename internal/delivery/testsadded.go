package delivery

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
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
	Path    string
	OldPath string
	Status  Status
	// Added is how many lines the change added to the file, or -1 for a
	// binary file. A pure rename or a mode change adds none.
	Added      int
	AddedLines []string
}

// Result is the answer of the tests_added evidence check.
type Result struct {
	// Added is true when the change adds lines to a test file.
	Added bool
	Files []string // the test files that gained lines, sorted
	// CodeChanged is true when the change touches a file that is neither a
	// test, test data, nor documentation. A change with no code needs no
	// new test.
	CodeChanged bool
	Reason      string
}

// ChangeRange names a change by the commit IDs that were recorded for it.
type ChangeRange struct {
	// RepoDir is a repository that has both commits.
	RepoDir string
	// BaseSHA and HeadSHA are the recorded base and head, full commit IDs.
	// The change is what HeadSHA adds since the merge base of the two.
	BaseSHA string
	HeadSHA string
	// GitPath is the PATH list git is found on and runs with (see
	// MergeRequest.GitPath). Empty means this process's PATH.
	GitPath string
}

// check validates the range and refuses a repository with replace refs or a
// grafts file.
func (r ChangeRange) check(ctx context.Context) (gitIn, error) {
	g := gitIn{gitPath: r.GitPath, dir: r.RepoDir}
	if r.RepoDir == "" {
		return g, errors.New("tests added: RepoDir is required")
	}
	if err := checkCommitID("base", r.BaseSHA); err != nil {
		return g, err
	}
	if err := checkCommitID("head", r.HeadSHA); err != nil {
		return g, err
	}
	return g, checkHistory(ctx, g)
}

// diffArgs are the options every diff of a change uses, so the lists agree
// on renames.
func (r ChangeRange) diffArgs(format ...string) []string {
	args := append([]string{"diff"}, format...)
	return append(args, "-M", "-z", "--no-color", "--no-ext-diff", "--no-textconv", r.BaseSHA+"..."+r.HeadSHA)
}

// ChangedFiles lists the files that the change touched since the merge base
// of base and head (git diff -M base...head), with how many lines each one
// gained.
func ChangedFiles(ctx context.Context, r ChangeRange) ([]FileChange, error) {
	g, err := r.check(ctx)
	if err != nil {
		return nil, err
	}
	out, err := g.bytes(ctx, nil, r.diffArgs("--name-status")...)
	if err != nil {
		return nil, err
	}
	files, err := parseNameStatus(string(out))
	if err != nil {
		return nil, err
	}
	out, err = g.bytes(ctx, nil, r.diffArgs("--numstat")...)
	if err != nil {
		return nil, err
	}
	added, err := parseNumstat(string(out))
	if err != nil {
		return nil, err
	}
	for i := range files {
		files[i].Added = added[files[i].Path]
	}
	return files, nil
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

// parseNumstat reads the output of git diff --numstat -z: "added\tdeleted\tpath"
// ended by NUL, or for a rename or copy "added\tdeleted\t" then the old and
// the new path, each ended by NUL. A binary file has "-" counts. It returns
// the added lines by (new) path, -1 for a binary file.
func parseNumstat(out string) (map[string]int, error) {
	added := map[string]int{}
	fields := strings.Split(out, "\x00")
	if n := len(fields); n > 0 && fields[n-1] == "" {
		fields = fields[:n-1]
	}
	for i := 0; i < len(fields); i++ {
		parts := strings.SplitN(fields[i], "\t", 3)
		if len(parts) != 3 {
			return nil, fmt.Errorf("git diff --numstat: bad entry %q", fields[i])
		}
		p := parts[2]
		if p == "" {
			if i+2 >= len(fields) {
				return nil, fmt.Errorf("git diff --numstat: rename %q needs two paths", fields[i])
			}
			p = fields[i+2]
			i += 2
		}
		n := -1
		if parts[0] != "-" {
			var err error
			if n, err = strconv.Atoi(parts[0]); err != nil || n < 0 {
				return nil, fmt.Errorf("git diff --numstat: bad count %q", parts[0])
			}
		}
		added[p] = n
	}
	return added, nil
}

// needsRustLines reports whether TestsAdded must look at the added lines of
// f to find inline tests.
func needsRustLines(f FileChange) bool {
	return f.Status != StatusDeleted && strings.HasSuffix(f.Path, ".rs") && !isTestPath(f.Path)
}

// LoadRustAddedLines returns a copy of files in which every Rust file that is
// not already a test by its path, and was not deleted, carries the lines the
// change added. TestsAdded needs these to see inline test blocks. It reads
// one diff for all such files.
func LoadRustAddedLines(ctx context.Context, r ChangeRange, files []FileChange) ([]FileChange, error) {
	g, err := r.check(ctx)
	if err != nil {
		return nil, err
	}
	res := make([]FileChange, len(files))
	copy(res, files)
	var paths []string
	for _, f := range res {
		if needsRustLines(f) {
			if f.OldPath != "" {
				paths = append(paths, f.OldPath)
			}
			paths = append(paths, f.Path)
		}
	}
	if len(paths) == 0 {
		return res, nil
	}
	args := []string{"diff", "-U0", "-M", "--no-color", "--no-ext-diff", "--no-textconv",
		"--src-prefix=a/", "--dst-prefix=b/", r.BaseSHA + "..." + r.HeadSHA, "--"}
	out, err := g.bytes(ctx, nil, append(args, paths...)...)
	if err != nil {
		return nil, err
	}
	byPath := addedLinesByFile(string(out))
	for i, f := range res {
		if needsRustLines(f) {
			res[i].AddedLines = byPath[f.Path]
		}
	}
	return res, nil
}

// addedLinesByFile reads a unified diff of several files and returns the
// lines each file gained, without the leading "+", by new path. Inside a
// hunk every line starts with "+", "-", " " or "\", so a header can never be
// mistaken for content.
func addedLinesByFile(diff string) map[string][]string {
	byPath := map[string][]string{}
	file := ""
	inHunk := false
	for _, l := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(l, "diff --git "):
			file, inHunk = "", false
		case !inHunk && strings.HasPrefix(l, "+++ "):
			file = diffPath(strings.TrimPrefix(l, "+++ "))
		case strings.HasPrefix(l, "@@"):
			inHunk = true
		case inHunk && file != "" && strings.HasPrefix(l, "+"):
			byPath[file] = append(byPath[file], l[1:])
		}
	}
	return byPath
}

// diffPath reads the path of a "+++ " line: "b/<path>", C-quoted when it has
// special characters, followed by a tab when it has a space. /dev/null gives
// "".
func diffPath(s string) string {
	s = strings.TrimSuffix(s, "\t")
	if strings.HasPrefix(s, `"`) {
		u, err := strconv.Unquote(s)
		if err != nil {
			return ""
		}
		s = u
	}
	p, ok := strings.CutPrefix(s, "b/")
	if !ok {
		return ""
	}
	return p
}

// DetectTestsAdded runs the whole check on a repo: it lists the changed files,
// reads the added lines of Rust files, and applies the rules.
func DetectTestsAdded(ctx context.Context, r ChangeRange) (Result, error) {
	files, err := ChangedFiles(ctx, r)
	if err != nil {
		return Result{}, err
	}
	files, err = LoadRustAddedLines(ctx, r, files)
	if err != nil {
		return Result{}, err
	}
	return TestsAdded(files), nil
}

// TestsAdded reports whether the change adds lines to a test file. A deleted
// test file does not count, nor does a test file that was only renamed or had
// its mode changed. Test data (testdata/, fixtures) does not count either. A
// Rust file outside tests/ counts only when AddedLines holds a test
// attribute such as "#[test]" or "#[cfg(test)]".
func TestsAdded(files []FileChange) Result {
	seen := map[string]bool{}
	var tests []string
	var deletedTests, untouchedTests, data, code bool
	for _, f := range files {
		if !isTestPath(f.Path) && !isTestDataPath(f.Path) && !isDocPath(f.Path) {
			code = true
		}
		switch {
		case f.Status == StatusDeleted:
			if isTestPath(f.Path) {
				deletedTests = true
			}
		case (isTestPath(f.Path) && f.Added > 0) || hasRustTestMarker(f):
			if !seen[f.Path] {
				seen[f.Path] = true
				tests = append(tests, f.Path)
			}
		case isTestPath(f.Path):
			untouchedTests = true
		case isTestDataPath(f.Path):
			data = true
		}
	}
	sort.Strings(tests)
	res := Result{CodeChanged: code}
	switch {
	case len(tests) > 0:
		res.Added, res.Files = true, tests
		res.Reason = "test files gained lines: " + strings.Join(tests, ", ")
	case untouchedTests:
		res.Reason = "test files were only renamed or changed mode, no test lines added"
	case data:
		res.Reason = "only test data changed (testdata or fixtures), no test files"
	case deletedTests:
		res.Reason = "only test files deleted, none added or changed"
	default:
		res.Reason = "no test files added or changed"
	}
	return res
}

// rustTestAttrRE matches the attributes that mark Rust test code.
var rustTestAttrRE = regexp.MustCompile(`^#\[(?:cfg\(test\)|(?:test|tokio::test|async_std::test|rstest|test_case)\b)`)

func hasRustTestMarker(f FileChange) bool {
	if f.Status == StatusDeleted || !strings.HasSuffix(f.Path, ".rs") {
		return false
	}
	for _, l := range f.AddedLines {
		if rustTestAttrRE.MatchString(strings.TrimSpace(l)) {
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

// docExts are documentation file types.
var docExts = map[string]bool{".md": true, ".markdown": true, ".rst": true, ".adoc": true}

// isDocPath reports whether the path is documentation: a file under the top
// docs/ directory, a Markdown, reStructuredText or AsciiDoc file, or a
// license file.
func isDocPath(p string) bool {
	if strings.HasPrefix(p, "docs/") || docExts[strings.ToLower(path.Ext(p))] {
		return true
	}
	base := strings.ToUpper(path.Base(p))
	return strings.HasPrefix(base, "LICENSE") || strings.HasPrefix(base, "LICENCE") || strings.HasPrefix(base, "COPYING")
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

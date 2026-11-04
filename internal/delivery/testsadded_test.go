package delivery

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// change is a file that gained one line, or a deleted file.
func change(status Status, path string) FileChange {
	added := 1
	if status == StatusDeleted {
		added = 0
	}
	return FileChange{Path: path, Status: status, Added: added}
}

func TestIsTestPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		// Go
		{"internal/foo/foo_test.go", true},
		{"foo_test.go", true},
		{"internal/foo/foo.go", false},
		{"internal/foo/testdata/x_test.go", false},
		// Python
		{"pkg/test_api.py", true},
		{"pkg/api_test.py", true},
		{"tests/helpers.py", true},
		{"test/helpers.py", true},
		{"src/pkg/tests/sub/util.py", true},
		{"pkg/api.py", false},
		{"pkg/contest.py", false},
		{"tests/data.json", false},
		{"tests/fixtures/helper.py", false},
		// JS and TS
		{"src/a.test.ts", true},
		{"src/a.test.tsx", true},
		{"src/a.test.js", true},
		{"src/a.test.jsx", true},
		{"src/a.test.mjs", true},
		{"src/a.test.cjs", true},
		{"src/a.spec.ts", true},
		{"src/a.spec.tsx", true},
		{"src/a.spec.js", true},
		{"src/a.spec.jsx", true},
		{"src/a.spec.mjs", true},
		{"src/a.spec.cjs", true},
		{"src/__tests__/a.ts", true},
		{"src/__tests__/deep/a.js", true},
		{"src/__tests__/__snapshots__/a.snap", false},
		{"src/a.ts", false},
		{"src/a.test.json", false},
		{"src/atest.ts", false},
		// Rust
		{"tests/integration.rs", true},
		{"crate/tests/it.rs", true},
		{"tests/readme.md", false},
		{"src/lib.rs", false},
		// shell
		{"scripts/run.bats", true},
		{"scripts/run.sh", false},
		// data and fixtures
		{"testdata/input.txt", false},
		{"fixtures/user.json", false},
		{"pkg/testdata/golden.json", false},
		{"pkg/__fixtures__/a.js", false},
	}
	for _, c := range cases {
		if got := isTestPath(c.path); got != c.want {
			t.Errorf("isTestPath(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestIsTestDataPath(t *testing.T) {
	cases := map[string]bool{
		"testdata/a.txt":         true,
		"pkg/testdata/a.txt":     true,
		"fixtures/a.json":        true,
		"tests/fixtures/a.json":  true,
		"src/__fixtures__/a.js":  true,
		"pkg/a.go":               false,
		"pkg/testdata_helper.go": false,
		"pkg/fixtures_test.go":   false,
	}
	for p, want := range cases {
		if got := isTestDataPath(p); got != want {
			t.Errorf("isTestDataPath(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestTestsAdded(t *testing.T) {
	cases := []struct {
		name      string
		files     []FileChange
		wantAdded bool
		wantFiles []string
		reason    string // substring
	}{
		{
			name:   "no files",
			reason: "no test files",
		},
		{
			name:      "go test added",
			files:     []FileChange{change(StatusAdded, "a/a_test.go"), change(StatusModified, "a/a.go")},
			wantAdded: true,
			wantFiles: []string{"a/a_test.go"},
			reason:    "a/a_test.go",
		},
		{
			name:      "go test modified",
			files:     []FileChange{change(StatusModified, "a/a_test.go")},
			wantAdded: true,
			wantFiles: []string{"a/a_test.go"},
		},
		{
			name:      "python test",
			files:     []FileChange{change(StatusAdded, "pkg/test_x.py")},
			wantAdded: true,
			wantFiles: []string{"pkg/test_x.py"},
		},
		{
			name:      "js test",
			files:     []FileChange{change(StatusAdded, "src/x.spec.ts")},
			wantAdded: true,
			wantFiles: []string{"src/x.spec.ts"},
		},
		{
			name:      "rust tests dir",
			files:     []FileChange{change(StatusAdded, "tests/it.rs")},
			wantAdded: true,
			wantFiles: []string{"tests/it.rs"},
		},
		{
			name:      "bats",
			files:     []FileChange{change(StatusAdded, "t/x.bats")},
			wantAdded: true,
			wantFiles: []string{"t/x.bats"},
		},
		{
			name: "rust inline #[test] in added lines",
			files: []FileChange{{
				Path: "src/lib.rs", Status: StatusModified,
				AddedLines: []string{"fn x() {}", "    #[test]", "fn y() {}"},
			}},
			wantAdded: true,
			wantFiles: []string{"src/lib.rs"},
		},
		{
			name: "rust inline #[cfg(test)] in added lines",
			files: []FileChange{{
				Path: "src/lib.rs", Status: StatusModified,
				AddedLines: []string{"#[cfg(test)]", "mod tests {}"},
			}},
			wantAdded: true,
			wantFiles: []string{"src/lib.rs"},
		},
		{
			name: "rust file without markers",
			files: []FileChange{{
				Path: "src/lib.rs", Status: StatusModified,
				AddedLines: []string{"fn x() {}"},
			}},
			reason: "no test files",
		},
		{
			name: "rust marker only in a comment",
			files: []FileChange{{
				Path: "src/lib.rs", Status: StatusModified,
				AddedLines: []string{"// #[test] is how you write one"},
			}},
			reason: "no test files",
		},
		{
			name: "marker lines on a non-rust file are ignored",
			files: []FileChange{{
				Path: "src/lib.go", Status: StatusModified,
				AddedLines: []string{"#[test]"},
			}},
			reason: "no test files",
		},
		{
			name:   "only deleted tests",
			files:  []FileChange{change(StatusDeleted, "a/a_test.go"), change(StatusModified, "a/a.go")},
			reason: "only test files deleted",
		},
		{
			name: "deleted rust file with markers does not count",
			files: []FileChange{{
				Path: "src/lib.rs", Status: StatusDeleted,
				AddedLines: []string{"#[test]"},
			}},
			reason: "no test files",
		},
		{
			name:   "only testdata",
			files:  []FileChange{change(StatusAdded, "pkg/testdata/in.txt"), change(StatusModified, "pkg/a.go")},
			reason: "only test data changed",
		},
		{
			name:   "only fixtures",
			files:  []FileChange{change(StatusModified, "tests/fixtures/u.json")},
			reason: "only test data changed",
		},
		{
			name:      "testdata plus a real test counts",
			files:     []FileChange{change(StatusAdded, "pkg/testdata/in.txt"), change(StatusAdded, "pkg/a_test.go")},
			wantAdded: true,
			wantFiles: []string{"pkg/a_test.go"},
		},
		{
			name:      "renamed test with new lines counts",
			files:     []FileChange{{Path: "pkg/b_test.go", OldPath: "pkg/a_test.go", Status: StatusRenamed, Added: 3}},
			wantAdded: true,
			wantFiles: []string{"pkg/b_test.go"},
		},
		{
			name:   "pure rename of a test does not count",
			files:  []FileChange{{Path: "pkg/b_test.go", OldPath: "pkg/a_test.go", Status: StatusRenamed}},
			reason: "only renamed or changed mode",
		},
		{
			name:   "mode change of a test does not count",
			files:  []FileChange{{Path: "pkg/a_test.go", Status: StatusModified}},
			reason: "only renamed or changed mode",
		},
		{
			name:   "binary file at a test path does not count",
			files:  []FileChange{{Path: "pkg/a_test.go", Status: StatusAdded, Added: -1}},
			reason: "only renamed or changed mode",
		},
		{
			name:   "rename from test to non-test does not count",
			files:  []FileChange{{Path: "pkg/b.go", OldPath: "pkg/a_test.go", Status: StatusRenamed, Added: 1}},
			reason: "no test files",
		},
		{
			name: "rust tokio, async_std, rstest and test_case attributes",
			files: []FileChange{
				{Path: "src/a.rs", Status: StatusModified, Added: 1, AddedLines: []string{"#[tokio::test]"}},
				{Path: "src/b.rs", Status: StatusModified, Added: 1, AddedLines: []string{"  #[tokio::test(flavor = \"multi_thread\")]"}},
				{Path: "src/c.rs", Status: StatusModified, Added: 1, AddedLines: []string{"#[rstest]"}},
				{Path: "src/d.rs", Status: StatusModified, Added: 1, AddedLines: []string{"#[test_case(1, 2)]"}},
				{Path: "src/e.rs", Status: StatusModified, Added: 1, AddedLines: []string{"#[async_std::test]"}},
				{Path: "src/f.rs", Status: StatusModified, Added: 1, AddedLines: []string{"#[testing_helper]", "#[tests]"}},
			},
			wantAdded: true,
			wantFiles: []string{"src/a.rs", "src/b.rs", "src/c.rs", "src/d.rs", "src/e.rs"},
		},
		{
			name: "files are sorted and deduplicated",
			files: []FileChange{
				change(StatusAdded, "z_test.go"),
				change(StatusAdded, "a_test.go"),
				change(StatusAdded, "z_test.go"),
			},
			wantAdded: true,
			wantFiles: []string{"a_test.go", "z_test.go"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := TestsAdded(c.files)
			if got.Added != c.wantAdded {
				t.Fatalf("Added = %v, want %v (reason %q)", got.Added, c.wantAdded, got.Reason)
			}
			if !reflect.DeepEqual(got.Files, c.wantFiles) && !(len(got.Files) == 0 && len(c.wantFiles) == 0) {
				t.Fatalf("Files = %v, want %v", got.Files, c.wantFiles)
			}
			if got.Reason == "" {
				t.Fatalf("Reason is empty")
			}
			if c.reason != "" && !strings.Contains(got.Reason, c.reason) {
				t.Fatalf("Reason = %q, want it to contain %q", got.Reason, c.reason)
			}
			if strings.Contains(got.Reason, "\u2014") {
				t.Fatalf("Reason has an em dash: %q", got.Reason)
			}
		})
	}
}

func TestParseNameStatus(t *testing.T) {
	out := "A\x00new.go\x00M\x00mod.go\x00D\x00gone.go\x00R100\x00old.go\x00moved.go\x00T\x00typed.go\x00C075\x00src.go\x00copy.go\x00"
	got, err := parseNameStatus(out)
	if err != nil {
		t.Fatal(err)
	}
	want := []FileChange{
		{Path: "new.go", Status: StatusAdded},
		{Path: "mod.go", Status: StatusModified},
		{Path: "gone.go", Status: StatusDeleted},
		{Path: "moved.go", OldPath: "old.go", Status: StatusRenamed},
		{Path: "typed.go", Status: StatusModified},
		{Path: "copy.go", Status: StatusAdded},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v\nwant %#v", got, want)
	}
	if _, err := parseNameStatus("R100\x00only-one-path\x00"); err == nil {
		t.Fatal("expected an error for a truncated rename")
	}
	if _, err := parseNameStatus("Q\x00x\x00"); err == nil {
		t.Fatal("expected an error for an unknown status")
	}
}

func TestAddedLinesByFile(t *testing.T) {
	diff := "diff --git a/src/lib.rs b/src/lib.rs\n" +
		"--- a/src/lib.rs\n+++ b/src/lib.rs\n" +
		"@@ -1,2 +1,4 @@\n" +
		"-fn old() {}\n" +
		"+#[test]\n" +
		"+++ not a header inside a hunk\n" +
		"diff --git a/src/with space.rs b/src/with space.rs\n" +
		"new file mode 100644\n" +
		"--- /dev/null\n+++ b/src/with space.rs\t\n" +
		"@@ -0,0 +1 @@\n" +
		"+fn s() {}\n" +
		"diff --git \"a/src/caf\\303\\251.rs\" \"b/src/caf\\303\\251.rs\"\n" +
		"--- \"a/src/caf\\303\\251.rs\"\n+++ \"b/src/caf\\303\\251.rs\"\n" +
		"@@ -1 +1 @@\n" +
		"-x\n" +
		"+y\n" +
		"diff --git a/src/gone.rs b/src/gone.rs\n" +
		"deleted file mode 100644\n" +
		"--- a/src/gone.rs\n+++ /dev/null\n" +
		"@@ -1 +0,0 @@\n" +
		"-z\n"
	got := addedLinesByFile(diff)
	want := map[string][]string{
		"src/lib.rs":        {"#[test]", "++ not a header inside a hunk"},
		"src/with space.rs": {"fn s() {}"},
		"src/café.rs":       {"y"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestParseNumstat(t *testing.T) {
	out := "3\t1\tpkg/a.go\x00-\t-\timg.png\x000\t0\t\x00old_test.go\x00new_test.go\x0012\t0\tdir/with\ttab.go\x00"
	got, err := parseNumstat(out)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"pkg/a.go": 3, "img.png": -1, "new_test.go": 0, "dir/with\ttab.go": 12}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for _, bad := range []string{"x\x00", "a\tb\tc\x00", "1\t1\t\x00only-old\x00"} {
		if _, err := parseNumstat(bad); err == nil {
			t.Errorf("parseNumstat(%q): want an error", bad)
		}
	}
}

func TestCodeChanged(t *testing.T) {
	cases := []struct {
		name  string
		files []FileChange
		want  bool
	}{
		{"code", []FileChange{change(StatusModified, "pkg/a.go")}, true},
		{"deleted code", []FileChange{change(StatusDeleted, "pkg/a.go")}, true},
		{"tests only", []FileChange{change(StatusAdded, "pkg/a_test.go")}, false},
		{"test data only", []FileChange{change(StatusAdded, "pkg/testdata/in.txt")}, false},
		{"docs only", []FileChange{
			change(StatusModified, "README.md"), change(StatusAdded, "docs/design/x.md"),
			change(StatusAdded, "docs/img/a.png"), change(StatusModified, "LICENSE"), change(StatusModified, "pkg/NOTES.rst"),
		}, false},
		{"docs and code", []FileChange{change(StatusModified, "README.md"), change(StatusModified, "scripts/x.sh")}, true},
		{"requirements is code", []FileChange{change(StatusModified, "requirements.txt")}, true},
		{"nothing", nil, false},
	}
	for _, c := range cases {
		if got := TestsAdded(c.files).CodeChanged; got != c.want {
			t.Errorf("%s: CodeChanged = %v, want %v", c.name, got, c.want)
		}
	}
}

// --- end to end with real git ---

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid",
		"LC_ALL=C")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// rev returns the full commit ID of a branch.
func rev(t *testing.T, dir, name string) string {
	t.Helper()
	return gitRun(t, dir, "rev-parse", "--verify", name+"^{commit}")
}

// rng is the change from main to feature in dir.
func rng(t *testing.T, dir string) ChangeRange {
	t.Helper()
	return ChangeRange{RepoDir: dir, BaseSHA: rev(t, dir, "main"), HeadSHA: rev(t, dir, "feature")}
}

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commitAll(t *testing.T, dir, msg string) {
	t.Helper()
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", msg)
}

// newRepo makes a temp repo with a base commit on main and returns its dir.
func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q", "-b", "main")
	writeFile(t, dir, "pkg/a.go", "package pkg\n\nfunc A() int { return 1 }\n")
	writeFile(t, dir, "pkg/a_test.go", "package pkg\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {\n\tif A() != 1 {\n\t\tt.Fatal(\"bad\")\n\t}\n}\n")
	writeFile(t, dir, "pkg/old_test.go", "package pkg\n\nimport \"testing\"\n\nfunc TestOld(t *testing.T) {}\n")
	writeFile(t, dir, "src/lib.rs", "pub fn f() -> i32 { 1 }\n")
	commitAll(t, dir, "base")
	return dir
}

func TestEndToEndTestAdded(t *testing.T) {
	dir := newRepo(t)
	gitRun(t, dir, "checkout", "-q", "-b", "feature")
	writeFile(t, dir, "pkg/b.go", "package pkg\n\nfunc B() int { return 2 }\n")
	writeFile(t, dir, "pkg/b_test.go", "package pkg\n")
	commitAll(t, dir, "feature")

	res, err := DetectTestsAdded(context.Background(), rng(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Added || !reflect.DeepEqual(res.Files, []string{"pkg/b_test.go"}) {
		t.Fatalf("got %+v", res)
	}
}

func TestEndToEndNoTests(t *testing.T) {
	dir := newRepo(t)
	gitRun(t, dir, "checkout", "-q", "-b", "feature")
	writeFile(t, dir, "pkg/a.go", "package pkg\n\nfunc A() int { return 3 }\n")
	commitAll(t, dir, "feature")

	files, err := ChangedFiles(context.Background(), rng(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(files, []FileChange{{Path: "pkg/a.go", Status: StatusModified, Added: 1}}) {
		t.Fatalf("files = %#v", files)
	}
	if res := TestsAdded(files); res.Added {
		t.Fatalf("got %+v", res)
	}
}

// S11: a pure rename of a test adds no test.
func TestEndToEndPureRenameOfTestDoesNotCount(t *testing.T) {
	dir := newRepo(t)
	gitRun(t, dir, "checkout", "-q", "-b", "feature")
	gitRun(t, dir, "mv", "pkg/a_test.go", "pkg/renamed_test.go")
	commitAll(t, dir, "rename")

	files, err := ChangedFiles(context.Background(), rng(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	want := []FileChange{{Path: "pkg/renamed_test.go", OldPath: "pkg/a_test.go", Status: StatusRenamed}}
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("files = %#v, want %#v", files, want)
	}
	if res := TestsAdded(files); res.Added || res.CodeChanged {
		t.Fatalf("got %+v", res)
	}
}

func TestEndToEndRenameWithNewLinesCounts(t *testing.T) {
	dir := newRepo(t)
	gitRun(t, dir, "checkout", "-q", "-b", "feature")
	gitRun(t, dir, "mv", "pkg/a_test.go", "pkg/renamed_test.go")
	writeFile(t, dir, "pkg/renamed_test.go", "package pkg\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {\n\tif A() != 1 {\n\t\tt.Fatal(\"bad\")\n\t}\n}\n\nfunc TestMore(t *testing.T) {}\n")
	commitAll(t, dir, "rename and add")

	res, err := DetectTestsAdded(context.Background(), rng(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Added || !reflect.DeepEqual(res.Files, []string{"pkg/renamed_test.go"}) {
		t.Fatalf("got %+v", res)
	}
}

// S11: a mode change of a test adds no test.
func TestEndToEndChmodOfTestDoesNotCount(t *testing.T) {
	dir := newRepo(t)
	gitRun(t, dir, "checkout", "-q", "-b", "feature")
	gitRun(t, dir, "update-index", "--chmod=+x", "pkg/a_test.go")
	gitRun(t, dir, "commit", "-q", "-m", "chmod")

	res, err := DetectTestsAdded(context.Background(), rng(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	if res.Added || res.CodeChanged {
		t.Fatalf("got %+v", res)
	}
}

func TestEndToEndDocsOnlyIsNoCode(t *testing.T) {
	dir := newRepo(t)
	gitRun(t, dir, "checkout", "-q", "-b", "feature")
	writeFile(t, dir, "docs/guide.md", "# Guide\n")
	writeFile(t, dir, "README.md", "hello\n")
	commitAll(t, dir, "docs")

	res, err := DetectTestsAdded(context.Background(), rng(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	if res.Added || res.CodeChanged {
		t.Fatalf("got %+v", res)
	}
}

// One diff reads every Rust file, including odd names and a renamed file.
func TestEndToEndRustSeveralFiles(t *testing.T) {
	dir := newRepo(t)
	writeFile(t, dir, "src/old name.rs", "pub fn a() {}\npub fn b() {}\npub fn c() {}\npub fn d() {}\n")
	commitAll(t, dir, "more rust")
	gitRun(t, dir, "checkout", "-q", "-b", "feature")
	writeFile(t, dir, "src/café.rs", "#[tokio::test]\nasync fn t() {}\n")
	writeFile(t, dir, "src/plain.rs", "pub fn p() {}\n")
	gitRun(t, dir, "mv", "src/old name.rs", "src/new name.rs")
	writeFile(t, dir, "src/new name.rs", "pub fn a() {}\npub fn b() {}\npub fn c() {}\npub fn d() {}\n#[cfg(test)]\nmod tests {}\n")
	commitAll(t, dir, "rust")

	res, err := DetectTestsAdded(context.Background(), rng(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"src/café.rs", "src/new name.rs"}
	if !res.Added || !reflect.DeepEqual(res.Files, want) {
		t.Fatalf("got %+v, want files %v", res, want)
	}
}

func TestEndToEndDeletingTestsDoesNotCount(t *testing.T) {
	dir := newRepo(t)
	gitRun(t, dir, "checkout", "-q", "-b", "feature")
	gitRun(t, dir, "rm", "-q", "pkg/old_test.go")
	writeFile(t, dir, "pkg/a.go", "package pkg\n\nfunc A() int { return 4 }\n")
	commitAll(t, dir, "delete tests")

	res, err := DetectTestsAdded(context.Background(), rng(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	if res.Added || !strings.Contains(res.Reason, "only test files deleted") {
		t.Fatalf("got %+v", res)
	}
}

func TestEndToEndTestdataAlone(t *testing.T) {
	dir := newRepo(t)
	gitRun(t, dir, "checkout", "-q", "-b", "feature")
	writeFile(t, dir, "pkg/testdata/in.txt", "x\n")
	commitAll(t, dir, "data")

	res, err := DetectTestsAdded(context.Background(), rng(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	if res.Added || !strings.Contains(res.Reason, "only test data changed") {
		t.Fatalf("got %+v", res)
	}
}

func TestEndToEndRustInlineTest(t *testing.T) {
	dir := newRepo(t)
	gitRun(t, dir, "checkout", "-q", "-b", "feature")
	writeFile(t, dir, "src/lib.rs", "pub fn f() -> i32 { 1 }\n\n#[cfg(test)]\nmod tests {\n    #[test]\n    fn t() { assert_eq!(super::f(), 1); }\n}\n")
	commitAll(t, dir, "rust")

	res, err := DetectTestsAdded(context.Background(), rng(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Added || !reflect.DeepEqual(res.Files, []string{"src/lib.rs"}) {
		t.Fatalf("got %+v", res)
	}
}

func TestEndToEndRustWithoutTests(t *testing.T) {
	dir := newRepo(t)
	gitRun(t, dir, "checkout", "-q", "-b", "feature")
	writeFile(t, dir, "src/lib.rs", "pub fn f() -> i32 { 2 }\n")
	commitAll(t, dir, "rust")

	res, err := DetectTestsAdded(context.Background(), rng(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	if res.Added {
		t.Fatalf("got %+v", res)
	}
}

// The diff is taken from the merge base: work that landed on main after the
// branch started must not count as part of the change.
func TestChangedFilesUsesMergeBase(t *testing.T) {
	dir := newRepo(t)
	gitRun(t, dir, "checkout", "-q", "-b", "feature")
	writeFile(t, dir, "pkg/f.go", "package pkg\n")
	commitAll(t, dir, "feature")
	gitRun(t, dir, "checkout", "-q", "main")
	writeFile(t, dir, "pkg/m_test.go", "package pkg\n")
	commitAll(t, dir, "main moves on")

	files, err := ChangedFiles(context.Background(), rng(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(files, []FileChange{{Path: "pkg/f.go", Status: StatusAdded, Added: 1}}) {
		t.Fatalf("files = %#v", files)
	}
}

func TestChangedFilesOddPaths(t *testing.T) {
	dir := newRepo(t)
	gitRun(t, dir, "checkout", "-q", "-b", "feature")
	writeFile(t, dir, "pkg/with space_test.go", "package pkg\n")
	writeFile(t, dir, "pkg/café_test.go", "package pkg\n")
	commitAll(t, dir, "odd")

	res, err := DetectTestsAdded(context.Background(), rng(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"pkg/café_test.go", "pkg/with space_test.go"}
	if !res.Added || !reflect.DeepEqual(res.Files, want) {
		t.Fatalf("got %+v, want files %v", res, want)
	}
}

func TestChangedFilesErrors(t *testing.T) {
	dir := newRepo(t)
	ctx := context.Background()
	main := rev(t, dir, "main")
	r := func(base, head string) ChangeRange { return ChangeRange{RepoDir: dir, BaseSHA: base, HeadSHA: head} }
	if _, err := ChangedFiles(ctx, r(main, strings.Repeat("d", 40))); err == nil {
		t.Fatal("expected an error for an unknown commit")
	}
	out := filepath.Join(t.TempDir(), "out")
	if _, err := ChangedFiles(ctx, r("--output="+out, main)); err == nil {
		t.Fatal("expected an error for a ref that looks like an option")
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatal("git wrote the --output file")
	}
	for _, bad := range []string{"", "main", "HEAD", main[:12], strings.ToUpper(main)} {
		if _, err := ChangedFiles(ctx, r(bad, main)); err == nil {
			t.Fatalf("expected an error for base %q: only full commit IDs are taken", bad)
		}
		if _, err := ChangedFiles(ctx, r(main, bad)); err == nil {
			t.Fatalf("expected an error for head %q: only full commit IDs are taken", bad)
		}
	}
	if _, err := ChangedFiles(ctx, ChangeRange{BaseSHA: main, HeadSHA: main}); err == nil {
		t.Fatal("expected an error without RepoDir")
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := ChangedFiles(cctx, r(main, main)); err == nil {
		t.Fatal("expected an error for a cancelled context")
	}
}

func TestTestsAddedRefusesReplaceRef(t *testing.T) {
	dir := newRepo(t)
	gitRun(t, dir, "checkout", "-q", "-b", "feature")
	writeFile(t, dir, "pkg/x.go", "package pkg\n")
	commitAll(t, dir, "x")
	gitRun(t, dir, "checkout", "-q", "-b", "fake", "main")
	writeFile(t, dir, "pkg/x_test.go", "package pkg\n")
	commitAll(t, dir, "fake")
	gitRun(t, dir, "replace", rev(t, dir, "feature"), rev(t, dir, "fake"))

	if _, err := DetectTestsAdded(context.Background(), rng(t, dir)); !errors.Is(err, ErrAlteredHistory) {
		t.Fatalf("err = %v, want ErrAlteredHistory", err)
	}
}

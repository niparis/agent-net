package temppath

import "testing"

func TestUnderChecksWholeFolderNames(t *testing.T) {
	roots := []string{"/tmp", "/var/folders/ab/T"}
	cases := map[string]bool{
		"/tmp":                   true,
		"/tmp/tmpabc123":         true,
		"/tmp/a/b":               true,
		"/tmpfoo":                false,
		"/var/folders/ab/T":      true,
		"/var/folders/ab/T/x":    true,
		"/var/folders/ab/Tother": false,
		"/home/u/code":           false,
		"":                       false,
	}
	for path, want := range cases {
		if got := Under(path, roots); got != want {
			t.Errorf("Under(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestDefaultReadsTMPDIR(t *testing.T) {
	dir := "/custom-tmp-for-agent-net-test"
	t.Setenv("TMPDIR", dir)
	if !Under(dir+"/x", Default()) {
		t.Fatalf("TMPDIR %q is not in %v", dir, Default())
	}
}

func TestUnderIgnoresEmptyRoots(t *testing.T) {
	if Under("/tmp/x", []string{"", "."}) {
		t.Fatal("an empty root matched")
	}
}

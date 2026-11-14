package ctoskill

import (
	"flag"
	"testing"
)

var update = flag.Bool("update", false, "rewrite generated skill files")

// TestReferenceMatchesCode fails when a code list or default changed and
// reference.md was not regenerated. Run go generate ./internal/daemon/ctoskill
// (or this test with -update) to rewrite it.
func TestReferenceMatchesCode(t *testing.T) {
	if *update {
		if err := Generate(); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := File("reference.md")
	if err != nil {
		t.Fatal(err)
	}
	if want := Reference(); string(got) != want {
		t.Fatalf("reference.md is out of date; run go generate ./internal/daemon/ctoskill\n--- want\n%s", want)
	}
}

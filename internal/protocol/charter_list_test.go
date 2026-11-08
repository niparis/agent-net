package protocol

import (
	"reflect"
	"testing"
)

func TestSplitCharterListSplitsSemicolonsAndNewlines(t *testing.T) {
	got := SplitCharterList("a; b\nc;;\n d ")
	want := []string{"a", "b", "c", "d"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SplitCharterList = %q, want %q", got, want)
	}
}

func TestSplitCharterListOfEmptyValue(t *testing.T) {
	for _, value := range []string{"", "   ", ";\n; "} {
		if got := SplitCharterList(value); got != nil {
			t.Fatalf("SplitCharterList(%q) = %q, want nil", value, got)
		}
	}
}

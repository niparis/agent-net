package featuremap

import (
	"flag"
	"os"
	"testing"
)

var update = flag.Bool("update", false, "rewrite docs/feature-map.md")

// TestFeatureMapMatchesCode fails when a tab, panel, key, help text or tool
// changed and docs/feature-map.md was not regenerated.
func TestFeatureMapMatchesCode(t *testing.T) {
	want, err := Render()
	if err != nil {
		t.Fatal(err)
	}
	if *update {
		if err := Generate(); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(Path)
	if err != nil {
		t.Fatalf("%s: %v; run go generate ./internal/featuremap", Path, err)
	}
	if string(got) != want {
		t.Fatalf("docs/feature-map.md is out of date; run go generate ./internal/featuremap")
	}
}

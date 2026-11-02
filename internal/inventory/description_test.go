package inventory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillDescription(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"---\ndescription: >\n  hello\n  world\n---\nbody", "hello world"},
		{"---\ndescription: \"hello\\tworld\\u0001\"\n---", "hello world"},
		{"body", ""}, {"---\nname: demo\n---", ""},
		{"---\ndescription: " + strings.Repeat("word ", 300) + "\n---", strings.TrimSpace(strings.Repeat("word ", 205))},
	} {
		if got := skillDescription([]byte(c.in)); got != c.want {
			t.Fatalf("got %q want %q", got, c.want)
		}
	}
}

func TestSkillDescriptionWordBoundary(t *testing.T) {
	for _, text := range []string{strings.Repeat("x", 1025), strings.Repeat("x", 1024) + " tail"} {
		got := skillDescription([]byte("---\ndescription: " + text + "\n---"))
		want := ""
		if len(text) > 1025 {
			want = strings.Repeat("x", 1024)
		}
		if got != want {
			t.Fatalf("got %d characters, want %d", len(got), len(want))
		}
	}
}

func TestPiStandaloneDescription(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".pi", "agent")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "demo.md"), []byte("---\ndescription: Standalone skill\n---\nbody"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"skills":["demo.md"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	report := Scan(Options{Home: home})
	for _, item := range report.Items {
		if item.Harness == "pi" && item.Name == "demo" {
			if item.Description != "Standalone skill" || item.Manifest == nil {
				t.Fatalf("%+v", item)
			}
			return
		}
	}
	t.Fatal("missing standalone skill")
}

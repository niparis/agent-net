package harnesses

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// agy is Google's Antigravity CLI (decision 0049). Tests always use a fake
// binary: the real one needs the network and a sign-in.

func agyOptions(t *testing.T, dirs ...string) Options {
	t.Helper()
	return Options{PATH: dirs[0], Dirs: dirs[1:], LoginShellPATH: func(context.Context) string { return "" }}
}

func TestDetectAgyVersionModelsAndSignIn(t *testing.T) {
	root := t.TempDir()
	script(t, root, "agy", `case "$1" in
--version) echo 1.4.12 ;;
models) printf 'Fetching available models...\ngemini-3.8-flash-low\tGemini 3.8 Flash (low)\ngemini-4-argon-high\tGemini 4 Argon (high)\nclaude-sonnet-5-5-medium\tClaude Sonnet 5.5 (medium)\ngpt-oss-120b-medium\tGPT-OSS 120B (medium)\n' ;;
esac`)
	got := Detect(context.Background(), agyOptions(t, root))
	if len(got) != 1 || got[0].Name != "agy" {
		t.Fatalf("got %+v", got)
	}
	h := got[0]
	if h.Version != "1.4.12" {
		t.Errorf("version = %q", h.Version)
	}
	if h.SignedIn != "yes" || h.SignIn != "" {
		t.Errorf("sign-in = %q %q", h.SignedIn, h.SignIn)
	}
	want := []string{"claude-sonnet-5-5-medium", "gemini-3.8-flash-low", "gemini-4-argon-high", "gpt-oss-120b-medium"}
	if !reflect.DeepEqual(h.Models, want) {
		t.Errorf("models = %v, want %v", h.Models, want)
	}
	if len(h.ModelDefaultEfforts) != 0 {
		t.Errorf("agy carries the effort in the id, not in defaults: %v", h.ModelDefaultEfforts)
	}
}

func TestDetectAgySignedOut(t *testing.T) {
	root := t.TempDir()
	script(t, root, "agy", `case "$1" in
--version) echo 1.4.12 ;;
models) echo 'Error: not logged in. Run agy login first.' >&2; exit 1 ;;
esac`)
	got := Detect(context.Background(), agyOptions(t, root))
	if len(got) != 1 {
		t.Fatalf("got %+v", got)
	}
	if got[0].SignedIn != "no" || got[0].Models != nil {
		t.Fatalf("signed out: %+v", got[0])
	}
}

func TestDetectAgyTimeoutGivesUnknown(t *testing.T) {
	root := t.TempDir()
	script(t, root, "agy", `case "$1" in
--version) echo 1.4.12 ;;
models) /bin/sleep 2 ;;
esac`)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	got := Detect(ctx, agyOptions(t, root))
	if len(got) != 1 {
		t.Fatalf("got %+v", got)
	}
	if got[0].SignedIn != "" || got[0].Models != nil {
		t.Fatalf("timeout must be unknown: %+v", got[0])
	}
}

func TestDetectAgyIsOneEntry(t *testing.T) {
	root := t.TempDir()
	a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
	body := `case "$1" in --version) echo 1.4.12 ;; models) printf 'gemini-3.8-flash-low\tLow\n' ;; esac`
	script(t, a, "agy", body)
	script(t, b, "agy", body)
	got := Detect(context.Background(), agyOptions(t, a, b))
	if len(got) != 1 || got[0].Name != "agy" {
		t.Fatalf("one harness, one entry: %+v", got)
	}
	if len(got[0].Others) != 1 {
		t.Fatalf("the second copy belongs in Others: %+v", got[0])
	}
}

func TestParseAgyModels(t *testing.T) {
	out := "Fetching available models...\n" +
		"gemini-3.8-flash-low\tGemini 3.8 Flash (low)\n" +
		"  claude-sonnet-5-5-medium  \tClaude Sonnet 5.5 (medium)\n" +
		"gemini-3.8-flash-low\tagain\n" +
		"no tab here\n" +
		"\n"
	want := []string{"claude-sonnet-5-5-medium", "gemini-3.8-flash-low"}
	if got := ParseAgyModels(out); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if ParseAgyModels("Fetching available models...\nnot logged in\n") != nil {
		t.Fatal("want nil when no model is found")
	}
}

func TestAgyNeedsNoSignInCommand(t *testing.T) {
	// The model list is the sign-in check, so a fake without an auth command
	// still reports "yes" when it lists models.
	root := t.TempDir()
	path := script(t, root, "agy", `case "$1" in --version) echo 1 ;; models) printf 'gemini-4-argon-high\tHigh\n' ;; esac`)
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	got := Detect(context.Background(), agyOptions(t, root))
	if len(got) != 1 || got[0].SignedIn != "yes" {
		t.Fatalf("got %+v", got)
	}
}

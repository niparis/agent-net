package protocol

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseModelSelection(t *testing.T) {
	for _, h := range []string{"codex", "claude", "pi", "opencode", "agy"} {
		t.Run(h, func(t *testing.T) {
			old := "vendor/model:high"
			if h == "agy" {
				old = "vendor/model-high"
			}
			got := ParseModelSelection(h, "", old, "")
			want := ParseModelSelection(h, "vendor", "model", "high")
			if got != want {
				t.Fatalf("legacy %#v != separate %#v", got, want)
			}
		})
	}
}

func TestHarnessModelArguments(t *testing.T) {
	tests := []struct {
		h, p, m, e string
		want       []string
	}{
		{"codex", "openai", "gpt-a", "high", []string{"gpt-a:high"}},
		{"claude", "anthropic", "opus", "high", []string{"--model", "opus", "--effort", "high"}},
		{"agy", "google", "gemini-a", "high", []string{"--model", "gemini-a-high"}},
		{"opencode", "vendor", "model", "high", []string{"vendor/model", "high"}},
		{"pi", "vendor", "model", "high", []string{"--model", "vendor/model:high"}},
	}
	for _, tt := range tests {
		t.Run(tt.h, func(t *testing.T) {
			s := ParseModelSelection(tt.h, tt.p, tt.m, tt.e)
			got, err := s.Arguments([]string{"gemini-a-low", "gemini-a-high"})
			if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v, %v; want %v", got, err, tt.want)
			}
		})
	}
}

func TestAgyModelInvalidEffort(t *testing.T) {
	_, err := ParseModelSelection("agy", "", "gemini-a", "xhigh").Arguments([]string{"gemini-a-low", "gemini-a-high"})
	if err == nil || !strings.Contains(err.Error(), "high, low") {
		t.Fatalf("got %v", err)
	}
}

func TestWorkerProfileSeparateSelection(t *testing.T) {
	old := WorkerProfile{Harness: "agy", Model: "gemini-4-argon-high"}
	separate := WorkerProfile{Harness: "agy", Model: "gemini-4-argon", Effort: "high"}
	if old.Selection() != separate.Selection() {
		t.Fatalf("%+v != %+v", old.Selection(), separate.Selection())
	}
	if separate.Selection().ModelID() != "gemini-4-argon-high" {
		t.Fatal(separate.Selection())
	}
}

func TestAgyModelNameMaxIsNotEffort(t *testing.T) {
	got := ParseModelSelection("agy", "", "gpt-max", "")
	if got.Model != "gpt-max" || got.Effort != "" {
		t.Fatalf("got %+v", got)
	}
}

func TestAgyInventoryEffortBeyondKnownSuffixes(t *testing.T) {
	got := ParseModelSelection("agy", "", "gemini-a-ultra", "ultra")
	if got.Model != "gemini-a" {
		t.Fatalf("got %+v", got)
	}
	args, err := got.Arguments([]string{"gemini-a-ultra"})
	if err != nil || !reflect.DeepEqual(args, []string{"--model", "gemini-a-ultra"}) {
		t.Fatalf("%v, %v", args, err)
	}
}

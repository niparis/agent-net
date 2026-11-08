package protocol

import "testing"

func TestEmbeddedModelScoresReturnsACopy(t *testing.T) {
	a := EmbeddedModelScores()
	if len(a) == 0 {
		t.Fatal("no bundled scores")
	}
	want := a[0]
	a[0].Score = -1
	a[0].Match = "changed"
	b := EmbeddedModelScores()
	if b[0] != want {
		t.Fatalf("a caller changed the bundled table: %+v want %+v", b[0], want)
	}
}

func TestScoreNameCachedMatchesCompute(t *testing.T) {
	for _, in := range []string{"", "Opus", "sonnet", "anthropic/claude-sonnet-4.5", "gpt-5.1-codex:free", "claude-haiku-4-5-20251001", "openai/gpt-5@high", "x-free"} {
		for i := 0; i < 2; i++ {
			if got, want := scoreName(in), scoreNameCompute(in); got != want {
				t.Fatalf("scoreName(%q) = %q, want %q", in, got, want)
			}
		}
	}
}

func BenchmarkLookupModelScore(b *testing.B) {
	for i := 0; i < b.N; i++ {
		LookupModelScoreWithDefaultEffort("anthropic/claude-sonnet-4.5", "high", nil)
	}
}

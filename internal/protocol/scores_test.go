package protocol

import "testing"

func TestModelScore(t *testing.T) {
	tests := []struct {
		model     string
		overrides []ModelScore
		score     float64
		source    string
		ok        bool
	}{
		{"deepseek/deepseek-v4.1-flash:max", nil, 32.5, "artificialanalysis", true},
		{"deepseek/deepseek-v4.1-flash:high", nil, 0, "", false},
		{"unknown", nil, 0, "", false},
		{"deepseek/deepseek-v4.1-flash:high", []ModelScore{{Match: "deepseek-v4.1-flash", Score: 45, Source: "owner"}}, 45, "owner", true},
		{"p/model:high", []ModelScore{{Match: "model", Score: 20, Source: "owner"}, {Match: "p/model:high", Score: 50, Source: "owner"}}, 50, "owner", true},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			got, ok := LookupModelScore(tt.model, tt.overrides)
			if ok != tt.ok || got.Score != tt.score || got.Source != tt.source {
				t.Fatalf("got %+v, %v", got, ok)
			}
		})
	}
}

func TestDifficultyFit(t *testing.T) {
	for _, tt := range []struct {
		level string
		want  string
	}{
		{"trivial", "excellent"}, {"simple", "excellent"}, {"normal", "good"}, {"hard", "poor"}, {"very_complex", "denied"},
	} {
		t.Run(tt.level, func(t *testing.T) {
			got, err := DifficultyFit(32, tt.level, nil)
			if err != nil || got != tt.want {
				t.Fatalf("got %q, %v", got, err)
			}
		})
	}
}

func TestScoreNames(t *testing.T) {
	for _, tt := range []struct {
		model string
		score float64
		ok    bool
	}{
		{"openrouter/anthropic/claude-opus-5.5", 48.1, true}, {"claude-opus-5-5-free", 48.1, true}, {"claude-opus-5-5:free", 48.1, true}, {"claude-opus-5-5-0902", 48.1, true}, {"claude-opus-5-5:1017", 48.1, true},
		{"opus", 48.1, true}, {"sonnet", 30.3, true}, {"haiku", 24.6, true},
		{"gpt-6.1-sol-fast", 0, false}, {"gpt-6.1-sol-pro", 0, false}, {"gpt-6.1-sol-ultrafast", 0, false}, {"gpt-6.1-sol-flash", 0, false}, {"gpt-6.1-sol-lite", 0, false},
	} {
		t.Run(tt.model, func(t *testing.T) {
			got, ok := LookupModelScore(tt.model, nil)
			if ok != tt.ok || ok && got.Score != tt.score {
				t.Fatalf("got %+v, %v", got, ok)
			}
		})
	}
}

func TestLooseScorePatternAndVariants(t *testing.T) {
	overrides := []ModelScore{{Match: "terra:high", Score: 42, Source: "owner"}}
	for _, tt := range []struct {
		model string
		want  bool
	}{{"openai/gpt-6.1-terra:high", true}, {"openai/gpt-6.1-terra:low", false}, {"openai/gpt-6.1-terra-fast:high", false}} {
		got, ok := LookupModelScore(tt.model, overrides)
		if tt.want && (!ok || got.Score != 42) {
			t.Errorf("%s did not match override", tt.model)
		}
		if !tt.want && ok {
			got, _ := LookupModelScore(tt.model, overrides)
			if got.Score == 42 {
				t.Errorf("%s matched override", tt.model)
			}
		}
	}
}

func TestScoreEffort(t *testing.T) {
	for _, tt := range []struct {
		model string
		score float64
		ok    bool
	}{
		{"gpt-6.1-terra:high", 42.3, true}, {"gpt-6.1-terra:low", 29.2, true}, {"gpt-6.1-terra", 38.1, true}, {"gpt-6.1-terra:ultra", 0, false}, {"kimi-k3.1-pro", 46, true},
	} {
		t.Run(tt.model, func(t *testing.T) {
			got, ok := LookupModelScore(tt.model, nil)
			if ok != tt.ok || ok && got.Score != tt.score {
				t.Fatalf("got %+v, %v", got, ok)
			}
		})
	}
	if n := len(EmbeddedModelScores()); n != 85 {
		t.Fatalf("got %d scores", n)
	}
}

func TestScoreHarnessDefaultEffort(t *testing.T) {
	for _, tt := range []struct {
		effort string
		score  float64
		ok     bool
	}{{"high", 42.3, true}, {"", 38.1, true}, {"max", 51, true}, {"ultra", 0, false}} {
		got, ok := LookupModelScoreWithDefaultEffort("gpt-6.1-terra", tt.effort, nil)
		if ok != tt.ok || ok && got.Score != tt.score {
			t.Errorf("%s: %+v %v", tt.effort, got, ok)
		}
	}
}

func TestOwnerEffortOverrideUsesHarnessDefault(t *testing.T) {
	overrides := []ModelScore{{Match: "gpt-6.1-terra:high", Score: 60, Source: "owner"}}
	got, ok := LookupModelScoreWithDefaultEffort("gpt-6.1-terra", "high", overrides)
	if !ok || got.Score != 60 || got.Source != "owner" {
		t.Fatalf("got %+v %v", got, ok)
	}
}

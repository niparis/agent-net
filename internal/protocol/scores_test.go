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
		{"deepseek/deepseek-v4.1-flash", nil, 32, "owner", true},
		{"deepseek/deepseek-v4.1-flash:high", nil, 32, "owner", true},
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

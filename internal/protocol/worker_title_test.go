package protocol

import (
	"strings"
	"testing"
)

func TestValidWorkerTitle(t *testing.T) {
	tests := []struct {
		name    string
		title   string
		wantErr bool
	}{
		{"empty is allowed", "", false},
		{"short line", "Fix the parser", false},
		{"exactly 60 characters", strings.Repeat("a", 60), false},
		{"61 characters", strings.Repeat("a", 61), true},
		{"a newline", "first line\nsecond line", true},
		{"a carriage return", "first line\rsecond", true},
		{"only spaces", "   ", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidWorkerTitle(tt.title)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidWorkerTitle(%q) error = %v, want error %v", tt.title, err, tt.wantErr)
			}
		})
	}
}

func TestWorkerTitleFallback(t *testing.T) {
	tests := []struct {
		name  string
		title string
		task  string
		fall  string
		want  string
	}{
		{
			name: "setup sentences are skipped",
			task: "Repo: agent-net (Go). Base: branch merge-main. Read AGENTS.md first.\n\n" +
				"BUG (owner report): the Claude Code plugin that shows a session's workers uses the task line as the name.",
			fall: "agent-net worker 8",
			want: "BUG (owner report): the Claude Code plugin that shows a…",
		},
		{
			name: "setup lines are skipped",
			task: "Repo: shop\nBase: main\nRead docs/workers.md.\n\nAdd a title field to the worker start.",
			fall: "shop worker 3",
			want: "Add a title field to the worker start.",
		},
		{
			name: "sentences on one line are checked one by one",
			task: "Repo: shop. Base: main. Read AGENTS.md. Fix the parser crash.",
			fall: "shop worker 3",
			want: "Fix the parser crash.",
		},
		{
			name: "a brief that is only setup falls back to the worker name",
			task: "Repo: shop\nBase: main\nRead AGENTS.md first.",
			fall: "shop worker 8",
			want: "shop worker 8",
		},
		{
			name: "a long sentence is cut at a word boundary",
			task: "Implement the worker title fallback so every band row has a useful label",
			fall: "shop worker 4",
			want: "Implement the worker title fallback so every band row has a…",
		},
		{
			name:  "an explicit title wins",
			title: "Parser fix",
			task:  "Repo: shop\n\nFix the parser crash.",
			fall:  "shop worker 3",
			want:  "Parser fix",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := WorkerTitle(tt.title, tt.task, tt.fall); got != tt.want {
				t.Fatalf("WorkerTitle(%q, %q, %q) = %q, want %q", tt.title, tt.task, tt.fall, got, tt.want)
			}
		})
	}
}

package daemon

import (
	"strings"
	"testing"
)

func TestPolicyHumanClaudePrompt(t *testing.T) {
	tests := []struct {
		name, prompt string
		human        bool
	}{
		{"peer cross session", "Another Claude session sent a message:\n<cross-session-message from=peer>hello</cross-session-message>", false},
		{"peer agent", "\n Another Claude session sent a message:\n<agent-message from=peer>hello</agent-message>", false},
		{"background task", "\n <system-reminder>\n[SYSTEM NOTIFICATION - NOT USER INPUT]\n<task-notification>done</task-notification>", false},
		{"system notification", " [SYSTEM NOTIFICATION - NOT USER INPUT]\n<task-notification>done", false},
		{"embedded task", "notice:\n<task-notification>done", false},
		{"scheduled", " <scheduled>run", false},
		{"autonomous", " <<autonomous-loop", false},
		{"human", "Please fix this bug", true},
		{"late mention", strings.Repeat("a", 600) + "<task-notification>", true},
		{"empty", " \n", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isHumanClaudePrompt(tt.prompt); got != tt.human {
				t.Errorf("got %v, want %v", got, tt.human)
			}
		})
	}
}

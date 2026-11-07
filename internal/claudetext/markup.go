package claudetext

import (
	"regexp"
	"strings"
)

var reCommandTags = regexp.MustCompile(`(?s)</?(command|local-command)[^>]*>`)

// CleanCommandMarkup cleans Claude Code local-command markup (<command-name>,
// <command-args>, <command-message>, <local-command-stdout>, <local-command-stderr>).
// It returns the cleaned line, the updated command name (for linking stdout to
// its command), and whether the line is a command output (stdout or stderr).
func CleanCommandMarkup(text, lastCmd string) (string, string, bool) {
	trimmed := strings.TrimSpace(text)
	if strings.Contains(trimmed, "<command-name>") || strings.Contains(trimmed, "<command-message>") {
		cmdName := extractTag(trimmed, "command-name")
		if cmdName == "" {
			cmdName = extractTag(trimmed, "command-message")
			if cmdName != "" && !strings.HasPrefix(cmdName, "/") {
				cmdName = "/" + cmdName
			}
		}
		cmdArgs := strings.TrimSpace(extractTag(trimmed, "command-args"))
		cmd := cmdName
		if cmdArgs != "" {
			cmd = cmdName + " " + cmdArgs
		}
		if cmd == "" {
			cmd = strings.TrimSpace(reCommandTags.ReplaceAllString(trimmed, ""))
		}
		return strings.TrimSpace(cmd), cmdName, false
	}
	if strings.Contains(trimmed, "<local-command-stdout>") || strings.Contains(trimmed, "<local-command-stderr>") {
		out := strings.TrimSpace(reCommandTags.ReplaceAllString(trimmed, ""))
		if lastCmd != "" && !strings.HasPrefix(out, lastCmd+" → ") && !strings.Contains(out, " → ") {
			out = lastCmd + " → " + out
		}
		return out, lastCmd, true
	}
	if strings.Contains(trimmed, "<command-") || strings.Contains(trimmed, "<local-command-") {
		out := strings.TrimSpace(reCommandTags.ReplaceAllString(trimmed, ""))
		return out, lastCmd, false
	}
	return text, lastCmd, false
}

func extractTag(s, tag string) string {
	open := "<" + tag + ">"
	close := "</" + tag + ">"
	start := strings.Index(s, open)
	if start == -1 {
		return ""
	}
	start += len(open)
	end := strings.Index(s[start:], close)
	if end == -1 {
		rem := s[start:]
		if nextOpen := strings.Index(rem, "<"); nextOpen != -1 {
			return strings.TrimSpace(rem[:nextOpen])
		}
		return strings.TrimSpace(rem)
	}
	return strings.TrimSpace(s[start : start+end])
}

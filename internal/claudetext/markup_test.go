package claudetext

import "testing"

func TestCleanCommandMarkup(t *testing.T) {
	// Turn 1 command markup
	raw1 := "<command-name>/mcp</command-name>\n  <command-message>mcp</command-message>\n  <command-args></command-args>"
	text1, cmd1, isOutput1 := CleanCommandMarkup(raw1, "")
	if text1 != "/mcp" || cmd1 != "/mcp" || isOutput1 {
		t.Fatalf("turn 1: got text %q, cmd %q, isOutput %v; want %q, %q, false", text1, cmd1, isOutput1, "/mcp", "/mcp")
	}

	// Turn 1 command markup with args
	rawArgs := "<command-name>/mcp</command-name>\n  <command-message>mcp</command-message>\n  <command-args>status</command-args>"
	textArgs, cmdArgs, isOutputArgs := CleanCommandMarkup(rawArgs, "")
	if textArgs != "/mcp status" || cmdArgs != "/mcp" || isOutputArgs {
		t.Fatalf("turn 1 with args: got text %q; want %q", textArgs, "/mcp status")
	}

	// Turn 2 local command stdout
	raw2 := "<local-command-stdout>Failed to reconnect to agent-net-cto: CONNECTION_CLOSED"
	text2, cmd2, isOutput2 := CleanCommandMarkup(raw2, cmd1)
	want2 := "/mcp → Failed to reconnect to agent-net-cto: CONNECTION_CLOSED"
	if text2 != want2 || cmd2 != cmd1 || !isOutput2 {
		t.Fatalf("turn 2 stdout: got text %q, isOutput %v; want %q, true", text2, isOutput2, want2)
	}

	// Turn 2 local command stderr
	rawErr := "<local-command-stderr>connection refused</local-command-stderr>"
	textErr, _, isOutputErr := CleanCommandMarkup(rawErr, cmd1)
	wantErr := "/mcp → connection refused"
	if textErr != wantErr || !isOutputErr {
		t.Fatalf("turn 2 stderr: got text %q, isOutput %v; want %q, true", textErr, isOutputErr, wantErr)
	}

	// Stray command tags
	rawStray := "<command-foo>something</command-foo>"
	textStray, _, _ := CleanCommandMarkup(rawStray, "")
	if textStray != "something" {
		t.Fatalf("stray tags: got %q, want %q", textStray, "something")
	}
}

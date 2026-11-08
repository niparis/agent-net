package tui

import (
	tea "charm.land/bubbletea/v2"
	"encoding/json"
	"github.com/niparis/agent-net/internal/protocol"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAppConsentOneClick(t *testing.T) {
	for _, tc := range []struct{ key, answer string }{{"y", "approve"}, {"n", "refuse"}} {
		t.Run(tc.key, func(t *testing.T) {
			src := &ctoUndoSource{}
			m := newModel(80, 24)
			m.src = src
			m.tab = inboxTab
			m.asks = askItems([]protocol.InboxItem{{ID: "ask_fixture", Kind: "approval", Node: "apps", SourceRef: "app-consent:fixture", Status: "open", Text: "Approve Fixture (code ABC234)?", Options: []string{"approve", "refuse"}}})
			_, cmd := m.Update(tea.KeyPressMsg{Text: tc.key, Code: rune(tc.key[0])})
			if cmd == nil {
				t.Fatal("missing one-click command")
			}
			cmd()
			if len(src.methods) != 1 || src.methods[0] != "inbox_answer" {
				t.Fatalf("methods: %v", src.methods)
			}
			raw, _ := json.Marshal(src.params[0])
			var p struct{ Answer string }
			_ = json.Unmarshal(raw, &p)
			if p.Answer != tc.answer {
				t.Fatalf("answer: %s", raw)
			}
		})
	}
}

func TestCTOEnterUsesVisibleTUIHerdrContext(t *testing.T) {
	m, src := workerModel(100, 24)
	m.herdrSocket = "/tmp/fixture-herdr/herdr.sock"
	m.herdrPaneID = "pane-fixture"
	_, cmd := m.applyCTOUI(protocol.UIRequest{EnterSession: workerID})
	if cmd == nil {
		t.Fatal("enter was not requested")
	}
	cmd()
	if len(src.enters) != 1 || src.enters[0].SessionID != workerID || src.enters[0].HerdrSocket != m.herdrSocket || src.enters[0].HerdrPaneID != m.herdrPaneID {
		t.Fatalf("enter: %+v", src.enters)
	}
}

func TestConsentBrowserCommand(t *testing.T) {
	for _, platform := range []string{"linux", "darwin"} {
		name, args, err := consentBrowserCommand("https://login.tailscale.com/a/fixture", platform)
		if err != nil || len(args) != 1 || args[0] != "https://login.tailscale.com/a/fixture" {
			t.Fatalf("command: %s %v %v", name, args, err)
		}
	}
	for _, link := range []string{"file:///tmp/fixture", "https://user:password@example.test/", "http://example.test/"} {
		if _, _, err := consentBrowserCommand(link, "linux"); err == nil {
			t.Fatalf("unsafe link accepted: %s", link)
		}
	}
}

func TestPublicConsentOneClickOpensLinkWithoutClosingAsk(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("browser opener is for supported desktop platforms")
	}
	dir := t.TempDir()
	logPath := filepath.Join(dir, "opened")
	name := "xdg-open"
	if runtime.GOOS == "darwin" {
		name = "open"
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nprintf '%s\\n' \"$1\" > \"$CONSENT_OPEN_TEST_LOG\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("CONSENT_OPEN_TEST_LOG", logPath)
	src := &ctoUndoSource{}
	m := newModel(80, 24)
	m.src = src
	m.tab = inboxTab
	m.asks = askItems([]protocol.InboxItem{{ID: "ask_fixture", Kind: "question", Node: "apps", SourceRef: "apps-public-consent", Status: "open", Text: "Approve Tailscale login", Detail: "https://login.tailscale.com/a/fixture", Options: []string{"open link", "cancel"}}})
	_, cmd := m.Update(tea.KeyPressMsg{Text: "y", Code: 'y'})
	if cmd == nil {
		t.Fatal("missing browser command")
	}
	if result, ok := cmd().(actionMsg); !ok || result.err != nil {
		t.Fatalf("browser action: %+v", result)
	}
	opened, err := os.ReadFile(logPath)
	if err != nil || strings.TrimSpace(string(opened)) != "https://login.tailscale.com/a/fixture" {
		t.Fatalf("opened: %q %v", opened, err)
	}
	if len(src.methods) != 0 {
		t.Fatalf("opening the link answered the ask early: %v", src.methods)
	}
}

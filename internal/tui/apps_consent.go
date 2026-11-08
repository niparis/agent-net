package tui

import (
	"context"
	"fmt"
	"net/url"
	osexec "os/exec"
	"runtime"
	"strings"

	tea "charm.land/bubbletea/v2"
)

func consentBrowserCommand(link, platform string) (string, []string, error) {
	u, err := url.Parse(link)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return "", nil, fmt.Errorf("invalid consent link")
	}
	if platform == "darwin" {
		return "open", []string{link}, nil
	}
	return "xdg-open", []string{link}, nil
}

func openOwnerConsent(link string) tea.Cmd {
	return func() tea.Msg {
		cmd, args, err := consentBrowserCommand(strings.TrimSpace(link), runtime.GOOS)
		if err != nil {
			return actionMsg{err: err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), Timeout)
		defer cancel()
		err = osexec.CommandContext(ctx, cmd, args...).Run()
		if err != nil {
			return actionMsg{err: fmt.Errorf("open approval link: %w", err)}
		}
		return actionMsg{text: "Approval link opened. This ask closes after approval."}
	}
}

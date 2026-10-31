# Bubble Tea v2 migration follow-up

The v2 experiment is isolated from onboarding part 3. It was committed on
`agent-net/w_a89184e6d4db149f` as
`20b8e7190d24c0c93e1e2ba585ddf48ed800a9f8`.

The migration replaced `github.com/charmbracelet/bubbletea` and Bubbles v1 with
`charm.land/bubbletea/v2` and `charm.land/bubbles/v2`. It updated imports across
`internal/tui`, changed text inputs to v2 key messages and width setters, and
changed hidden cursors to `SetVirtualCursor(false)`. Upstream v2 returns a
`tea.View` instead of a string and uses key press, paste, and separate mouse
events. Program mouse mode and alternate-screen state also moved to the View.

`internal/tui/tea/tea.go` adapted the existing screen API to v2: it wrapped
string views, translated keys and mouse events, and mapped mouse-mode commands
and program options. This kept the screen models and their tests on the old API,
but added a custom compatibility layer that must be maintained.

Risks to review before scheduling the migration:

- Key and mouse translation must preserve modifiers, paste, wheel, and motion
  behavior across terminals.
- `internal/tui/enter.go` hands `internal/ptyproxy` to Bubble Tea's exec command.
  Verify v2 restores the terminal and hands it to the proxy correctly.
- `internal/ptyproxy` owns the interactive attach path and its escape handling;
  test that path with a real PTY under v2.
- The adapter can hide upstream API changes and should be removed if the screens
  migrate directly to v2.

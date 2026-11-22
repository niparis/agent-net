# Bubble Tea v2 Migration Plan and Visual Pass

**Date:** 2026-11-04  
**Status:** Design and planning document for review. No code changes in this pass.  
**Scope:** Architecture migration for the agent-net TUI from Bubble Tea v1 to v2, ptyproxy and Herdr terminal handover verification, and a comprehensive visual audit of all screens at 80 and 120 columns across dark and light terminals.

---

## 1. What Bubble Tea v2 Changes for agent-net

This assessment is verified directly against the upstream sources of `charm.land/bubbletea/v2` (v2.1.1), `charm.land/bubbles/v2` (v2.2.1), and `charm.land/lipgloss/v2` (v2.0.6), comparing them against the current implementation in `internal/tui`, `internal/ptyproxy`, and `cmd/agent-net`.

### 1.1 Core Architectural Shifts

1. **Renderer architecture (`cursed_renderer.go` and `charmbracelet/x/cellbuf`)**:
   - *v1*: Bubble Tea v1 rendered frames as raw ANSI strings with cursor repositioning escapes. Any frame rewrite required repainting lines, relying on basic line diffs or full screen repaints. High-frequency updates caused visible flicker, especially in rich ASCII/braille views.
   - *v2*: Bubble Tea v2 uses a virtual 2D terminal grid (`cellbuf.Buffer`). Views write to the grid, and the renderer performs exact cell-by-cell delta compression, emitting escape sequences only for cells whose rune, foreground, background, or attributes changed.
2. **Key handling and keyboard enhancements (`key.go`, `keyboard.go`)**:
   - *v1*: `tea.KeyMsg` was a single concrete struct (`Type KeyType`, `Runes []rune`, `Alt bool`, `Paste bool`). Disambiguation of modified keys (such as `Shift+Tab`, `Ctrl+C`, `Ctrl+U`, or `Alt+` combinations) was ad-hoc and brittle across different terminal emulators. Space returned `" "`.
   - *v2*: `tea.KeyMsg` is an interface. Key events are delivered as `tea.KeyPressMsg` and `tea.KeyReleaseMsg`. The underlying `tea.Key` struct contains `Code rune`, `Text string`, `Mod KeyMod`, `ShiftedCode rune`, `BaseCode rune`, and `IsRepeat bool`. Space bar returns `"space"` via `msg.String()`. Furthermore, v2 natively negotiates the Kitty keyboard protocol and `modifyOtherKeys`, providing robust key disambiguation on modern terminals.
3. **Declarative View-based state (`View() tea.View`)**:
   - *v1*: Terminal modes were toggled imperatively across multiple subsystems: options in `tea.NewProgram(..., tea.WithAltScreen())`, and commands in `Init()` or `Update()` (`tea.EnterAltScreen`, `tea.EnableMouseCellMotion`, `tea.DisableMouse`). Tracking whether mouse capture or alt-screen was active required out-of-band state management.
   - *v2*: `Model.View()` returns `tea.View` instead of `string`. Terminal modes are declared per render:
     ```go
     func (m Model) View() tea.View {
         v := tea.NewView(m.renderString())
         v.AltScreen = true
         if m.graphMouseActive() && !m.quitting {
             v.MouseMode = tea.MouseModeCellMotion
         }
         return v
     }
     ```
     Bubble Tea reconciles the terminal state against the returned `View` on each frame.
4. **Dedicated Bracketed Paste (`paste.go`)**:
   - *v1*: Pasted content arrived as a sequence of `KeyMsg` with `Paste: true`, or as raw runes that could accidentally trigger shortcut keystrokes if the terminal did not bracket the input.
   - *v2*: Pasted blocks arrive as a single atomic `tea.PasteMsg{Content: string}`, framed by `tea.PasteStartMsg` and `tea.PasteEndMsg`. This cleanly isolates pasted text from single-key shortcuts.
5. **Background colour detection without startup blocking (`color.go`)**:
   - *v1*: Importing `github.com/charmbracelet/bubbletea` ran an `init()` probe using `termenv` that queried terminal background via OSC 11 (`\x1b]11;?`). This caused severe regressions in non-TUI commands (`cmd/agent-net/terminal_startup_test.go`), leaked raw terminal responses into setup stdin (`internal/setup/onboarding.go`), and forced agent-net to vendor Bubble Tea in `third_party/bubbletea/tea_init.go` and add workarounds like `readPromptLine()`.
   - *v2*: No background query is executed at package initialization. Instead, the application sends an explicit command `tea.RequestBackgroundColor()` during `Model.Init()`. The terminal replies asynchronously with `tea.BackgroundColorMsg`, exposing `msg.IsDark() bool`. This completely eliminates startup hangs and escape sequence leaks in non-TUI commands.
6. **Lip Gloss v2 Colour Handling (`charm.land/lipgloss/v2`)**:
   - *v1*: Styles were stateful pointers tied to a global `*lipgloss.Renderer`. `lipgloss.Color` was a string type. Downsampling was coupled to renderer global variables (`lipgloss.SetColorProfile`).
   - *v2*: `lipgloss.Style` is an immutable value type, preventing concurrency race conditions during background event polling. `lipgloss.Color()` is a function returning standard library `image/color.Color`. Downsampling is performed automatically by Bubble Tea's cell buffer layer based on the detected terminal profile. Dynamic theme adaptation is achieved cleanly via `lipgloss.LightDark(isDark)` or `charm.land/lipgloss/v2/compat.AdaptiveColor`.

### 1.2 Which Benefits Matter for agent-net Screens

#### Benefits that Matter
1. **Clean startup and no OSC 11 leaks:** Eliminates the vendor fork in `third_party/bubbletea` and removes the skips in `terminal_startup_test.go`. Setup and daemon commands run cleanly without escape corruption on stdin.
2. **Flicker-free repaints:** agent-net polls daemon events once per second, updates live quotas, and animates braille traffic graphs. Cell-buffer delta rendering eliminates line jitter and tearing across all tabs.
3. **Atomic paste in forms:** Message composition (`m`), worker task prompts, project paths, and policy overrides can safely accept multi-line pastes without triggering single-letter shortcuts (such as `q` to quit or `x` to stop a worker).
4. **Reliable Shift+Tab in forms:** Multi-input dialogs (new session, worker form, policy form) can reliably support backward field navigation across all terminal emulators.
5. **Declarative mouse management:** The Graph tab captures mouse cell motion for node dragging and camera panning, but must disable mouse tracking when leaving the tab or quitting. Declaring `v.MouseMode` in `View()` eliminates state desynchronization bugs.

#### Benefits that Do Not Matter
1. **Key Release events (`tea.KeyReleaseMsg`):** agent-net is a management interface, not an arcade game. Key release tracking adds unnecessary message traffic and can be ignored.
2. **All-motion mouse tracking (`tea.MouseModeAllMotion`):** agent-net does not require hover effects or micro-cursor coordinates. Cell motion (`MouseModeCellMotion`) is sufficient for graph interactions.
3. **Window title manipulation via View (`view.WindowTitle`):** agent-net manages terminal titles specifically during session handovers in `internal/ptyproxy`. The main TUI title is kept clean to avoid overwriting user terminal tab configurations.
4. **Canvas 3D layers:** agent-net views are strictly 2D text layouts with unicode box borders.

---

## 2. Direct Migration Plan (No Adapter)

### 2.1 Why the Previous Experiment Failed

On branch `agent-net/w_a89184e6d4db149f` (commit `20b8e719`), an experiment attempted to migrate to Bubble Tea v2 using an adapter package (`internal/tui/tea/tea.go`). That approach was abandoned and reverted in commit `fa2b0e7c` because:
1. **Re-implementing v1 on top of v2:** The adapter defined its own `KeyMsg`, `KeyType`, `MouseMsg`, and translated back and forth. It recreated the old v1 API surface, preserving all legacy quirks rather than taking advantage of v2 improvements.
2. **Two competing type systems:** Upstream components like `charm.land/bubbles/v2/textinput` expect `v2.KeyPressMsg`. The adapter forced every text input update in `forms.go`, `cto.go`, `inbox.go`, and `policy.go` to invoke `k.BubbleTea()` conversions.
3. **Hidden API discrepancies:** The adapter masked how v2 actually delivers paste and window resize events, preventing the screens from reacting natively to v2 lifecycle messages.
4. **All-or-nothing blast radius:** Because the adapter sat between the engine and all 20+ screen files, any bug in key conversion or mouse mapping broke the entire TUI simultaneously.

### 2.2 Direct Migration Principles

- **No intermediate compatibility layer:** The codebase will import `charm.land/bubbletea/v2`, `charm.land/bubbles/v2`, and `charm.land/lipgloss/v2` directly.
- **De-coupled text rendering:** In agent-net, view rendering functions (`viewMain()`, `viewQuotas()`, `viewTools()`, etc.) already construct strings. The top-level `Model.View()` simply packages the resulting string into a `tea.View`. The individual view drawing functions require minimal signature changes.
- **Incremental, modular releases:** Migration proceeds across 4 focused releases. Each release must compile, pass `go vet`, pass package tests with `-race`, and regenerate affected golden scenes.

### 2.3 Step-by-Step Release Plan

```
+-------------------------------------------------------------------------------+
| Release 1: Theme & Styling Foundation                                        |
| - charm.land/lipgloss/v2 & colorprofile                                       |
| - internal/tui/theme.go semantic colors                                       |
| - Remove third_party/bubbletea vendor replacement                             |
+-------------------------------------------------------------------------------+
                                        |
                                        v
+-------------------------------------------------------------------------------+
| Release 2: Core Runtime & Form Inputs                                         |
| - charm.land/bubbletea/v2 & charm.land/bubbles/v2                             |
| - Model.View() returns tea.View; declarative alt-screen & mouse               |
| - textinput.Model upgraded across all forms                                   |
| - Base keyboard dispatch (KeyPressMsg, PasteMsg, "space")                     |
+-------------------------------------------------------------------------------+
                                        |
                                        v
+-------------------------------------------------------------------------------+
| Release 3: Screen Navigation & Tab Subsystems                                 |
| - Tabs 1 to 7 key handlers updated to native KeyPressMsg                      |
| - Graph tab mouse handling updated to MouseClickMsg / MouseMotionMsg          |
| - Asynchronous RequestBackgroundColor in Init()                               |
| - Fix top 10 visual and layout flaws identified in audit                      |
+-------------------------------------------------------------------------------+
                                        |
                                        v
+-------------------------------------------------------------------------------+
| Release 4: PTY Handover & Integration Verification                            |
| - tea.Exec integration with internal/ptyproxy and Herdr                       |
| - Real PTY test suite for Ctrl+] escape and resize behavior                   |
| - Full golden scene regeneration with scripts/screens.sh                      |
+-------------------------------------------------------------------------------+
```

#### Release 1: Theme & Styling Foundation
- **Scope:** Upgrade Lip Gloss to `charm.land/lipgloss/v2`.
- **Changes:**
  - Update `go.mod` to add `charm.land/lipgloss/v2`.
  - In `internal/tui/theme.go`, replace `lipgloss.TerminalColor` with `image/color.Color`.
  - Use `charm.land/lipgloss/v2/compat.AdaptiveColor` or `lipgloss.LightDark` for semantic color roles (`roleAccent`, `roleGood`, `roleWarning`, `roleError`, etc.).
  - Update `internal/tui/view.go` color blending helpers (`blendHex`).
  - Drop the `replace github.com/charmbracelet/bubbletea => ./third_party/bubbletea` directive in `go.mod`.
- **Validation:** Run `go test -race ./internal/setup/... ./internal/tui/theme_test.go`. Verify that `TestNonTUIStartupDoesNotQueryTerminalBackground` passes.

#### Release 2: Core Runtime & Form Inputs
- **Scope:** Switch core framework to `charm.land/bubbletea/v2` and `charm.land/bubbles/v2`.
- **Changes:**
  - Update `go.mod` to add `charm.land/bubbletea/v2` and `charm.land/bubbles/v2`.
  - Update `internal/tui/model.go`:
    - `Update(tea.Msg) (tea.Model, tea.Cmd)` handles `tea.KeyPressMsg`, `tea.PasteMsg`, `tea.WindowSizeMsg`.
    - Change `case " ":` to `case "space":`.
    - `View()` returns `tea.View`. Set `v.AltScreen = true`.
  - In `internal/tui/run.go`, invoke `v2.NewProgram(m, v2.WithContext(ctx))`.
  - In `internal/tui/forms.go`, `policy.go`, `inbox.go`, `cto.go`:
    - Update `textinput.New()` to bubbles v2.
    - Replace `Cursor.SetMode(cursor.CursorHide)` with `SetVirtualCursor(false)`.
    - Update form key handlers to forward `tea.KeyPressMsg`.
- **Validation:** Run `go test -race ./internal/tui -run 'Test(Forms|Policy|WorkerForm)'`.

#### Release 3: Screen Navigation & Tab Subsystems
- **Scope:** Update individual tab key handlers and mouse dispatch.
- **Changes:**
  - Sessions tab (`internal/tui/model.go`): Key dispatch for navigation, enter, session stop, filter input.
  - Graph tab (`internal/tui/graph_tab.go`): Translate `tea.MouseClickMsg`, `tea.MouseMotionMsg`, `tea.MouseWheelMsg`.
  - Quotas & Workers log (`internal/tui/worker_log.go`, `view_quotas.go`): Table scrolling, details toggle.
  - Messages tab (`internal/tui/conversations.go`, `feed.go`): Thread selection and message paging.
  - Tools & Skills (`internal/tui/tools.go`, `tools_agents.go`): Project selection and skill toggling.
  - Settings & Connected Apps (`internal/tui/apps.go`): App revocation and internet access toggle.
  - CTO & Inbox (`internal/tui/cto.go`, `inbox.go`): Proposal approval and decision keys.
  - In `Model.Init()`, return `tea.RequestBackgroundColor`. In `Update()`, handle `tea.BackgroundColorMsg` to configure dark/light contrast.
- **Validation:** Run all screen unit tests in `internal/tui`.

#### Release 4: PTY Handover, Goldens & Verification
- **Scope:** Terminal handover hardening, golden scenes, and regression suite.
- **Changes:**
  - Verify `internal/tui/enter.go` executes `proxy := ptyproxy.New(cmd)` using `tea.Exec`.
  - Run `scripts/screens.sh` to regenerate golden `.ansi` and `.png` files.
  - Confirm diffs against existing baselines.
- **Validation:** Execute full test suite for changed packages and run PTY integration tests.

---

## 3. Risks and Testing Strategy

### 3.1 Terminal Handover and the `ptyproxy` Enter Path

#### How the enter path works
When the owner selects a session and presses Enter, `internal/tui/enter.go` requests an enter plan from the daemon:
- If the session can open in Herdr, the daemon opens the pane or tab directly (`plan.OpenedHerdr = true`). The TUI remains active and reports the open in its status line.
- If Herdr is not running, or for remote sessions requiring `herdr --remote <host>`, or fallback SSH, the daemon returns an interactive command (`plan.Argv`).
- The TUI invokes `tea.Exec(proxy, doneFn)`, wrapping the command in `internal/ptyproxy.Proxy`.

#### Mechanics of `tea.Exec` in Bubble Tea v2
1. `p.releaseTerminal(false)` is invoked:
   - Cancels the active `cancelreader` and waits for the input loop goroutine to terminate.
   - Stops the cursed renderer flush ticker.
   - Restores the TTY to cooked mode via `term.Restore(ttyInput.Fd(), previousTtyInputState)`.
   - Leaves the alternate screen intact (it does not reset the buffer).
2. `proxy.Run()` takes control:
   - Writes `\x1b[22;0t\x1b]0;Title\a` to save the terminal title.
   - Sets the terminal into raw mode (`term.MakeRaw(f.Fd())`).
   - Starts the child process inside a real pseudo-terminal (`pty.Start(p.Cmd)`).
   - Monitors terminal resizing via `SIGWINCH` and reserves the bottom row for the agent-net status bar.
   - Forwards I/O between the real terminal and the PTY.
   - Intercepts `Ctrl+]` to break out and terminate the child session.
   - On exit, restores raw mode state and writes `\x1b[<u\x1b[>4;0m\x1b[23;0t` to disable Kitty keyboard flags and restore window title.
3. `p.RestoreTerminal()` resumes:
   - Puts terminal back into raw mode.
   - Reinitializes the cancel reader and input event loop.
   - Restarts the renderer, re-queries window size, pushes Kitty keyboard flags if applicable, and triggers a full screen repaint.

#### Failure Modes and Mitigations
- *Risk 1: Unconsumed terminal responses leaking into child.* If Bubble Tea sends terminal queries right before `Exec`, the replies could leak into the child process.
  *Mitigation:* `releaseTerminal()` in v2 flushes queued output before releasing raw mode. The test will verify that child processes receive clean stdin.
- *Risk 2: Terminal state desynchronization after Ctrl+].* If `ptyproxy` exits abruptly, the terminal might stay in an undefined state.
  *Mitigation:* Both `ptyproxy.Run()` and Bubble Tea's `RestoreTerminal()` execute deferred `term.Restore()` calls.
- *Risk 3: Window resize during child execution.* If the terminal is resized while attached to Claude or SSH, Bubble Tea must recognize the new dimensions upon return.
  *Mitigation:* `RestoreTerminal()` in v2 re-queries terminal dimensions via `term.GetSize()` and emits an immediate `WindowSizeMsg`.

#### Testing with Real PTYs
An automated integration test (`internal/tui/enter_pty_test.go`) must be added using `creack/pty`:
1. Launch `agent-net tui` inside a simulated PTY master/slave pair.
2. Send `enter` on a session configured for terminal fallback (`ptyproxy`).
3. Assert that the enter announcement appears ("agent-net: entering X. Press ctrl+] to come back here.").
4. Send text to the child process and assert echo.
5. Send `\x1d` (`Ctrl+]`) through the PTY.
6. Assert that `enterDoneMsg` is received, the TUI returns cleanly to the session table, and no stray escape codes appear.

### 3.2 Herdr Integration and Image Paste Integrity

#### The Rule (Decision 0046 & AGENTS.md)
"A session in herdr on another machine opens with `herdr --remote <host>` whenever this machine has herdr, even when the TUI runs in a plain terminal such as Ghostty. herdr's remote client is the only path that carries the owner's clipboard, so image paste depends on it. Never fall back to `ssh` while a local herdr exists."

#### Why Image Paste Depends on Herdr
Plain `ssh` does not forward local OS clipboard images to remote Claude Code or OpenCode sessions. `herdr --remote` runs an authenticated tunnel carrying OSC 52 and custom binary clipboard payloads.

#### Bubble Tea v2 Interaction
- While running the TUI, Bubble Tea v2 must not intercept or corrupt bracketed paste sequences intended for session inputs.
- When `enterCommand` hands over execution to `herdr --remote`, Bubble Tea v2 releases the TTY entirely. `herdr` has direct, unimpeded access to the terminal stream for clipboard forwarding.
- Regression tests in `internal/daemon/enter_herdr_test.go` and `internal/tui/enter_test.go` must run under the v2 dependencies to verify that `herdr --remote` remains the preferred plan over `ssh`.

### 3.3 Key and Mouse Translation Across Terminals

#### Tested Terminal Matrix
- **Ghostty & Kitty:** Full Kitty keyboard protocol (`ansi.PushKittyKeyboard`), unambiguous modifiers, bracketed paste.
- **iTerm2 & Alacritty:** `modifyOtherKeys` mode 2, CSI u sequences, standard xterm mouse.
- **Apple Terminal (Terminal.app):** Legacy escape sequences, standard ANSI mouse, basic function keys.
- **tmux & herdr multiplexers:** Pass-through escape translation, `\x1b[Z` for `Shift+Tab`.

#### Test Strategy
Implement table-driven test cases in `internal/tui/keys_test.go` sending raw byte streams to the Bubble Tea v2 input reader:
- Shift+Tab (`\x1b[Z`, `\x1b[1;2Z`, `\x1b[27;2;9~`) -> verifies navigation moves to previous form field.
- Space (` `) -> verifies `msg.String() == "space"` cycles selection rules and does not get missed.
- Fast pasted strings (`\x1b[200~line1\nline2\x1b[201~`) -> verifies delivery as `tea.PasteMsg` without triggering action hotkeys.

---

## 4. Visual Pass (80 and 120 Columns, Dark and Light)

A full visual audit was performed across all 157 golden scenes generated by `scripts/screens.sh` at both 80x24 and 120x34/40 resolutions, stored for review in `/tmp/agent-net-screens/`.

### 4.1 Top 10 Concrete Visual Problems

#### Problem 1: Workers Log Cuts Task Titles at 80 Columns
- **Screen:** `Workers log` (`workers_log_80x40.ansi`)
- **Cause:** Fixed column allocations give 11 cols to Date (`11-14 22:13`), 18 cols to Model (`test-model:high     `), 10 cols to Difficulty (`normal    `), 10 cols to Caller (`Agent One  `), and 9 cols to Status (`running  `). In an 80-column terminal, the task title is squeezed into just 12 columns, truncating all titles to ~9 characters (e.g. `Review wo…`).
- **Fix:** At widths under 100 columns:
  1. Trim Date to time only `22:13` (saves 6 cols).
  2. Shorten Model to base name or 10 chars max (saves 8 cols). Show effort (`:h`) or move effort to detail pane.
  3. Abbreviate Difficulty to 4 chars (`norm`, `hard`, `simp`) (saves 6 cols).
  4. This reclaims 20 columns for the task title, allowing 32 visible characters.
- **Mockup (80 Columns):**
  *Before:*
  ```
  │   11-14 22:13  test-model:high      normal     owner      Review wo… running │
  │ ▸ 11-14 22:11  test-model:high      normal     Agent One  Review worke… done │
  │   11-14 22:10  test-model:high      normal     CTO        Review wo… blocked │
  ```
  *After:*
  ```
  │   22:13  test-model  norm  owner      Review worker history and… running │
  │ ▸ 22:11  test-model  norm  Agent One  Review worker history and…    done │
  │   22:10  test-model  norm  CTO        Review worker history and… blocked │
  ```

#### Problem 2: Sessions Table Drops Essential Context Columns at 80 Columns
- **Screen:** `Sessions table` (`sessions_active_80x24.ansi`)
- **Cause:** At 80 columns, the table prioritizes `STATUS`, `TRUST`, `RULE`, `DLG`, `CPU`, `RAM`, and drops `AGENT`, `PROJECT`, `MODEL`, and `TASK`. The user cannot see what project or harness a session belongs to. Furthermore, the column header `DLG` (Delegation Policy) is cryptic.
- **Fix:** At 80 columns, hide `TRUST` and `RULE` (which almost always hold default `-` and `normal`), replace `DLG` with `POL` (or omit when unset), and show `PROJECT` (10 cols) and `AGENT` (7 cols).
- **Mockup (80 Columns):**
  *Before:*
  ```
    NAME               STATUS TRUST RULE   DLG        CPU   RAM  view: active  (v)
   Mac (here) 3 sessions ─────────────────────────────────────────────────────────
  ▸ tenebrae-53        busy   -     normal -          38%  1.2G
    strategies         idle   -     normal -         240%  4.6G
  ```
  *After:*
  ```
    NAME         AGENT   PROJECT     STATUS    CPU   RAM  TASK        view: active
   Mac (here) 3 sessions ─────────────────────────────────────────────────────────
  ▸ tenebrae-53  claude  tenebrae    busy      38%  1.2G  deploy…
    strategies   claude  strategies  idle     240%  4.6G  backtest…
  ```

#### Problem 3: Top Tab Bar Runs Into Counter on Narrow Terminals
- **Screen:** Top bar across all tabs (`view_main.go:422`, `cto_box_proposals_80x24.ansi`)
- **Cause:** When the Inbox has pending items (e.g. `7 Inbox (2)`), the left tab bar width reaches 69 columns. Adding `" │ 12 online"` (12 cols) equals 81 columns. Because 81 > 80, line 422 falls back to dropping the vertical pipe: `return clip(left+" "+short, w)`. This produces `Inbox (2) 12 online` with no visual delimiter.
- **Fix:** When width <= 80 and the Inbox count is non-zero, shorten tab names dynamically: use `1 Sess 2 Graph 3 Quota 4 Msg 5 Tools 6 Set 7 Inbox (2) │ 12 on`, ensuring the `│` separator is never removed.
- **Mockup (80 Columns):**
  *Before:*
  ```
  1 Sessions 2 Graph 3 Quotas 4 Messages 5 Tools 6 Settings 7 Inbox (2) 12 online
  ```
  *After:*
  ```
  1 Sess 2 Graph 3 Quota 4 Msg 5 Tools 6 Set 7 Inbox (2) │ 12 online
  ```

#### Problem 4: Settings Tab URL Overflow and Clipping
- **Screen:** `Settings` (`settings_80x24.ansi`)
- **Cause:** Line 7 of Settings tries to display the toggle state, the phone URL, and the bots URL on a single line:
  `▸ Internet access: on   phone https://example.test:8443/   bots https://example…`
  The bots URL is truncated with an ellipsis.
- **Fix:** At terminal widths under 100 columns, format the URLs as indented items below the toggle setting.
- **Mockup (80 Columns):**
  *Before:*
  ```
  ▸ Internet access: on   phone https://example.test:8443/   bots https://example…
    Add an app
  ```
  *After:*
  ```
  ▸ Internet access: on
      phone  https://example.test:8443/
      bots   https://example.test:8443/mcp
    Add an app
  ```

#### Problem 5: Rigid Split Truncates Projects While Skills Pane is Empty
- **Screen:** `Skills per project` (`tools_agents_80x24.ansi`)
- **Cause:** The screen uses a fixed 19-column width for the Projects list on the left and 57 columns for the Skills pane on the right. Project names like `flood-warning` and `homelab-prod` are chopped to `flood-war…` and `homelab-p…`, while the Skills pane has over 35 columns of empty trailing space.
- **Fix:** At 80 columns, adjust the split to 26 columns for Projects and 50 columns for Skills.
- **Mockup (80 Columns):**
  *Before:*
  ```
  ╭─Projects─────────╮╭─Skills · homelab─────────────────────────────────────────╮
  │   flood-war…   1 ││   [x] archify                         ~120               │
  │ ▸ homelab      2 ││   [x] estate-runbook                  ~160               │
  │   homelab-p…   1 ││                                                          │
  ```
  *After:*
  ```
  ╭─Projects──────────────╮╭─Skills · homelab─────────────────────────────────────╮
  │   flood-warning     1 ││   [x] archify                         ~120           │
  │ ▸ homelab           2 ││   [x] estate-runbook                  ~160           │
  │   homelab-prod      1 ││                                                      │
  ```

#### Problem 6: Vertical Void in Delivery Details on 80x24 Terminals
- **Screen:** `Delivery Detail` (`delivery_detail_failed_80x24.ansi`)
- **Cause:** The Evidence panel has a static height calculation that leaves 5 completely empty blank lines between the Evidence box and the bottom CTO box on standard 24-row displays.
- **Fix:** Allow the Evidence box to expand to consume available vertical space, or display step execution logs/reasons in the bottom section instead of leaving blank terminal lines.
- **Mockup (80x24):**
  *Before:*
  ```
  ╰─Evidence─────────────────────────────────────────────────────────────────────╯





  ╭─CTO──────────────────────────────────────────────────────────────────────────╮
  ```
  *After:*
  ```
  │ step: review · failed: findings_open=2 independent=yes                       │
  │ merger review on c41de0a rejected breaking changes to security policy.       │
  ╰──────────────────────────────────────────────────────────────────────────────╯
  ╭─CTO──────────────────────────────────────────────────────────────────────────╮
  ```

#### Problem 7: Quotas Subscriptions Columns Steal Space from Progress Bars
- **Screen:** `Quotas` (`quotas_80x24.ansi`)
- **Cause:** The Subscriptions table displays separate columns for `MAC` (`live`) and `OTHER` (`-`). Even when empty, these columns consume 16 characters. This forces the usage bar (`█████─────  50%`) and `PACE` column into narrow boundaries where reset times are cramped.
- **Fix:** Combine machine location into a compact badge beside the provider name (`claude (here)`), remove empty `OTHER` columns, and shorten the progress bar to 8 cells at 80 columns.
- **Mockup (80 Columns):**
  *Before:*
  ```
  │ PROVIDER PLAN MAC     OTHER   WINDOW LEFT                 PACE      RESETS   │
  │ > claude max  live    -       5h     █████─────  50%      on pace   -        │
  ```
  *After:*
  ```
  │ PROVIDER       PLAN  WINDOW  USAGE       PACE     RESETS                     │
  │ > claude (Mac) max   5h      ████░ 50%   on pace  -                          │
  ```

#### Problem 8: Canvas Squeezed Out by Legend and Empty CTO Box in Graph
- **Screen:** `Graph tab` (`graph_80x24.ansi`)
- **Cause:** On an 80x24 display, the graph canvas is squeezed down to only 11 vertical rows because:
  - Top tab bar: 2 lines
  - Bottom legend and status: 4 lines
  - CTO Box: 4 lines (mostly empty)
  - Key help footer: 1 line
  Edges, node labels, and braille traffic markers collide and become illegible.
- **Fix:** On 24-row terminals, collapse the 4-line legend into 1 concise line (`● charter · ○ open · gold: approved · pink: unverified`), and hide the CTO box on the Graph tab unless an active CTO proposal directly concerns a graph node or edge.
- **Mockup (80x24):**
  *Before:*
  ```
    project-01 → project-02 request  ·  project-04 → project-05 reply
    ● charter  ·  ○ no charter  ·  gold: your words  ·  pink: talked, no rule set
    selected: project-03 · component · 3 edges
    left/right select  +/- zoom  m traffic  enter card  ? help
  ╭─CTO──────────────────────────────────────────────────────────────────────────╮
  │                                                                              │
  │ c: open the CTO                                                              │
  ╰──────────────────────────────────────────────────────────────────────────────╯
  ```
  *After:*
  ```
  ● charter · ○ open · gold/pink status · sel: project-03 (3 edges)
  ←/→ select · +/- zoom · m traffic · enter card · c CTO · ? help
  ```
  *(Reclaims 6 vertical rows, expanding canvas from 11 to 17 rows).*

#### Problem 9: Message Thread Previews Truncated in Messages Tab
- **Screen:** `Messages tab` (`messages_80x24.ansi`)
- **Cause:** In the thread list, `tenebrae-53 ↔ ended session` consumes 31 chars, and `1 message · 1m ago` consumes 20 chars. The message preview snippet is allotted only 23 chars (`Which environment should…`).
- **Fix:** Truncate peer session names at 14 chars, format counts compactly (`1 msg · 1m`), giving 38+ chars to the preview snippet.
- **Mockup (80 Columns):**
  *Before:*
  ```
  │   tenebrae-53 ↔ ended session · Which environment should… 1 message · 1m ago │
  ```
  *After:*
  ```
  │   tenebrae-53 ↔ ended-sess · Which environment should we target? · 1m · 1 msg│
  ```

#### Problem 10: Repeated Headers and Box Borders in Quality Details
- **Screen:** `Quality Analysis` (`quality_details_80x24.ansi`)
- **Cause:** Each task difficulty tier (`simple`, `normal`, `hard`, `very complex`) renders its own full border box and duplicates the entire 6-column header (`MODEL MY TIME / TASK THIS WEEK NEEDED HELP LATE BUGS RATING`). On 24-row terminals, the fourth tier (`very complex`) is pushed completely off screen, showing only its top border.
- **Fix:** Render a single unified table with section separator rows for the tiers rather than separate bordered boxes for each tier.
- **Mockup (80 Columns):**
  *Before:*
  ```
  ╭─simple · 9 tasks─────────────────────────────────────────────────────────────╮
  │ MODEL         MY TIME / TASK  THIS WEEK  NEEDED HELP     LATE BUGS  RATING   │
  │   luna           1 min █████          3           0%             -       -   │
  ╰──────────────────────────────────────────────────────────────────────────────╯
  ╭─normal · 196 tasks · picker: ▸ sol (codex 5% ahead of pace)──────────────────╮
  │ MODEL         MY TIME / TASK  THIS WEEK  NEEDED HELP     LATE BUGS  RATING   │
  ```
  *After:*
  ```
  MODEL            MY TIME/TASK  THIS WEEK  HELP    LATE BUGS  RATING
  ─── simple (9 tasks) ──────────────────────────────────────────────
    luna            1m   █████           3    0%            -       -
  ─── normal (196 tasks · picker: sol) ──────────────────────────────
    deepseek        8m   ████░          25    0%            -       -
    terra           9m   █████          18   35%   1 (sev 3)      4.5
  ─── hard (42 tasks) ───────────────────────────────────────────────
  ```

### 4.2 Light and Dark Terminal Considerations

In Bubble Tea v1, `lipgloss.HasDarkBackground()` frequently failed or defaulted to dark mode on light terminals (or was circumvented in tests with `lipgloss.SetHasDarkBackground(true)`). When running on light background terminals (e.g. standard macOS Terminal or white-theme Ghostty):
1. **Low Contrast Text:** Colors such as `roleText` (`#d7dce5` in dark mode) become unreadable against white backgrounds (`#ffffff`).
2. **Subtle Selection Borders:** In dark mode, `roleSelection` is `#22303a`. In light mode, it must be `#e6f1f2` with a dark contrasting foreground (`#11151c`).
3. **Graph Braille Pixels:** Braille canvas points use faint gray ramps (`#454c58` vs `#a7adb8`). In light mode, unselected nodes must use darker grays (`#596171`) to maintain readability.

**Solution with v2:**
- Use `tea.RequestBackgroundColor` on startup.
- In `internal/tui/theme.go`, define a clean `Theme` structure parameterized by `isDark bool`:
  ```go
  type Theme struct {
      IsDark    bool
      Text      color.Color
      Base      color.Color
      Accent    color.Color
      Selection color.Color
      Dim       color.Color
      Faint     color.Color
      Good      color.Color
      Warning   color.Color
      Error     color.Color
      CTO       color.Color
  }
  ```
- All screen views reference the active theme instance stored on `Model`. When `BackgroundColorMsg` arrives, `m.theme = NewTheme(msg.IsDark())` updates all components instantly without restarting the TUI.

---

## 5. Effort, Order, and Recommendation

### 5.1 Effort Estimate by Phase

| Phase | Description | Scope | Risk | Estimated Effort |
|---|---|---|---|---|
| **Phase 1** | Lip Gloss v2 & Theme Modernization | `internal/tui/theme.go`, `view.go`, `color.go` | Low | 1.5 days |
| **Phase 2** | Bubble Tea v2 Core & Input Forms | `model.go`, `run.go`, `forms.go`, `bubbles/v2` | Medium | 2.5 days |
| **Phase 3** | Screen Key Handlers & Layout Polish | 7 main tabs, top 10 visual fixes | Medium | 3.0 days |
| **Phase 4** | PTY Handover, Herdr Tests & Goldens | `enter.go`, `ptyproxy`, golden tests | Low | 1.5 days |
| **Total** | | | | **8.5 days** |

### 5.2 Recommended First Release

**Recommended First Milestone: Release A (Theme Modernization & Safe Background Detection)**
- Upgrade `go.mod` to `charm.land/lipgloss/v2`.
- Update `internal/tui/theme.go` to use `image/color.Color` and `compat.AdaptiveColor`.
- Remove the Bubble Tea fork replacement in `go.mod` (`replace github.com/charmbracelet/bubbletea => ./third_party/bubbletea`) and delete `third_party/bubbletea`.
- Implement `TestNonTUIStartupDoesNotQueryTerminalBackground` without skips.
- Fix Problem 3 (top bar tab truncation `Inbox (2) 12 online`) and Problem 1 (workers log column layout).
- **Why this first:** It decouples styling from runtime, resolves the non-TUI terminal background query issue, fixes the two most glaring visual bugs, and requires zero changes to the underlying event loop. It delivers immediate user value and prepares the codebase for the v2 engine cutover.

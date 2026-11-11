# 0132 - Sign-in asks open a CLI login in herdr

**Date:** 2026-11-11
**Status:** Accepted.

## Context

The owner's standing order is that his logins reach him as a one-click prompt,
never as a command to type. The first case is quota-axi on research and on his
Mac reporting that the Alibaba Bailian CLI has no console access token. Its
remedy is `bl auth login --console`, an interactive command that needs a
terminal on the machine that holds the login.

The Desk already carries asks from many sources (decision 0090). herdr is how
the owner reaches terminals on every machine, and a session on another machine
opens with `herdr --remote` (decision 0046).

## Decision

A `sign_in` ask names a machine, a short CLI label and the reason. Its provider
key is stored in the ask's source ref. The command comes only from the fixed
allowlist in `internal/protocol/signin.go`, keyed by provider. For now the
table has one entry: `alibaba` runs `bl auth login --console` for the Alibaba
Bailian CLI. No agent, CTO call or frame can supply an argv. An unknown
provider is refused.

The machine's daemon raises one ask per provider when quota-axi reports a
provider error whose remedy is a known login. A repeat refreshes that one ask,
and the ask is not raised again after a dismissal until the error clears. The
CTO may raise an ask for a known provider on a machine with `sign_in_raise`,
and list them with `sign_in_list`. Raising grants nothing.

Only the owner's click opens the pane. The TUI Desk, the web console and the
owner's operator path call `sign_in_open`, which is not in the CTO allowlist.
The daemon opens a herdr pane on the machine the ask names, titled
`sign in: <label>`, and runs the allowlisted argv. On another machine the
target daemon opens its own pane and this machine shows it with
`herdr --remote <target>`. There is no ssh fallback. A target without herdr
answers with a note.

After the pane closes, that machine runs the provider's quota check once. When
the login error is gone, the ask closes itself with `signed in`. A persisting
error keeps the ask open and does not raise a second one.

This adds no setting and no approval gate. The allowlist is the security
boundary: it is the only way a command is chosen.
# 0058 - Its own Tailscale name for connected apps

**Date:** 2026-11-04
**Status:** Accepted. Built behind a setup choice. The default is still the
machine's own Tailscale node, until the owner switches.

## Context

Connected apps (the Grok bot and the phone) reach the gateway through Tailscale
Funnel (v0.4.81). The gateway runs `tailscale funnel` on the machine's own
Tailscale node. So:

- The public name is the machine's name, for example `gw.example.ts.net`. Every
  other Serve or Funnel route on that machine shares the name. Those services
  get the phone sign-in cookie, and the TUI has to warn about them.
- agent-net shares one Serve and Funnel config with the other services. It must
  pick a free port (443, 8443, then 10000) and must check ownership before it
  removes a route.

## Options

**(a) tsnet.** The gateway runs its own Tailscale node in its process with the
`tailscale.com/tsnet` package. The node has its own name, for example
`agent-net.example.ts.net`, and its own Funnel on port 443. It needs one login:
an auth key from a file, or a login link the owner opens once.

**(b) A dedicated port or path on the machine's node.** This is what we have
now, with a fixed port or a path prefix. The name is still shared, so the
cookie and the collisions stay. It does not solve the problem.

**(c) A second tailscaled on the machine.** Not acceptable: it needs root, a
second system service and a second TUN device.

## Checks for (a)

- **Pure Go.** `tailscale.com` v1.104.5 builds with `CGO_ENABLED=0` for
  linux/amd64, linux/arm64, darwin/amd64 and darwin/arm64. tsnet uses a
  user-space network stack (gVisor netstack), so it needs no root and no TUN
  device.
- **Go version.** The module needs Go 1.27.3, so `go.mod` moves from 1.27 to
  1.27.3. CI reads the version from `go.mod`.
- **Binary size.** The release binary grows by about 26 MB, about 45%
  (`CGO_ENABLED=0 go build -trimpath`, sizes in bytes):

| Target | Before | After | Change |
|---|---|---|---|
| linux/amd64 | 60,144,284 | 87,394,799 | +27,250,515 |
| linux/arm64 | 56,816,146 | 82,503,564 | +25,687,418 |
| darwin/amd64 | 60,697,184 | 87,459,872 | +26,762,688 |
| darwin/arm64 | 57,907,858 | 83,358,434 | +25,450,576 |

  Tailscale's `ts_omit_*` build tags can leave features out. With about 50 of
  them (SSH, Taildrop, exit nodes, kube and more) linux/amd64 is 83,272,058
  bytes, only 4 MB less. That saving does not pay for a release build with
  tags that could switch off a feature tsnet needs, so the release build stays
  as it is.
- **Other modules.** `tailscale.com`'s own `go.mod` raises some shared modules,
  among them `golang.org/x/net`, `golang.org/x/term`, `github.com/mattn/go-runewidth`
  and `github.com/charmbracelet/x/ansi`. The TUI tests cover the width
  libraries.

## Decision

Build (a), behind a setup choice. It is the only option that gives agent-net
a name of its own: (b) keeps the shared name, and (c) needs root. (a) stays
pure Go and keeps the four release builds. The cost is the binary size above.

- `agent-net setup --gateway --apps-tailscale-name agent-net` saves the choice
  in `tailscale-node.json` in the gateway data folder and restarts the gateway.
  `--apps-tailscale-authkey-file PATH` adds the path of a file that holds a
  Tailscale auth key. The file is read only when the node first logs in. The
  key is never copied, logged or shown. tsnet also reads `TS_AUTHKEY` from the
  gateway's environment.
- The choice lives in the gateway data folder, not in the service arguments, so
  a later `agent-net setup` without the flag keeps it.
  `--apps-tailscale-machine` removes the choice and goes back to the machine's
  node.
- With the choice, "Reachable from the internet" (`p`) starts the embedded node
  and opens a Funnel listener on port 443 of that node. The public URL is
  `https://<name>.<tailnet>.ts.net`. Without an auth key, the TUI shows the
  login link; the owner opens it once, then presses `p` again.
- The gateway serves the connected-apps handler on the Funnel listener. The
  remote address of each request is the internet client's address that Funnel
  reports, so rate limits stay per client. The loopback listener at
  `127.0.0.1:7422` stays for a pasted tunnel URL.
- Before it starts the node the first time, the gateway writes an ownership
  marker in the node state folder (`tailscale/agent-net-owned.json`).
  Uninstall, and a gateway start after `--apps-tailscale-machine`, log the node
  out (this removes it from the tailnet) and delete the state folder, but only
  when the marker is there. A route on the machine's node that an earlier mode
  left is still removed by the `funnel.json` record.
- The node state folder holds the node's private keys. It is mode 0700 and is
  not part of any output.

## Consequences

- The phone cookie and the MCP URL are on a name that only agent-net uses. The
  "other routes" warning is empty in this mode.
- Funnel must be allowed for the new node in the tailnet policy. A node that the
  owner logs in gets the owner's node attributes. A tagged auth key needs the
  `funnel` attribute for that tag.
- tsnet sends its logs to Tailscale's log service, the same as tailscaled does.
- The default does not change. The owner tests the new mode and then decides
  whether it becomes the default.

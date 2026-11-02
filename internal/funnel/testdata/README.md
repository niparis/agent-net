# Fixture sources

These are minimal, example-only JSON fixtures, not captures of this machine.
No real node, address, key, user or hostname was recorded.

The status fields follow Tailscale's ipnstate.Status and PeerStatus:
https://github.com/tailscale/tailscale/blob/main/ipn/ipnstate/ipnstate.go

The Serve/Funnel JSON follows ipn.ServeConfig, returned unchanged by both
`tailscale serve status --json` and `tailscale funnel status --json`:
https://github.com/tailscale/tailscale/blob/main/ipn/serve.go
https://github.com/tailscale/tailscale/blob/main/cmd/tailscale/cli/serve_legacy.go

The node-web example in upstream TestPrintServeStatusTrees has the same TCP,
Web and Handlers shape. Our fixture adds a foreground Funnel to exercise the
shared port inventory:
https://github.com/tailscale/tailscale/blob/main/cmd/tailscale/cli/serve_status_test.go

Error parsing sources:

- The exact Linux Access denied/operator text in TestCommandErrors came from
  the task's observed CLI output.
- enableFeatureInteractive prints the server's Text and URL to stdout. It can
  exit 0 when ShouldWait is false, before Funnel has been enabled. The test's
  login URL is an example, not an actual approval URL:
  https://github.com/tailscale/tailscale/blob/main/cmd/tailscale/cli/serve_legacy.go
- NodeCanFunnel supplies the legacy "Funnel not available" text and the help
  links used when a node lacks the capability or HTTPS:
  https://github.com/tailscale/tailscale/blob/main/ipn/serve.go
- removeWebServe supplies "handler does not exist" for repeated off commands:
  https://github.com/tailscale/tailscale/blob/main/cmd/tailscale/cli/serve_v2.go

Sources checked 2026-11-02. The TUI-facing sentences are specified by the task;
Tailscale's messages are the input to the mapping, not the displayed text.

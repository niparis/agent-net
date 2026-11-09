# 0123 - Pair the owner's tailnet browser automatically

Date: 2026-11-09

The identity is the owner's Tailscale user, not a device. Every device signed
in to the tailnet as that user auto-pairs, including machines where AI agents
run (for example the owner's laptop, or the gateway machine through another
tailnet path). An agent on such a machine can therefore fetch the console
through its own tailscaled and receive a full owner cookie. The owner
accepted this risk on 2026-11-09, in person, choosing this over limiting
auto-pairing to devices that do not run agent-net. The uid-0 check stops only
local forgery on the gateway machine.

Status: Accepted by the owner, with the agent risk above

A2 (exclude agent-net machines by `X-Forwarded-For`) was offered and declined.
The owner chose option A, auto-pairing by Tailscale user. The caller approved
the revised socket check.

## Decision

A fresh browser reaching the console over the owner's tailnet as the user who
owns the gateway machine's Tailscale node pairs without a code. Public Funnel
requests keep the code that only the owner approves in the TUI.

On Linux, trust `Tailscale-User-Login` only when the actual TCP peer's socket
has uid 0. Match both ends of the connection in `/proc/net/tcp` and
`/proc/net/tcp6`. This trusts local root processes as tailscaled. Root on the
gateway machine already owns it. An agent running as the unprivileged owner
on the gateway machine can send forged headers to the loopback listener, but
cannot create the uid-0 peer socket needed for those headers to be trusted.
Inspecting root's `/proc/<pid>/fd` is not possible from the gateway's user
service, so we do not use the original process-executable design.

Serve strips incoming identity headers before adding the verified tailnet
identity. Its `addTailscaleIdentityHeaders` function returns without adding
user identity for Funnel:
[Tailscale source](https://github.com/tailscale/tailscale/blob/main/ipn/ipnlocal/serve.go).
A header without the root socket check is never enough.

Read `Self.UserID` and `User[UserID].LoginName` from `tailscale status --json`.
Cache the login for 30 seconds and errors for five seconds. Bound command time
to 500 ms; requests during a refresh use the code flow. No new setting or
privileged service is needed. Other OSes and any missing proof use the code
flow. The separate in-process Tailscale node does not gain auto-pairing.

Create the same phone-purpose owner token and 90-day cookie as code pairing.
Record each new device in Connected apps, with a name beginning
`Tailscale sign-in: <login>, <short user agent>`, and one owner Inbox information
notice: `Paired <device> by Tailscale sign-in`. Device, token and notice share
one transaction. No agent, tool or approval request participates.

Existing cookies keep their validation, renewal and revocation. A rejected
cookie uses the code flow so revoking a device does not immediately pair it
again. Clearing cookies permits a fresh Tailscale sign-in. The unpaired
request cap, code flow and public Funnel behavior do not change.

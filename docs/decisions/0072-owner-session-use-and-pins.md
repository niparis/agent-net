# 0072. Record owner use and pins for sessions

Agent activity is not evidence that the owner uses a session. The gateway
stores owner use separately from harness activity, and the owner may pin a
session. Session snapshots carry the latest use, a decaying score, and the pin
time. A score halves for every complete day without use and increases by one
when another use arrives. This keeps recent repeated use useful for ordering
without keeping a permanent raw event history.

Opening or resuming, an owner message, and restoring an archive count as use.
Pi's interactive input and Claude Code's prompt hook also report use. Claude
cross-session deliveries do not count. OpenCode and Codex input is omitted
where the source cannot be distinguished from injected input. Signals are
best effort while the gateway is reachable. Pins use the existing owner-only
operator and gateway checks.

After an hour for daemon reconnects, and hourly thereafter, the gateway
archives offline temporary-folder sessions
and offline sessions inactive and unused by the owner for seven days. It
protects pins, recent owner use, online sessions, owners, the CTO, bots and
registered mergers. It keeps the existing archive and restore path. A restore
records use, which keeps the job from immediately hiding the restored record.

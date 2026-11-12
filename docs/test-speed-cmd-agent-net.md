# Test speed: cmd/agent-net

Measured on 2026-11-12 with `go test -race -json ./cmd/agent-net/`, one run before and one after, on a 16 core machine. Seconds per top-level test. The research runner (GOMAXPROCS=1) is about three times slower.

| Test | Before (s) | After (s) | What was slow |
|---|---|---|---|
| TestMCPSwitchesWithOpenSubscription | 29.8 | 1.6 | 2 binary builds, and 2 waits for the 10 s file check |
| TestMCPDetectsBinaryReplacementWithoutDaemonRestart | 29.7 | 7.5 | 2 builds, one wait for the 10 s file check. The 7.5 s is the one-time build of the 3 helper binaries for the package |
| TestMCPSwitchesToNewerBinary | 22.7 | 1.7 | 2 binary builds |
| TestEventsCommand | 12.4 | 11.0 | 7 s is `gateway.New` (SQLite migration under -race, in internal/gateway); 2.5 s was fixed sleeps and the 1 s follow poll |
| TestMCPStaysWhenAutoUpgradeIsOff | 6.1 | 1.4 | 2 binary builds |
| TestMCPRouting | 5.3 | 0.2 | 1 binary build |
| TestNonTUIStartupDoesNotQueryTerminalBackground | 1.3 | 1.3 | starts the race test binary again (kept: it needs a fresh process with a terminal) |
| TestUpgradeHelpExplainsUnorderableVersions | 1.3 | 1.3 | starts the race test binary again (kept: it needs a fresh process) |
| TestPolicyHookStopsAfterItsBudget | 0.7 | 0.7 | the budget is the behaviour under test |
| TestHintHookStopsAfterItsBudget | 0.3 | 0.3 | the budget is the behaviour under test |
| TestHintHookPreToolUseBudget | 0.3 | 0.3 | the budget is the behaviour under test |
| all other tests (85) | 0.0 | 0.0 | |
| Package wall clock | 111.4 | 28.7 | |

What changed:

- The helper binaries (versions v0.0.1, v0.0.2, v0.0.3) are built once per test run and copied into each test's folder. The first is built alone to fill the Go build cache, the other two at the same time.
- `agent-net mcp` checks its own file every 10 s. `AGENT_NET_MCP_WATCH_INTERVAL` (a Go duration, at least 10ms) changes that. The tests use 100ms. The default stays 10 s.
- `agent-net events --follow` polls every 1 s. The test sets `eventsFollowInterval` to 20ms and waits for the output instead of sleeping.

Not done here: the 7 s `gateway.New` migration is in `internal/gateway`.

# Test speed, 2026-11-12

The merger measured the slowest single tests on a train commit (`-race`, one
CPU per process). This file lists what each fix did. Times are seconds under
`-race` with `GOMAXPROCS=1`, measured on the research machine while its load
average was 12 to 14, so they are noisy. "Before" is the old code and "after"
is this branch, run one test at a time. A test that is not in the table was
not changed.

| Test | Before | After | The one slow cause and the fix |
|---|---|---|---|
| TestActionScreensFitEverySize | 303 | 14 | Typing 200 characters per size redrew the lists on every key. Each screen is typed once and then resized. The full size grid is kept. |
| TestHelpScrollsAndWrapsAt80 | 58 | 3 | Every frame and every key built the whole help again. The test sets the scroll offset itself, ten lines per frame, and checks the down key once. Every help line must still show in some frame. |
| TestViewFitsEverySize | 33 | 3 | Sampled sizes: the smallest, the largest and the layout breakpoints. |
| TestEverySizeFitsWithUsage | 42 | 6 | The same sampling. |
| TestTUIAgentsLiteCutsBytes | 164 | 12 | The fixture made 300 changes that the agents and nodes queries never read, and 2000-byte attributes. It makes none and uses 600 bytes. |
| TestConversationsPageTwelveThousandMessagesStaysQuick | 61 | 6 | About 2 ms per inserted row under `-race`. With `-race` the fixture is a quarter of the size and has the same shape. Without `-race` it keeps the full size. |
| TestConversationsPageGroupsTenThousandWorkerMessagesFast | 32 | 3 | The same. |
| TestConversationsPageKeepsTenThousandMessagesFast | 31 | 3 | The same. |
| TestAppsPublicOwnerFacingFunnelErrors | 48 | 14 | Each of the four cases migrated a new database (about 10 s each under load). They start from the seeded store now. The 14 s includes the one-time template build of the test binary (7 s). |
| TestExposureWarningAppearsWithPublicAddressAndNotWithout | 44 | 1 | Three gateways, each migrating a new database. They start from the seeded store. |
| TestCharterWriteSkipsConnectionsWithoutWebsocket | 16 | 6 | The same. |
| TestOtherBroadcastsSkipConnectionsWithoutWebsocket | 16 | 5 | The same. |
| TestCharterBroadcastStopsWithTheGateway | 16 | 5 | The same. |
| TestGraphRefsAutomaticCutoverBacksUpAndMigratesCharter | 15 | 2 | The same. |
| TestAutoContinuePartialHandbackStopsAtTwo | 16 | 17 | Not faster. It was flaky: after the second automatic continue the test checked for "no notice yet", but the worker hands back a third time at once, so the notice could already be in. The check is now "exactly one notice at the end". |

Other tests that use a gateway directory of their own now start from the
seeded store too: apps_public, apps_review2, apps_e2e, apps_asks, ack,
charter_notify, enroll_limit, graph_refs, owner_use, worker_task_field, invite,
install, admin, record, skillbackup, skillstore, tools_assignment and
tools_accepted tests in `internal/gateway`.

## Left

- `TestMCPDetectsBinaryReplacementWithoutDaemonRestart`,
  `TestMCPSwitchesWithOpenSubscription` (cmd/agent-net), and the
  `TestPeerMessages*`, `TestUninstall*`, `TestCodexMCP*` and
  `TestClaudeWorkersMod*` tests (internal/setup): these packages were not in
  scope.
- Every fresh gateway database costs 7 to 10 s under `-race` with one CPU. The
  cause is in `Store.migrate`: each migration runs in its own transaction, and
  each DDL statement makes SQLite parse the whole schema again. A fix in the
  product code (fewer parses, or one transaction for a fresh database) would
  speed up every test that opens a gateway store, and the template build too.
- Daemon worker tests (`TestWorkerRateAskEveryFifth`,
  `TestCodexWorkerPermissionLevels`, `TestAgyWorkerToolchainDirs`,
  `TestDaemonStopWaitsForAWorkerThatHandedBack`,
  `TestSessionStartAgentFailureReportsAndNextStarts`): alone, each worker start
  costs about 1.4 s (three child processes) and the daemon test binary pays the
  7 s template build once. Their long times in the full run come mostly from
  load. No single wait to remove.
- `TestWorkerOpenCodeDeletionRetryExpired` takes 1 s alone; its 29 s in the full
  run was load.

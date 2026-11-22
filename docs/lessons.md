# Lessons learned

These engineering lessons so far lived in the lead's private notes. Each rule is also one line in [AGENTS.md](../AGENTS.md), and this page holds the story and the places to read. They sit under the rules already in AGENTS.md, such as the session entry rules in [decision 0046](decisions/0046-open-sessions-in-herdr.md).

To add a lesson, write a section with the four parts used below (Rule, What happened, How to apply, Where). Keep it plain English, with short sentences and no em dash (rule B9). Then add one line to the "Lessons learned" section of AGENTS.md that links to the section anchor here. Name the files, tests, rules and decisions a reader needs. Do not name a session, a machine or a person.

## Locks across calls

**Rule.** Never hold a daemon or gateway lock across a call to the gateway, another process or the database. Guard only in-memory maps with a leaf lock, do I/O outside locks, and call notify hooks after the commit.

**What happened.** In November 2026 a daemon held its own lock across a gateway call and deadlocked, hanging a laptop for about two hours. On 2026-11-22 the release train hit a gateway deadlock: `AddWorkerProfile` took `Store.mu` and then waited for the single SQLite writer, while `storeMessage` held a write transaction and called `allow()`, which took `Store.mu`.

**How to apply.** A leaf lock guards one in-memory map and nothing else (`allow` now uses its own `bucketMu`). Read or write the database before you take a lock. Run notify hooks after the transaction commits. When a run hangs, read the goroutine dump: find goroutines waiting in `sync.Mutex.Lock` and in `sql.DB.Begin` or a connection, and match the pair.

**Where.** Rule B12, the lock call check in `tools/rulecheck` and `tools/rulecheck/lockcall.baseline` (that list may only shrink), and `internal/gateway/worker_profiles_lock_test.go`.

## Migrations are append-only

**Rule.** Add a new migration at the end and never rewrite one that has shipped (rule B5). A migration that adds columns checks first that each column is missing.

**What happened.** Schema work in November 2026 had to run against rewound and replayed databases, where a column could already exist. Several branches also added migrations at the same time and collided.

**How to apply.** Use the migrate loop's column list pattern in `internal/gateway/store.go`: check `pragma_table_info` before `ALTER TABLE`. Regenerate `testdata/migrations.lock` with `-update-migrations-lock`; it may only gain lines at the end. Replay tests restore every dropped table through the shared helper in `internal/gateway/schema_restore_test.go`.

**Where.** Rule B5, `internal/gateway/migrations_lock_test.go`, and decisions [0179](decisions/0179-gateway-schema-reduction.md), [0183](decisions/0183-worker-tables-rework.md), [0184](decisions/0184-org-graph-leadership-and-folds.md) and [0186](decisions/0186-oauth-clients-live-in-apps.md).

## Prefer capability checks to version gates

**Rule.** Ask the peer whether it supports the feature, and act on the answer. Do not gate a behaviour on a version number.

**What happened.** A check like "daemon >= 0.4.N" broke when the change shipped in a later release than planned. The gate was true for a version that did not have the feature, or false for one that did.

**How to apply.** Put the question beside the call that needs the feature, and keep the answer with that call. The peer's reply decides, not the version string it reports.

**Where.** The gateway link and peer calls in `internal/daemon` and `internal/protocol`. Look for the feature flag or capability field that the call already carries before you add a version comparison.

## Setup tests use temporary directories

**Rule.** Point setup tests at a temporary HOME, temporary XDG directories and the `AGENT_NET_*` directories before the code runs.

**What happened.** A setup test ran without them and overwrote the real gateway's distributed binary.

**How to apply.** Set `HOME`, `XDG_CONFIG_HOME`, `XDG_DATA_HOME` and `XDG_STATE_HOME`, plus `AGENT_NET_CONFIG_DIR`, `AGENT_NET_STATE_DIR`, `AGENT_NET_SOCKET`, `AGENT_NET_GATEWAY_DATA` and `AGENT_NET_CLAUDE_SESSIONS_DIR`, to `t.TempDir()` paths. Put this in one helper in the package instead of copying it into each test.

**Where.** Rule B3, the setup tests in `internal/setup`, and the environment variable list in [docs/reference.md](reference.md).

## Run daemon and gateway tests outside the sandbox

**Rule.** Run `internal/daemon` and `internal/gateway` outside the sandbox before you accept a schema, daemon or store change.

**What happened.** Sandboxed workers cannot open unix sockets, so tests that need one skip with `testsock.Skip`. On 2026-11-21 a stack of schema reworks passed every worker's tests but failed 32 daemon tests outside the sandbox: an unused machine invite stored as a nodes row blocked the graph cutover.

**How to apply.** After a worker hands back, run the daemon and gateway tests on the host, not in a sandbox. Treat a skipped socket test as not run, not passed. Only a run outside the sandbox shows what the worker could not see.

**Where.** `internal/testsock`, the socket notice in `scripts/test-changed.sh`, and decisions [0179](decisions/0179-gateway-schema-reduction.md) and [0184](decisions/0184-org-graph-leadership-and-folds.md).

## Run the plugin tests before a plugin or tool change

**Rule.** Run the three plugin test scripts before you accept a change to `plugins/` or to a tool name.

**What happened.** On 2026-11-22 a tool rename passed every Go test and failed CI because the plugin tests still called the removed tools.

**How to apply.** Run `sh plugins/pi/test.sh`, `sh plugins/opencode/test.sh` and `sh plugins/claude/test-agent-net-workers.sh`. This is the CI job "pi, opencode and claude plugins". A tool name lives in more than Go: update the plugins in the same change.

**Where.** The three test scripts, [decision 0176](decisions/0176-one-daemon-method-per-tool-read.md), and [decision 0180](decisions/0180-urgent-messages-reach-a-busy-session.md), which carries tool calls into each harness.

## CI is the single test gate

**Rule.** Treat the merge queue's CI as the single test gate. Locally, run only the tests your change needs.

**What happened.** Full local race runs of every package took over 100 minutes each and cost a whole night of release time on 2026-11-21.

**How to apply.** Run `scripts/test-changed.sh` before hand-back; it picks the tests from the diff. Push the train and let CI run the full packages. Do not run `go test ./...` or the whole suite locally.

**Where.** [Decision 0164](decisions/0164-merge-queue-is-the-single-test-gate.md), `scripts/test-changed.sh`, and the CI shard list in [AGENTS.md](../AGENTS.md).

## Tests must not create load

**Rule.** Tests and test briefs must not create load: no busy loops, no "run under load", and no sleep over 2 seconds.

**What happened.** On a shared machine, load runs slowed every other worker and caused timeouts that looked like bugs. A constant sleep also makes a test depend on timing instead of state.

**How to apply.** Wait for state with a polling helper and a timeout. Assert on state, never on a pause. Keep each test cheap enough to run beside the other workers.

**Where.** Rule B4, the sleep check in `tools/rulecheck`, and the polling helpers in the test packages.

## Use a separate Go build cache

**Rule.** Use a separate Go build cache for agent test runs, and keep Go test temp files in RAM where you can.

**What happened.** The shared cache grew to 26 GB. Moving test temp files to `/dev/shm` cut quiet-time SSD writes about 8 times.

**How to apply.** Set `GOCACHE=/tmp/agent-net-gocache`, shared by all workers, and never create another cache folder. Leave `GOTMPDIR` as agent-net sets it, so build folders of interrupted runs stay in one place that the daemon clears. On Linux, a test that writes large temp files can use `/dev/shm`.

**Where.** The build notes in the worker brief and the Go environment that agent-net gives each worker.

## Check adapter fixes on the real program

**Rule.** Verify a fix to an agent program adapter (Claude Code, Codex, OpenCode, Pi, agy) on the real program, not only on the fakes.

**What happened.** Fakes passed while the real CTO session broke three times in a row.

**How to apply.** Run the real program against the fixed adapter and check the path the fix touches: start a session, send a message, deliver a reply, or reload the plugin. Keep the fake test as well; it does not replace the real run.

**Where.** The adapters in `internal/daemon` and `internal/daemon/claude`, the plugins in `plugins/pi`, `plugins/opencode` and `plugins/claude/agent-net-workers`, and [decision 0180](decisions/0180-urgent-messages-reach-a-busy-session.md).

## Write screen values so screens update

**Rule.** Write a value a screen shows with `bumpStateTopics`, or that screen will not update.

**What happened.** The gateway keeps values in `gateway_state`, and no screen topic follows that table on purpose. A value written there did not reach the screens, and hiding sessions broke once.

**How to apply.** When you write a `gateway_state` value that a screen reads, call `bumpStateTopics` with the topics that show it, in `internal/gateway/topic_events.go`. Check the screen after the write, not only the value in the database.

**Where.** `internal/gateway/topic_events.go`, `internal/gateway/gateway_state.go`, and [decision 0182](decisions/0182-one-settings-system.md).

## Stack schema changes in a fixed order

**Rule.** Stack schema changes on one train in a fixed order, keep each change's commits separate, and add "Stack fix:" commits for conflicts.

**What happened.** In November 2026 many changes touched the same schema at once. Parallel branches made conflicting migrations and made a failure hard to place.

**How to apply.** Decide the order before the first branch starts. Keep one change's commits together. Resolve a conflict with a commit whose message starts "Stack fix:". When a run fails, bisect by the head of each part to find the part that broke it.

**Where.** Rule B5, [decision 0179](decisions/0179-gateway-schema-reduction.md), decisions [0183](decisions/0183-worker-tables-rework.md), [0184](decisions/0184-org-graph-leadership-and-folds.md) and [0186](decisions/0186-oauth-clients-live-in-apps.md), and the migration lock check.

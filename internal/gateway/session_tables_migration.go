package gateway

// sessionTablesMarker names the migration in the migrate loop, which adds the
// columns below before the statement runs.
const sessionTablesMarker = "session tables rework"

// sessionTablesColumns are the columns the rework adds to sessions: what a
// session announced (profiles), the idle check state (session_checks) and the
// automatic wake state (session_wakes). name_fixed is 1 when the owner or the
// merger set the name, so a new registration keeps it.
var sessionTablesColumns = []migrationColumn{
	{"sessions", "function", `TEXT NOT NULL DEFAULT ''`},
	{"sessions", "capabilities", `TEXT NOT NULL DEFAULT '[]'`},
	{"sessions", "task", `TEXT NOT NULL DEFAULT ''`},
	{"sessions", "expertise", `TEXT NOT NULL DEFAULT ''`},
	{"sessions", "task_started", `INTEGER NOT NULL DEFAULT 0`},
	{"sessions", "announced_at", `INTEGER NOT NULL DEFAULT 0`},
	{"sessions", "name_fixed", `INTEGER NOT NULL DEFAULT 0`},
	{"sessions", "announced_project", `TEXT NOT NULL DEFAULT ''`},
	{"sessions", "check_active", `INTEGER NOT NULL DEFAULT 0`},
	{"sessions", "check_waiting", `INTEGER NOT NULL DEFAULT 0`},
	{"sessions", "check_more", `INTEGER NOT NULL DEFAULT 0`},
	{"sessions", "check_open", `INTEGER NOT NULL DEFAULT 0`},
	{"sessions", "check_msgs", `INTEGER NOT NULL DEFAULT 0`},
	{"sessions", "check_day", `TEXT NOT NULL DEFAULT ''`},
	{"sessions", "check_day_count", `INTEGER NOT NULL DEFAULT 0`},
	{"sessions", "check_task_hash", `TEXT NOT NULL DEFAULT ''`},
	{"sessions", "check_announced_at", `INTEGER NOT NULL DEFAULT 0`},
	{"sessions", "check_fingerprint", `TEXT NOT NULL DEFAULT ''`},
	{"sessions", "wake_day", `TEXT NOT NULL DEFAULT ''`},
	{"sessions", "wake_day_count", `INTEGER NOT NULL DEFAULT 0`},
	{"sessions", "wake_streak", `INTEGER NOT NULL DEFAULT 0`},
	{"sessions", "wake_open", `INTEGER NOT NULL DEFAULT 0`},
	{"sessions", "wake_msgs", `INTEGER NOT NULL DEFAULT 0`},
	{"sessions", "wake_failed_at", `INTEGER NOT NULL DEFAULT 0`},
	{"sessions", "wake_failed_reason", `TEXT NOT NULL DEFAULT ''`},
	{"sessions", "wake_attempted_at", `INTEGER NOT NULL DEFAULT 0`},
}

// sessionTablesMigration folds seven session tables into sessions (owner
// review of 2026-11-21). The name and project rules:
//   - The name the tools showed was the profile name when set, else the
//     automatic one. That value moves to sessions.name, with name_fixed set.
//   - The project the TUI, the graph and routing used is the detected repo
//     project, else the announced one (announced_project). The read tools
//     now use the same rule; before, they showed an announced project over a
//     different detected repo.
//
// The start markers held two-minute holds and the failed Agent choices; they
// now live in memory (startHolds), so their rows go. A failed choice keeps its
// Desk item, which holds the reason.
const sessionTablesMigration = `
-- session tables rework: copy into sessions columns, then drop seven tables.
CREATE TABLE IF NOT EXISTS profiles (session_id TEXT PRIMARY KEY, name TEXT NOT NULL DEFAULT '', function TEXT NOT NULL DEFAULT '', project TEXT NOT NULL DEFAULT '', capabilities TEXT NOT NULL DEFAULT '[]', task TEXT NOT NULL DEFAULT '', updated_at INTEGER NOT NULL, expertise TEXT NOT NULL DEFAULT '', task_started INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS session_checks (session_id TEXT PRIMARY KEY, check_active INTEGER NOT NULL DEFAULT 0, check_waiting INTEGER NOT NULL DEFAULT 0, check_more INTEGER NOT NULL DEFAULT 0, check_open INTEGER NOT NULL DEFAULT 0, check_msgs INTEGER NOT NULL DEFAULT 0, check_day TEXT NOT NULL DEFAULT '', check_day_count INTEGER NOT NULL DEFAULT 0, check_task_hash TEXT NOT NULL DEFAULT '', check_announced_at INTEGER NOT NULL DEFAULT 0, check_fingerprint TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS session_wakes (session_id TEXT PRIMARY KEY, wake_day TEXT NOT NULL DEFAULT '', wake_day_count INTEGER NOT NULL DEFAULT 0, wake_streak INTEGER NOT NULL DEFAULT 0, wake_open INTEGER NOT NULL DEFAULT 0, wake_msgs INTEGER NOT NULL DEFAULT 0, wake_failed_at INTEGER NOT NULL DEFAULT 0, wake_failed_reason TEXT NOT NULL DEFAULT '', wake_attempted_at INTEGER NOT NULL DEFAULT 0);
UPDATE sessions SET function = p.function, capabilities = p.capabilities, task = p.task, expertise = p.expertise,
 task_started = p.task_started, announced_at = p.updated_at,
 name = CASE WHEN p.name <> '' THEN p.name ELSE sessions.name END, name_fixed = (p.name <> ''),
 announced_project = p.project
 FROM profiles p WHERE p.session_id = sessions.id;
UPDATE sessions SET check_active = c.check_active, check_waiting = c.check_waiting, check_more = c.check_more,
 check_open = c.check_open, check_msgs = c.check_msgs, check_day = c.check_day, check_day_count = c.check_day_count,
 check_task_hash = c.check_task_hash, check_announced_at = c.check_announced_at, check_fingerprint = c.check_fingerprint
 FROM session_checks c WHERE c.session_id = sessions.id;
UPDATE sessions SET wake_day = w.wake_day, wake_day_count = w.wake_day_count, wake_streak = w.wake_streak,
 wake_open = w.wake_open, wake_msgs = w.wake_msgs, wake_failed_at = w.wake_failed_at,
 wake_failed_reason = w.wake_failed_reason, wake_attempted_at = w.wake_attempted_at
 FROM session_wakes w WHERE w.session_id = sessions.id;
DROP TABLE IF EXISTS profiles;
DROP TABLE IF EXISTS session_checks;
DROP TABLE IF EXISTS session_wakes;
DROP TABLE IF EXISTS repo_start_reservations;
DROP TABLE IF EXISTS session_agent_starts;
DROP TABLE IF EXISTS session_agent_start_failures;
DROP TABLE IF EXISTS check_answer_counts;
`

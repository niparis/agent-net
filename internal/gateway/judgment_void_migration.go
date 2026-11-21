package gateway

import "strings"

// judgmentVoidColumnsMigration adds the void state to standing_orders: a
// voided row stays in the table, with the reason, but no judgment search,
// listing or brief shows it. The columns are added replay-safely in the
// migration loop.
const judgmentVoidColumnsMigration = `SELECT 1 /* standing orders void columns */;`

// voidCompactionReason is why the compaction rows are voided. It names the
// agent that found the false row.
const voidCompactionReason = "agent-net idle compaction prompt, not the owner (a_64e7e272d7794cec)"

// voidCompactionJudgmentsMigration voids every judgment that holds agent-net's
// own idle compaction command as the owner's words, bare or after a "Q: ... A: "
// prefix, with or without the escaping backslash. No row is deleted.
const voidCompactionJudgmentsMigration = `
-- void idle compaction judgments: agent-net typed this, the owner did not.
UPDATE standing_orders SET voided_at=CAST(strftime('%s','now') AS INTEGER), void_reason='` + voidCompactionReason + `'
 WHERE voided_at=0 AND (
  text LIKE '/compact Keep: the current task%' OR text LIKE '\/compact Keep: the current task%'
  OR text LIKE '%A: /compact Keep: the current task%' OR text LIKE '%A: \/compact Keep: the current task%');
`

// isSlashCommand says whether words are a slash command, such as the
// "/compact ..." line agent-net types into an idle session. A slash command is
// never the owner's decision, so the gateway does not store it as one. A
// leading backslash is the escaped form of the same line.
func isSlashCommand(words string) bool {
	words = strings.TrimPrefix(strings.TrimSpace(words), `\`)
	return strings.HasPrefix(words, "/")
}

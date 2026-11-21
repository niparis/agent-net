package gateway

// ownerWordsSortMigration adds owner_message to ask_sorts: the message of the
// owner's words that a close rested on. The chief closes an ask the owner holds
// only on his words, and the sort history must show which message it cited.
// The column is added replay-safely in the migration loop.
const ownerWordsSortMigration = `SELECT 1 /* ask sort owner message column */;`

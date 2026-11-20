package gateway

// progressReplyMigration adds two columns to messages for progress replies
// (decision 0177). progress is 1 for a progress reply. progress_until is the
// time its follow_up_at gives, in unix nanoseconds, or 0 for none. The
// owed-reply check reads both. The columns are added replay-safely in the
// migration loop.
const progressReplyMigration = `SELECT 1 /* owed reply progress columns */;`

package gateway

// deskHideRemovalMarker names the migration that deletes what the removed
// Desk history-hiding feature left behind. The migration loop drops its
// column only when it is present, so a replay is safe.
const deskHideRemovalMarker = "Desk sort history hide removal"

// deskHideRemovalMigration deletes the hide receipts the feature wrote to
// desk_log. The owner asked for the data itself to go, not just the screen
// that showed it.
const deskHideRemovalMigration = `
-- Desk sort history hide removal: the receipts of the feature are deleted.
DELETE FROM desk_log WHERE about_kind='hide_receipt';
SELECT 1 /* Desk sort history hide removal */;
`
